package bitget

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func testClient(t *testing.T, url string, retries int) *Client {
	t.Helper()
	client, err := NewClient(ClientConfig{BaseURL: url, HTTPClient: &http.Client{Timeout: time.Second}, MaxRetries: retries, RetryBaseDelay: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestClientUsesPublicV3AndChecksApplicationCode(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != instrumentsEndpoint || r.URL.Query().Get("category") != spotCategory {
			t.Errorf("неверный endpoint: %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
		if r.Header.Get("ACCESS-KEY") != "" || r.Header.Get("ACCESS-SIGN") != "" {
			t.Error("публичный запрос не должен требовать ключи")
		}
		_, _ = io.WriteString(w, "{\"code\":\"00000\",\"msg\":\"success\",\"data\":[{\"symbol\":\"BTCUSDT\"}]}")
	}))
	defer server.Close()
	client := testClient(t, server.URL, 0)
	var rows []instrument
	if _, err := client.getJSON(context.Background(), "test", instrumentsEndpoint, spotCategory, &rows); err != nil || len(rows) != 1 || rows[0].Symbol != "BTCUSDT" {
		t.Fatalf("не удалось декодировать публичный ответ: rows=%#v err=%v", rows, err)
	}
	if calls.Load() != 1 {
		t.Fatalf("ожидался один запрос, получено %d", calls.Load())
	}
}

func TestClientRejectsAPIErrorMalformedJSONAndEmptyData(t *testing.T) {
	for name, body := range map[string]string{
		"api error":    "{\"code\":\"43001\",\"msg\":\"category invalid\",\"data\":[]}",
		"malformed":    "{\"code\":",
		"missing data": "{\"code\":\"00000\",\"msg\":\"success\"}",
		"null data":    "{\"code\":\"00000\",\"msg\":\"success\",\"data\":null}",
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }))
			defer server.Close()
			client := testClient(t, server.URL, 0)
			var rows []instrument
			_, err := client.getJSON(context.Background(), "test", instrumentsEndpoint, spotCategory, &rows)
			if err == nil {
				t.Fatal("ожидалась ошибка API или JSON")
			}
			if name == "api error" {
				apiErr, ok := err.(*APIError)
				if !ok || apiErr.Code != "43001" || apiErr.Message != "category invalid" || apiErr.HTTPStatus != http.StatusOK {
					t.Fatalf("неверная APIError: %#v", err)
				}
			}
		})
	}
}

func TestClientRetries429And5xxButNot400(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if calls.Add(1) == 1 {
					if status == http.StatusTooManyRequests {
						w.Header().Set("Retry-After", "0")
					}
					w.WriteHeader(status)
					_, _ = io.WriteString(w, "{\"code\":\"50000\",\"msg\":\"retry\"}")
					return
				}
				_, _ = io.WriteString(w, "{\"code\":\"00000\",\"msg\":\"success\",\"data\":[{\"symbol\":\"BTCUSDT\"}]}")
			}))
			defer server.Close()
			var rows []instrument
			if _, err := testClient(t, server.URL, 1).getJSON(context.Background(), "test", instrumentsEndpoint, spotCategory, &rows); err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 2 || len(rows) != 1 {
				t.Fatalf("ожидался один повтор: calls=%d rows=%#v", calls.Load(), rows)
			}
		})
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(http.StatusBadRequest) }))
	defer server.Close()
	var rows []instrument
	if _, err := testClient(t, server.URL, 3).getJSON(context.Background(), "test", instrumentsEndpoint, spotCategory, &rows); err == nil || calls.Load() != 1 {
		t.Fatalf("HTTP 400 не должен повторяться: err=%v calls=%d", err, calls.Load())
	}
}

func TestClientHonorsContextTimeoutAndResponseLimit(t *testing.T) {
	t.Run("context", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
		defer server.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		var rows []instrument
		if _, err := testClient(t, server.URL, 0).getJSON(ctx, "test", instrumentsEndpoint, spotCategory, &rows); err == nil {
			t.Fatal("ожидалась ошибка отменённого запроса")
		}
	})
	t.Run("response limit", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, "{\"code\":\"00000\",\"msg\":\"success\",\"data\":[\"very-long-response\"]}")
		}))
		defer server.Close()
		client, err := NewClient(ClientConfig{BaseURL: server.URL, HTTPClient: &http.Client{Timeout: time.Second}, MaxResponseSize: 20})
		if err != nil {
			t.Fatal(err)
		}
		var rows []instrument
		if _, err := client.getJSON(context.Background(), "test", instrumentsEndpoint, spotCategory, &rows); err == nil {
			t.Fatal("слишком большой ответ должен быть отклонён")
		}
	})
}
