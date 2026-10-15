package mexc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func testClient(t *testing.T, baseURL string, retries int) *Client {
	t.Helper()
	client, err := NewClient(ClientConfig{
		SpotBaseURL:     baseURL,
		FuturesBaseURL:  baseURL,
		HTTPClient:      &http.Client{Timeout: time.Second},
		MaxRetries:      retries,
		RetryBaseDelay:  time.Millisecond,
		MaxResponseSize: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestClientSpotSuccessAndFuturesEnvelope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case spotExchangeInfoEndpoint:
			_, _ = w.Write([]byte(`{"symbols":[]}`))
		case futuresContractInfoEndpoint:
			_, _ = w.Write([]byte(`{"success":true,"code":0,"data":[{"symbol":"BTC_USDT"}]}`))
		default:
			t.Fatalf("неожиданный endpoint: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := testClient(t, server.URL, 0)
	var spot spotExchangeInfoResponse
	receivedAt, err := client.getJSON(context.Background(), false, "Spot test", spotExchangeInfoEndpoint, nil, &spot)
	if err != nil {
		t.Fatal(err)
	}
	if receivedAt.IsZero() || spot.Symbols == nil {
		t.Fatalf("неверный ответ Spot: receivedAt=%v response=%#v", receivedAt, spot)
	}

	var futures futuresContractList
	receivedAt, err = client.getJSON(context.Background(), true, "Futures test", futuresContractInfoEndpoint, nil, &futures)
	if err != nil {
		t.Fatal(err)
	}
	if receivedAt.IsZero() || len(futures) != 1 || futures[0].Symbol != "BTC_USDT" {
		t.Fatalf("неверный ответ Futures: receivedAt=%v response=%#v", receivedAt, futures)
	}
}

func TestClientDetectsAPIErrorInHTTP200(t *testing.T) {
	for _, tc := range []struct {
		name    string
		futures bool
		body    string
	}{
		{name: "spot", body: `{"code":-1121,"msg":"Invalid symbol"}`},
		{name: "futures", futures: true, body: `{"success":false,"code":500,"message":"temporarily unavailable","data":null}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()

			client := testClient(t, server.URL, 0)
			var response jsonRawTarget
			_, err := client.getJSON(context.Background(), tc.futures, "API error test", spotExchangeInfoEndpoint, nil, &response)
			if err == nil {
				t.Fatal("ожидалась ошибка API в HTTP 200")
			}
			if !strings.Contains(err.Error(), "Invalid symbol") && !strings.Contains(err.Error(), "temporarily unavailable") {
				t.Fatalf("неожиданная ошибка: %v", err)
			}
		})
	}
}

type jsonRawTarget struct {
	Raw json.RawMessage
}

func TestClientRetries429And5xxButNot418(t *testing.T) {
	for _, tc := range []struct {
		name       string
		firstStatus int
		retries    int
		wantCalls  int32
	}{
		{name: "429", firstStatus: http.StatusTooManyRequests, retries: 1, wantCalls: 2},
		{name: "5xx", firstStatus: http.StatusServiceUnavailable, retries: 1, wantCalls: 2},
		{name: "418", firstStatus: 418, retries: 3, wantCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if calls.Add(1) == 1 {
					if tc.firstStatus == http.StatusTooManyRequests {
						w.Header().Set("Retry-After", "0")
					}
					w.WriteHeader(tc.firstStatus)
					_, _ = w.Write([]byte(`{"code":-1000,"msg":"temporary"}`))
					return
				}
				_, _ = w.Write([]byte(`{"symbols":[]}`))
			}))
			defer server.Close()

			client := testClient(t, server.URL, tc.retries)
			var response spotExchangeInfoResponse
			_, _ = client.getJSON(context.Background(), false, "retry test", spotExchangeInfoEndpoint, nil, &response)
			if got := calls.Load(); got != tc.wantCalls {
				t.Fatalf("calls=%d, ожидалось %d", got, tc.wantCalls)
			}
		})
	}
}

func TestClientRejectsMalformedJSONAndOversizedResponse(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		size int64
		want string
	}{
		{name: "malformed", body: `{"symbols":`, size: 1024, want: "разобрать JSON"},
		{name: "oversized", body: `{"symbols":[{"symbol":"BTCUSDT"}]}`, size: 8, want: "превышает лимит"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()

			client, err := NewClient(ClientConfig{
				SpotBaseURL: server.URL, FuturesBaseURL: server.URL,
				HTTPClient: &http.Client{Timeout: time.Second},
				MaxRetries: 0, MaxResponseSize: tc.size,
			})
			if err != nil {
				t.Fatal(err)
			}
			var response spotExchangeInfoResponse
			_, err = client.getJSON(context.Background(), false, "test", spotExchangeInfoEndpoint, nil, &response)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ожидалась ошибка %q, получено %v", tc.want, err)
			}
		})
	}
}

func TestClientHonorsContextCancellation(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()

	client := testClient(t, server.URL, 3)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		var response spotExchangeInfoResponse
		_, err := client.getJSON(ctx, false, "cancel test", spotExchangeInfoEndpoint, nil, &response)
		done <- err
	}()
	<-started
	cancel()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("ожидалась ошибка отменённого контекста")
		}
	case <-time.After(time.Second):
		t.Fatal("запрос не завершился после отмены context")
	}
}
