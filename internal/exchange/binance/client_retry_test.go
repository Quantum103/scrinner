package binance

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientRetriesHTTP5xx(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("temporarily unavailable"))
			return
		}
		_, _ = w.Write([]byte(`{"symbols":[]}`))
	}))
	defer server.Close()

	client := testClient(t, server.URL, 1)
	var response spotExchangeInfoResponse
	if _, err := client.getJSON(context.Background(), false, "test", spotExchangeInfoEndpoint, nil, &response); err != nil {
		t.Fatal(err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("ожидался один ограниченный повтор, вызовов: %d", got)
	}
}

func TestClientTimeoutIsNotRetriedAfterDeadline(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		<-r.Context().Done()
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{
		SpotBaseURL: server.URL, FuturesBaseURL: server.URL,
		HTTPClient: &http.Client{Timeout: 20 * time.Millisecond},
		MaxRetries: 3, RetryBaseDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.getJSON(context.Background(), false, "test", spotExchangeInfoEndpoint, nil, &spotExchangeInfoResponse{})
	if err == nil {
		t.Fatal("ожидалось завершение по HTTP timeout")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("запрос после timeout был повторён, calls=%d", got)
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	if delay, ok := parseRetryAfter("2", now); !ok || delay != 2*time.Second {
		t.Fatalf("не разобран Retry-After в секундах: delay=%v ok=%t", delay, ok)
	}
	if delay, ok := parseRetryAfter("0", now); !ok || delay != 0 {
		t.Fatalf("Retry-After: 0 должен означать отсутствие ожидания: delay=%v ok=%t", delay, ok)
	}
	if _, ok := parseRetryAfter("", now); ok {
		t.Fatal("пустой Retry-After не должен считаться предоставленным")
	}
	if _, ok := parseRetryAfter("not-a-date", now); ok {
		t.Fatal("некорректный Retry-After не должен считаться валидным")
	}
}
