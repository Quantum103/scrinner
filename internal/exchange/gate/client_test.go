package gate

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(t *testing.T, baseURL string, retries int, timeout time.Duration) *Client {
	t.Helper()
	client, err := NewClient(ClientConfig{
		BaseURL: baseURL, HTTPClient: &http.Client{Timeout: timeout},
		MaxRetries: retries, RetryBaseDelay: time.Millisecond, MaxResponseSize: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestClientDecodesArrayAndBuildsCorrectURL(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/spot/currency_pairs" {
			t.Errorf("путь запроса = %q", r.URL.Path)
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("не установлен Accept")
		}
		_, _ = io.WriteString(w, "[{\"id\":\"BTC_USDT\",\"base\":\"BTC\",\"quote\":\"USDT\",\"trade_status\":\"tradable\",\"type\":\"normal\"}]")
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, 0, time.Second)
	var pairs []spotCurrencyPair
	receivedAt, err := client.getJSON(context.Background(), "тест пар", spotCurrencyPairsEndpoint, &pairs)
	if err != nil {
		t.Fatal(err)
	}
	if receivedAt.IsZero() || len(pairs) != 1 || pairs[0].ID != "BTC_USDT" {
		t.Fatalf("неверно декодирован ответ: время=%v пары=%#v", receivedAt, pairs)
	}
}

func TestClientRejectsAPIErrorObjectEvenWithHTTP200(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "{\"label\":\"INVALID_PARAM_VALUE\",\"message\":\"bad request\"}")
	}))
	defer server.Close()
	client := newTestClient(t, server.URL, 0, time.Second)

	var pairs []spotCurrencyPair
	_, err := client.getJSON(context.Background(), "ошибка API", spotCurrencyPairsEndpoint, &pairs)
	if err == nil || !strings.Contains(err.Error(), "INVALID_PARAM_VALUE") || !strings.Contains(err.Error(), "bad request") {
		t.Fatalf("ожидалась прикладная ошибка Gate, получено %v", err)
	}
}

func TestClientRejectsMalformedUnexpectedAndOversizedResponses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
		size int64
		want string
	}{
		{name: "malformed JSON", body: "[", size: 1024, want: "разобрать JSON"},
		{name: "unexpected object", body: "{\"data\":[]}", size: 1024, want: "объект вместо ожидаемого массива"},
		{name: "empty body", body: "", size: 1024, want: "пустой JSON-ответ"},
		{name: "null body", body: "null", size: 1024, want: "пустой JSON-ответ"},
		{name: "oversized", body: "[{\"id\":\"BTC_USDT\",\"base\":\"BTC\"}]", size: 8, want: "превышает лимит"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			client, err := NewClient(ClientConfig{
				BaseURL: server.URL, HTTPClient: &http.Client{Timeout: time.Second},
				MaxRetries: 0, RetryBaseDelay: time.Millisecond, MaxResponseSize: tc.size,
			})
			if err != nil {
				t.Fatal(err)
			}
			var pairs []spotCurrencyPair
			_, err = client.getJSON(context.Background(), "формат ответа", spotCurrencyPairsEndpoint, &pairs)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ожидалась ошибка %q, получено %v", tc.want, err)
			}
		})
	}
}

func TestClientRetries429And5xx(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if calls.Add(1) == 1 {
					w.Header().Set("Retry-After", "0")
					w.WriteHeader(status)
					_, _ = io.WriteString(w, "{\"label\":\"TOO_MANY_REQUESTS\",\"message\":\"temporary\"}")
					return
				}
				_, _ = io.WriteString(w, "[]")
			}))
			defer server.Close()
			client := newTestClient(t, server.URL, 1, time.Second)
			var pairs []spotCurrencyPair
			if _, err := client.getJSON(context.Background(), "retry", spotCurrencyPairsEndpoint, &pairs); err != nil {
				t.Fatal(err)
			}
			if got := calls.Load(); got != 2 {
				t.Fatalf("выполнено %d запросов, ожидалось 2", got)
			}
		})
	}
}

func TestClientDoesNotRetryClientErrors(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, "{\"label\":\"INVALID_PARAM_VALUE\",\"message\":\"invalid\"}")
	}))
	defer server.Close()
	client := newTestClient(t, server.URL, 3, time.Second)
	var pairs []spotCurrencyPair
	_, err := client.getJSON(context.Background(), "no retry", spotCurrencyPairsEndpoint, &pairs)
	if err == nil || calls.Load() != 1 {
		t.Fatalf("ожидалась одна попытка и ошибка, запросов=%d err=%v", calls.Load(), err)
	}
}

func TestClientNetworkErrorTimeoutAndCancellation(t *testing.T) {
	t.Parallel()

	closedServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	closedURL := closedServer.URL
	closedServer.Close()
	client := newTestClient(t, closedURL, 0, time.Second)
	var pairs []spotCurrencyPair
	if _, err := client.getJSON(context.Background(), "network error", spotCurrencyPairsEndpoint, &pairs); err == nil {
		t.Fatal("ожидалась сетевая ошибка")
	}

	slowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(150 * time.Millisecond):
			_, _ = io.WriteString(w, "[]")
		case <-r.Context().Done():
			return
		}
	}))
	defer slowServer.Close()
	client = newTestClient(t, slowServer.URL, 0, 20*time.Millisecond)
	if _, err := client.getJSON(context.Background(), "timeout", spotCurrencyPairsEndpoint, &pairs); err == nil {
		t.Fatal("ожидалась ошибка timeout")
	}

	started := make(chan struct{})
	cancelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer cancelServer.Close()
	client = newTestClient(t, cancelServer.URL, 2, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := client.getJSON(ctx, "cancel", spotCurrencyPairsEndpoint, &pairs)
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

func TestParseRetryAfterAndErrorJSON(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	if delay, ok := parseRetryAfter("3", now); !ok || delay != 3*time.Second {
		t.Fatalf("неверная задержка Retry-After: %v %v", delay, ok)
	}
	if delay, ok := parseRetryAfter("0", now); !ok || delay != 0 {
		t.Fatalf("неверный нулевой Retry-After: %v %v", delay, ok)
	}
	if _, ok := parseRetryAfter("not-a-date", now); ok {
		t.Fatal("некорректный Retry-After должен игнорироваться")
	}
	var payload apiErrorResponse
	if err := json.Unmarshal([]byte("{\"label\":\"BAD\",\"message\":\"bad\"}"), &payload); err != nil || payload.Label != "BAD" {
		t.Fatalf("неверно декодирована ошибка API: %#v err=%v", payload, err)
	}
}
