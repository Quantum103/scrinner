package okx

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
		BaseURL:         baseURL,
		HTTPClient:      &http.Client{Timeout: timeout},
		MaxRetries:      retries,
		RetryBaseDelay:  time.Millisecond,
		MaxResponseSize: 1 << 20,
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestClientDecodesSuccessfulEnvelopeAndChecksAPICode(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/error" {
			_, _ = io.WriteString(w, "{\"code\":\"50011\",\"msg\":\"rate limited\",\"data\":[]}")
			return
		}
		_, _ = io.WriteString(w, "{\"code\":\"0\",\"msg\":\"\",\"data\":[{\"instId\":\"BTC-USDT\"}]}")
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, 0, time.Second)
	var data []struct {
		InstID string `json:"instId"`
	}
	if _, err := client.getJSON(context.Background(), "успешный ответ", "/success", nil, &data); err != nil {
		t.Fatal(err)
	}
	if len(data) != 1 || data[0].InstID != "BTC-USDT" {
		t.Fatalf("получены неожиданные данные: %#v", data)
	}
	_, err := client.getJSON(context.Background(), "ошибка API", "/error", nil, &data)
	if err == nil || !strings.Contains(err.Error(), "50011") || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("ожидалась ошибка прикладного API-кода, получено %v", err)
	}
}

func TestClientRejectsMalformedMissingAndOversizedResponses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		body string
		size int64
		want string
	}{
		{name: "malformed JSON", body: "{\"code\":", size: 1024, want: "разобрать JSON"},
		{name: "missing code", body: "{\"msg\":\"no code\",\"data\":[]}", size: 1024, want: "code"},
		{name: "missing data", body: "{\"code\":\"0\",\"msg\":\"\"}", size: 1024, want: "не содержит data"},
		{name: "oversized", body: "{\"code\":\"0\",\"msg\":\"\",\"data\":[{\"instId\":\"BTC-USDT\"}]}", size: 8, want: "превышает лимит"},
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
			var result []map[string]string
			_, err = client.getJSON(context.Background(), "тест", "/", nil, &result)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ожидалась ошибка %q, получено %v", tc.want, err)
			}
		})
	}
}

func TestClientRetriesHTTP429And5xx(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if calls.Add(1) == 1 {
					w.Header().Set("Retry-After", "0")
					w.WriteHeader(status)
					_, _ = io.WriteString(w, "{\"code\":\"50011\",\"msg\":\"temporary\"}")
					return
				}
				_, _ = io.WriteString(w, "{\"code\":\"0\",\"msg\":\"\",\"data\":[]}")
			}))
			defer server.Close()
			client := newTestClient(t, server.URL, 1, time.Second)
			var result []map[string]string
			if _, err := client.getJSON(context.Background(), "retry", "/", nil, &result); err != nil {
				t.Fatal(err)
			}
			if got := calls.Load(); got != 2 {
				t.Fatalf("выполнено %d запросов, ожидалось 2", got)
			}
		})
	}
}

func TestClientReportsNetworkErrorAndTimeout(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			time.Sleep(100 * time.Millisecond)
		}
		_, _ = io.WriteString(w, "{\"code\":\"0\",\"msg\":\"\",\"data\":[]}")
	}))
	slowURL := server.URL
	server.Close()

	client := newTestClient(t, slowURL, 0, 30*time.Millisecond)
	var result []json.RawMessage
	if _, err := client.getJSON(context.Background(), "network error", "/", nil, &result); err == nil {
		t.Fatal("ожидалась сетевая ошибка после закрытия сервера")
	}

	slowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(150 * time.Millisecond):
			_, _ = io.WriteString(w, "{\"code\":\"0\",\"msg\":\"\",\"data\":[]}")
		case <-r.Context().Done():
			return
		}
	}))
	defer slowServer.Close()
	client = newTestClient(t, slowServer.URL, 0, 20*time.Millisecond)
	if _, err := client.getJSON(context.Background(), "timeout", "/slow", nil, &result); err == nil {
		t.Fatal("ожидалась ошибка timeout")
	}
}

func TestClientHonorsContextCancellation(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, 3, time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		var data []json.RawMessage
		_, err := client.getJSON(ctx, "cancel", "/", nil, &data)
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

func TestParseRetryAfter(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	if delay, ok := parseRetryAfter("3", now); !ok || delay != 3*time.Second {
		t.Fatalf("неверная задержка Retry-After: %v %v", delay, ok)
	}
	if delay, ok := parseRetryAfter("0", now); !ok || delay != 0 {
		t.Fatalf("неверный нулевой Retry-After: %v %v", delay, ok)
	}
	if _, ok := parseRetryAfter("not-a-date", now); ok {
		t.Fatal("некорректный Retry-After должен быть проигнорирован")
	}
}
