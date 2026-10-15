package mexc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestFuturesEnvelopeRejectsMissingSuccessAndData(t *testing.T) {
	for _, body := range []string{
		`{"code":0,"data":[]}`,
		`{"success":true,"code":0}`,
		`{"success":true,"data":[]}`,
		`{"success":false,"code":700,"message":"contract unavailable"}`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(body))
		}))
		client := testClient(t, server.URL, 0)
		var response futuresContractList
		_, err := client.getJSON(context.Background(), true, "envelope test", futuresContractInfoEndpoint, nil, &response)
		server.Close()
		if err == nil {
			t.Fatalf("ожидалась ошибка для ответа %s", body)
		}
	}
}

func TestClientDoesNotRetryInvalidParameters(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":-1001,"msg":"invalid parameter"}`))
	}))
	defer server.Close()

	client := testClient(t, server.URL, 3)
	var response spotExchangeInfoResponse
	_, err := client.getJSON(context.Background(), false, "invalid request", spotExchangeInfoEndpoint, nil, &response)
	if err == nil || calls.Load() != 1 {
		t.Fatalf("ошибка запроса должна возвращаться без повторов: err=%v calls=%d", err, calls.Load())
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
	var response spotExchangeInfoResponse
	_, err = client.getJSON(context.Background(), false, "timeout test", spotExchangeInfoEndpoint, nil, &response)
	if err == nil {
		t.Fatal("ожидалась ошибка timeout")
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("после timeout запрос не должен повторяться: calls=%d", got)
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	if delay, ok := parseRetryAfter("2", now); !ok || delay != 2*time.Second {
		t.Fatalf("неверный Retry-After в секундах: %v %t", delay, ok)
	}
	if delay, ok := parseRetryAfter("0", now); !ok || delay != 0 {
		t.Fatalf("Retry-After 0 должен сохраняться как предоставленный: %v %t", delay, ok)
	}
	if _, ok := parseRetryAfter("", now); ok {
		t.Fatal("пустой Retry-After не должен считаться заданным")
	}
	if _, ok := parseRetryAfter("not-a-date", now); ok {
		t.Fatal("некорректный Retry-After не должен считаться заданным")
	}
}

func TestSpotErrorWithMalformedPayloadIsStillRejectedByJSONDecoder(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"symbols":`))
	}))
	defer server.Close()

	client := testClient(t, server.URL, 0)
	var response json.RawMessage
	_, err := client.getJSON(context.Background(), false, "malformed", spotExchangeInfoEndpoint, nil, &response)
	if err == nil || !strings.Contains(err.Error(), "разобрать JSON") {
		t.Fatalf("ожидалась ошибка JSON, получено: %v", err)
	}
}
