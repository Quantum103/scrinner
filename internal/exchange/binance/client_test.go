package binance

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

func TestClientGetJSONSuccessAndReceiveTime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != spotExchangeInfoEndpoint {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"symbols":[]}`))
	}))
	defer server.Close()

	client := testClient(t, server.URL, 0)
	var response spotExchangeInfoResponse
	receivedAt, err := client.getJSON(context.Background(), false, "test", spotExchangeInfoEndpoint, nil, &response)
	if err != nil {
		t.Fatal(err)
	}
	if receivedAt.IsZero() || receivedAt.Location() != time.UTC {
		t.Fatalf("неверное время получения: %v", receivedAt)
	}
	if response.Symbols == nil {
		t.Fatal("ожидался непустой JSON-массив, даже если в нём нет инструментов")
	}
}

func TestClientRejectsMalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"symbols":`))
	}))
	defer server.Close()

	client := testClient(t, server.URL, 0)
	_, err := client.getJSON(context.Background(), false, "test", spotExchangeInfoEndpoint, nil, &spotExchangeInfoResponse{})
	if err == nil || !strings.Contains(err.Error(), "разобрать JSON") {
		t.Fatalf("ожидалась ошибка JSON, получено: %v", err)
	}
}

func TestClientDoesNotRetryBadRequestOrHTTP418(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, 418} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"code":-1003,"msg":"blocked"}`))
			}))
			defer server.Close()

			client := testClient(t, server.URL, 3)
			_, err := client.getJSON(context.Background(), false, "test", spotExchangeInfoEndpoint, nil, &spotExchangeInfoResponse{})
			if err == nil {
				t.Fatal("ожидалась HTTP-ошибка")
			}
			if got := calls.Load(); got != 1 {
				t.Fatalf("неожиданное число запросов: %d", got)
			}
		})
	}
}

func TestClientRetriesHTTP429AndHonorsRetryAfter(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"code":-1003,"msg":"rate limit"}`))
			return
		}
		_, _ = w.Write([]byte(`{"symbols":[]}`))
	}))
	defer server.Close()

	client := testClient(t, server.URL, 1)
	client.retryBaseDelay = time.Millisecond
	var response spotExchangeInfoResponse
	if _, err := client.getJSON(context.Background(), false, "test", spotExchangeInfoEndpoint, nil, &response); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("ожидалось 2 запроса, получено %d", got)
	}
}

func TestClientRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"symbols":[{"symbol":"` + strings.Repeat("A", 128) + `"}]}`))
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{
		SpotBaseURL: server.URL, FuturesBaseURL: server.URL,
		HTTPClient: &http.Client{Timeout: time.Second},
		MaxRetries: 0, MaxResponseSize: 32,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.getJSON(context.Background(), false, "test", spotExchangeInfoEndpoint, nil, &spotExchangeInfoResponse{})
	if err == nil || !strings.Contains(err.Error(), "превышает лимит") {
		t.Fatalf("ожидалась ошибка размера ответа, получено: %v", err)
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
		_, err := client.getJSON(ctx, false, "test", spotExchangeInfoEndpoint, nil, &spotExchangeInfoResponse{})
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
