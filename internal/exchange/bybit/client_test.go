package bybit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != instrumentEndpoint {
			t.Fatalf("path=%s", r.URL.Path)
		}
		if r.URL.Query().Get("category") != spotCategory {
			t.Fatalf("category=%s", r.URL.Query().Get("category"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"retCode":0,"retMsg":"OK","result":{"category":"spot","list":[]},"time":1735809771618}`))
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}

	var result instrumentsResult
	envelope, err := client.getJSON(context.Background(), "test", instrumentEndpoint, mapStringValues("category", spotCategory), &result)
	if err != nil {
		t.Fatal(err)
	}
	if envelope.RetCode != 0 || result.Category != spotCategory {
		t.Fatalf("unexpected response: %#v %#v", envelope, result)
	}
}

func TestClientAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"retCode":10001,"retMsg":"invalid category","result":{},"time":1735809771618}`))
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{BaseURL: server.URL, MaxRetries: 0})
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.getJSON(context.Background(), "test", instrumentEndpoint, nil, &instrumentsResult{})
	if err == nil {
		t.Fatal("ожидалась API ошибка")
	}

	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("тип ошибки=%T", err)
	}
	if apiErr.Code != 10001 || apiErr.Message != "invalid category" {
		t.Fatalf("ошибка=%#v", apiErr)
	}
}

func TestClientMalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"retCode":0,"retMsg":"OK","result":`))
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{BaseURL: server.URL, MaxRetries: 0})
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.getJSON(context.Background(), "test", instrumentEndpoint, nil, &instrumentsResult{})
	if err == nil || !strings.Contains(err.Error(), "разобрать JSON") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClientRetriesHTTP500(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("temporary"))
			return
		}
		_, _ = w.Write([]byte(`{"retCode":0,"retMsg":"OK","result":{"category":"spot","list":[]},"time":1735809771618}`))
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{
		BaseURL:        server.URL,
		MaxRetries:     1,
		RetryBaseDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}

	var result instrumentsResult
	_, err = client.getJSON(context.Background(), "test", instrumentEndpoint, nil, &result)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("calls=%d", calls)
	}
}

func TestClientDoesNotRetryInvalidParameter(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"retCode":10001,"retMsg":"invalid parameter","result":{},"time":1735809771618}`))
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{BaseURL: server.URL, MaxRetries: 3, RetryBaseDelay: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}

	_, err = client.getJSON(context.Background(), "test", instrumentEndpoint, nil, &instrumentsResult{})
	if err == nil {
		t.Fatal("ожидалась ошибка")
	}
	if calls != 1 {
		t.Fatalf("invalid parameter был повторён %d раз", calls)
	}
}

func TestClientContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{BaseURL: server.URL, MaxRetries: 2, RetryBaseDelay: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err = client.getJSON(ctx, "test", instrumentEndpoint, nil, &instrumentsResult{})
	if err == nil {
		t.Fatal("ожидалась отмена context")
	}
}

func mapStringValues(key, value string) map[string][]string {
	return map[string][]string{key: {value}}
}
