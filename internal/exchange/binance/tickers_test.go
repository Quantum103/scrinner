package binance

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/example/crypto-arbitrage-scanner/internal/market"
	"github.com/shopspring/decimal"
)

func TestCollectTickersKeepsSpotAndPerpetualSeparate(t *testing.T) {
	const futuresTime int64 = 1735809771618
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case spotBookTickerEndpoint:
			_, _ = w.Write([]byte(`[
				{"symbol":"BTCUSDT","bidPrice":"0.000000123456789","bidQty":"1","askPrice":"0.000000123456799","askQty":"2"},
				{"symbol":"ETHUSDT","bidPrice":"bad","bidQty":"1","askPrice":"2000","askQty":"2"},
				{"symbol":"NOTREQUESTED","bidPrice":"1","bidQty":"1","askPrice":"2","askQty":"2"}
			]`))
		case spot24hrTickerEndpoint:
			if r.URL.Query().Get("symbols") == "" {
				t.Fatal("для Spot статистики должен передаваться symbols")
			}
			_, _ = w.Write([]byte(`[
				{"symbol":"BTCUSDT","volume":"15342.7821","quoteVolume":"1"},
				{"symbol":"ETHUSDT","volume":"not-a-number"}
			]`))
		case futuresBookTickerEndpoint:
			_, _ = w.Write([]byte(`[
				{"symbol":"BTCUSDT","bidPrice":"1.0123456789","bidQty":"3","askPrice":"1.0123456790","askQty":"4","time":1735809771618},
				{"symbol":"ETHUSDT","bidPrice":"10","bidQty":"1","askPrice":"9","askQty":"1","time":1735809771618}
			]`))
		case futures24hrTickerEndpoint:
			_, _ = w.Write([]byte(`[
				{"symbol":"BTCUSDT","volume":"151.1234","quoteVolume":"999999"}
			]`))
		default:
			t.Fatalf("unexpected endpoint %s", r.URL.Path)
		}
	}))
	defer server.Close()

	adapter := New(testClient(t, server.URL, 0), nil)
	markets := []market.Market{
		{Exchange: "binance", Symbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT", MarketType: market.MarketTypeSpot, ContractType: market.ContractTypeSpot, Active: true},
		{Exchange: "binance", Symbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT", MarketType: market.MarketTypeFutures, ContractType: market.ContractTypePerpetual, Active: true},
	}
	tickers, stats, err := adapter.CollectTickersWithStats(context.Background(), markets)
	if err != nil {
		t.Fatal(err)
	}
	if len(tickers) != 2 || stats.Received != 5 || stats.Valid != 2 || stats.Skipped != 3 {
		t.Fatalf("tickers=%d stats=%#v", len(tickers), stats)
	}

	var spot, futures bool
	for _, ticker := range tickers {
		if !ticker.IsValid() || ticker.Exchange != "binance" || ticker.Symbol != "BTCUSDT" {
			t.Fatalf("invalid ticker: %#v", ticker)
		}
		switch {
		case ticker.MarketType == market.MarketTypeSpot:
			spot = true
			wantBid, _ := decimal.NewFromString("0.000000123456789")
			wantAsk, _ := decimal.NewFromString("0.000000123456799")
			wantVolume, _ := decimal.NewFromString("15342.7821")
			if !ticker.Bid.Equal(wantBid) || !ticker.Ask.Equal(wantAsk) {
				t.Fatalf("Spot precision lost: bid=%s ask=%s", ticker.Bid, ticker.Ask)
			}
			if ticker.Volume24h == nil || !ticker.Volume24h.Equal(wantVolume) {
				t.Fatalf("Spot base volume incorrect: %#v", ticker.Volume24h)
			}
			if ticker.ExchangeTimestamp != nil {
				t.Fatal("Spot bookTicker не сообщает exchange timestamp")
			}
			if ticker.ReceivedAt.IsZero() || !ticker.Timestamp.Equal(ticker.ReceivedAt) {
				t.Fatalf("для Spot нужен receive timestamp: %#v", ticker)
			}
		case ticker.MarketType == market.MarketTypeFutures:
			futures = true
			wantBid, _ := decimal.NewFromString("1.0123456789")
			wantAsk, _ := decimal.NewFromString("1.0123456790")
			wantVolume, _ := decimal.NewFromString("151.1234")
			if !ticker.Bid.Equal(wantBid) || !ticker.Ask.Equal(wantAsk) {
				t.Fatalf("Futures precision lost: bid=%s ask=%s", ticker.Bid, ticker.Ask)
			}
			if ticker.Volume24h == nil || !ticker.Volume24h.Equal(wantVolume) {
				t.Fatalf("Futures base volume incorrect: %#v", ticker.Volume24h)
			}
			wantTime := time.UnixMilli(futuresTime).UTC()
			if ticker.ExchangeTimestamp == nil || !ticker.Timestamp.Equal(wantTime) || !ticker.ExchangeTimestamp.Equal(wantTime) {
				t.Fatalf("futures exchange timestamp incorrect: %#v", ticker)
			}
			if ticker.ReceivedAt.IsZero() {
				t.Fatal("receive timestamp missing")
			}
		default:
			t.Fatalf("unknown market type: %#v", ticker)
		}
	}
	if !spot || !futures {
		t.Fatalf("Spot и Perpetual не должны смешиваться: %#v", tickers)
	}
}

func TestCollectTickersIgnoresOtherExchangeAndNonUSDTMarkets(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = w.Write([]byte("[]"))
	}))
	defer server.Close()

	adapter := New(testClient(t, server.URL, 0), nil)
	markets := []market.Market{
		{Exchange: "binance", Symbol: "BTCUSDC", BaseAsset: "BTC", QuoteAsset: "USDC", MarketType: market.MarketTypeSpot, ContractType: market.ContractTypeSpot, Active: true},
		{Exchange: "bybit", Symbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT", MarketType: market.MarketTypeSpot, ContractType: market.ContractTypeSpot, Active: true},
	}
	tickers, stats, err := adapter.CollectTickersWithStats(context.Background(), markets)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 0 || len(tickers) != 0 || stats.Received != 0 {
		t.Fatalf("для неподдерживаемых рынков запросы не нужны: requests=%d tickers=%d stats=%#v", requests, len(tickers), stats)
	}
}

func TestCollectTickersRejectsEmptyJSONInsteadOfArray(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == spotBookTickerEndpoint {
			_, _ = w.Write([]byte("null"))
			return
		}
		_, _ = w.Write([]byte("[]"))
	}))
	defer server.Close()

	adapter := New(testClient(t, server.URL, 0), nil)
	markets := []market.Market{
		{Exchange: "binance", Symbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT", MarketType: market.MarketTypeSpot, ContractType: market.ContractTypeSpot, Active: true},
	}
	_, _, err := adapter.CollectTickersWithStats(context.Background(), markets)
	if err == nil {
		t.Fatal("JSON null не должен приниматься вместо массива тикеров")
	}
}
