package bybit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/example/crypto-arbitrage-scanner/internal/market"
)

func TestCollectTickersConvertsAndFilters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		category := r.URL.Query().Get("category")
		switch category {
		case spotCategory:
			_, _ = w.Write([]byte(`{"retCode":0,"retMsg":"OK","result":{"category":"spot","list":[{"symbol":"BTCUSDT","bid1Price":"65000.10","ask1Price":"65000.20","volume24h":"12345.67"},{"symbol":"ETHUSDT","bid1Price":"","ask1Price":"2000","volume24h":"10"},{"symbol":"XRPUSDT","bid1Price":"1","ask1Price":"0.9","volume24h":"10"},{"symbol":"NOTREQUESTED","bid1Price":"1","ask1Price":"2","volume24h":"5"}]},"time":1735809771618}`))
		case linearCategory:
			_, _ = w.Write([]byte(`{"retCode":0,"retMsg":"OK","result":{"category":"linear","list":[{"symbol":"BTCUSDT","bid1Price":"64990.00","ask1Price":"64990.10","volume24h":"9999.1"},{"symbol":"ETHUSDT","bid1Price":"3000","ask1Price":"3010","volume24h":"bad"}]},"time":1735809772618}`))
		default:
			t.Fatalf("unexpected category=%s", category)
		}
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	adapter := New(client, nil)

	markets := []market.Market{
		{Exchange: "bybit", Symbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT", MarketType: market.MarketTypeSpot, ContractType: market.ContractTypeSpot, Active: true},
		{Exchange: "bybit", Symbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT", MarketType: market.MarketTypeFutures, ContractType: market.ContractTypePerpetual, Active: true},
		{Exchange: "bybit", Symbol: "ETHUSDT", BaseAsset: "ETH", QuoteAsset: "USDT", MarketType: market.MarketTypeFutures, ContractType: market.ContractTypePerpetual, Active: true},
	}

	tickers, stats, err := adapter.CollectTickersWithStats(context.Background(), markets)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Received != 6 || stats.Valid != 3 || stats.Skipped != 3 {
		t.Fatalf("stats=%#v", stats)
	}
	if len(tickers) != 3 {
		t.Fatalf("tickers=%d %#v", len(tickers), tickers)
	}

	for _, ticker := range tickers {
		if ticker.Exchange != "bybit" || ticker.Symbol == "" || !ticker.IsValid() {
			t.Fatalf("invalid ticker=%#v", ticker)
		}
		if ticker.Timestamp.IsZero() || ticker.Timestamp.Location() != time.UTC {
			t.Fatalf("timestamp=%v", ticker.Timestamp)
		}
		if ticker.Symbol == "BTCUSDT" && ticker.MarketType == market.MarketTypeSpot {
			if ticker.ContractType != market.ContractTypeSpot {
				t.Fatalf("spot contract type=%s", ticker.ContractType)
			}
			if ticker.Volume24h == nil {
				t.Fatal("Spot volume должен сохраниться")
			}
		}
		if ticker.Symbol == "ETHUSDT" {
			if ticker.MarketType != market.MarketTypeFutures || ticker.ContractType != market.ContractTypePerpetual {
				t.Fatalf("linear type=%s/%s", ticker.MarketType, ticker.ContractType)
			}
			if ticker.Volume24h != nil {
				t.Fatal("некорректный volume не должен становиться фиктивным значением")
			}
		}
	}
}

func TestFetchTickersRejectsMissingResponseTime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"retCode":0,"retMsg":"OK","result":{"category":"spot","list":[]},"time":0}`))
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	adapter := New(client, nil)

	_, _, err = adapter.fetchTickers(context.Background(), spotCategory, map[string]struct{}{"BTCUSDT": {}})
	if err == nil {
		t.Fatal("ожидалась ошибка некорректного timestamp")
	}
}
