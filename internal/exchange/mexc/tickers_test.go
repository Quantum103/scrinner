package mexc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/example/crypto-arbitrage-scanner/internal/market"
	"github.com/shopspring/decimal"
)

func TestCollectTickersPreservesSpotAndFuturesPrecision(t *testing.T) {
	const futuresTimestamp int64 = 1792050000123
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case spotBookTickerEndpoint:
			_, _ = w.Write([]byte(`[
				{"symbol":"BTCUSDT","bidPrice":"0.0000001234567890123","askPrice":"0.0000001234567890456","bidQty":"1","askQty":"2"},
				{"symbol":"ETHUSDT","bidPrice":"bad","askPrice":"2000"},
				{"symbol":"NOTREQUESTED","bidPrice":"1","askPrice":"2"}
			]`))
		case spot24hrTickerEndpoint:
			_, _ = w.Write([]byte(`[
				{"symbol":"BTCUSDT","volume":"15342.7821","quoteVolume":"999999999"},
				{"symbol":"ETHUSDT","volume":"bad"}
			]`))
		case futuresTickerEndpoint:
			_, _ = w.Write([]byte(`{"success":true,"code":0,"data":[
				{"symbol":"BTC_USDT","bid1":1.01234567890123456789,"ask1":1.01234567890123456790,"volume24":1000,"amount24":999999.99,"timestamp":1792050000123},
				{"symbol":"ETH_USDT","bid1":10,"ask1":9,"volume24":100,"amount24":1000,"timestamp":1792050000123}
			]}`))
		default:
			t.Fatalf("неожиданный endpoint: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	adapter := New(testClient(t, server.URL, 0), nil)
	contractSize, _ := decimal.NewFromString("0.0001")
	adapter.contractSizes["BTC_USDT"] = contractSize
	markets := []market.Market{
		{Exchange: "mexc", Symbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT", MarketType: market.MarketTypeSpot, ContractType: market.ContractTypeSpot, Active: true},
		{Exchange: "mexc", Symbol: "BTC_USDT", BaseAsset: "BTC", QuoteAsset: "USDT", MarketType: market.MarketTypeFutures, ContractType: market.ContractTypePerpetual, Active: true},
	}
	tickers, stats, err := adapter.CollectTickersWithStats(context.Background(), markets)
	if err != nil {
		t.Fatal(err)
	}
	if len(tickers) != 2 || stats.Received != 5 || stats.Valid != 2 || stats.Skipped != 3 {
		t.Fatalf("tickers=%d stats=%#v", len(tickers), stats)
	}

	foundSpot, foundFutures := false, false
	for _, ticker := range tickers {
		if ticker.Exchange != "mexc" || !ticker.IsValid() {
			t.Fatalf("некорректный тикер: %#v", ticker)
		}
		switch {
		case ticker.Symbol == "BTCUSDT" && ticker.MarketType == market.MarketTypeSpot:
			foundSpot = true
			wantBid, _ := decimal.NewFromString("0.0000001234567890123")
			wantAsk, _ := decimal.NewFromString("0.0000001234567890456")
			wantVolume, _ := decimal.NewFromString("15342.7821")
			if !ticker.Bid.Equal(wantBid) || !ticker.Ask.Equal(wantAsk) {
				t.Fatalf("потеряна Spot-точность: bid=%s ask=%s", ticker.Bid, ticker.Ask)
			}
			if ticker.Volume24h == nil || !ticker.Volume24h.Equal(wantVolume) {
				t.Fatalf("Spot volume должен быть объёмом базового актива: %#v", ticker.Volume24h)
			}
			if ticker.ExchangeTimestamp != nil || ticker.Timestamp.IsZero() ||
				!ticker.Timestamp.Equal(ticker.ReceivedAt) {
				t.Fatalf("Spot не должен выдумывать timestamp биржи: %#v", ticker)
			}
		case ticker.Symbol == "BTC_USDT" && ticker.MarketType == market.MarketTypeFutures:
			foundFutures = true
			wantBid, _ := decimal.NewFromString("1.01234567890123456789")
			wantAsk, _ := decimal.NewFromString("1.01234567890123456790")
			wantVolume, _ := decimal.NewFromString("0.1")
			wantTimestamp := time.UnixMilli(futuresTimestamp).UTC()
			if !ticker.Bid.Equal(wantBid) || !ticker.Ask.Equal(wantAsk) {
				t.Fatalf("потеряна Futures-точность: bid=%s ask=%s", ticker.Bid, ticker.Ask)
			}
			if ticker.Volume24h == nil || !ticker.Volume24h.Equal(wantVolume) {
				t.Fatalf("объём Futures должен переводиться из контрактов в базовый актив: %#v", ticker.Volume24h)
			}
			if ticker.ExchangeTimestamp == nil || !ticker.Timestamp.Equal(wantTimestamp) ||
				!ticker.ExchangeTimestamp.Equal(wantTimestamp) || ticker.ReceivedAt.IsZero() {
				t.Fatalf("неверные timestamp Futures: %#v", ticker)
			}
		default:
			t.Fatalf("Spot и Futures должны оставаться раздельными: %#v", ticker)
		}
	}
	if !foundSpot || !foundFutures {
		t.Fatalf("не найдены оба ожидаемых рынка: %#v", tickers)
	}
}

func TestCollectTickersDoesNotRequestUnsupportedMarkets(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = w.Write([]byte("[]"))
	}))
	defer server.Close()

	adapter := New(testClient(t, server.URL, 0), nil)
	markets := []market.Market{
		{Exchange: "mexc", Symbol: "BTCUSDC", BaseAsset: "BTC", QuoteAsset: "USDC", MarketType: market.MarketTypeSpot, ContractType: market.ContractTypeSpot, Active: true},
		{Exchange: "binance", Symbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT", MarketType: market.MarketTypeSpot, ContractType: market.ContractTypeSpot, Active: true},
	}
	tickers, stats, err := adapter.CollectTickersWithStats(context.Background(), markets)
	if err != nil {
		t.Fatal(err)
	}
	if requests != 0 || len(tickers) != 0 || stats.Received != 0 {
		t.Fatalf("неподдерживаемые рынки не должны приводить к запросам: requests=%d tickers=%d stats=%#v", requests, len(tickers), stats)
	}
}

func TestCollectTickersRejectsNullBookTicker(t *testing.T) {
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
		{Exchange: "mexc", Symbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT", MarketType: market.MarketTypeSpot, ContractType: market.ContractTypeSpot, Active: true},
	}
	_, _, err := adapter.CollectTickersWithStats(context.Background(), markets)
	if err == nil {
		t.Fatal("JSON null не должен приниматься вместо списка котировок")
	}
}


// TestMissingVolumesRemainUnknown проверяет, что отсутствующий объём не подменяется оборотом.
func TestMissingVolumesRemainUnknown(t *testing.T) {
	now := time.Now().UTC()
	spotMarkets := map[string]market.Market{
		"BTCUSDT": {Exchange: "mexc", Symbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT", MarketType: market.MarketTypeSpot, ContractType: market.ContractTypeSpot, Active: true},
	}
	spotBook := spotTickerList{{
		Symbol: "BTCUSDT",
		BidPrice: json.RawMessage(`"1.00"`),
		AskPrice: json.RawMessage(`"1.01"`),
	}}
	spotTickers, spotStats, err := buildSpotTickers(spotBook, spotMarkets, map[string]*decimal.Decimal{}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(spotTickers) != 1 || spotStats.Valid != 1 || spotTickers[0].Volume24h != nil {
		t.Fatalf("Spot без объёма должен сохранять nil volume: tickers=%#v stats=%#v", spotTickers, spotStats)
	}

	futuresMarkets := map[string]market.Market{
		"BTC_USDT": {Exchange: "mexc", Symbol: "BTC_USDT", BaseAsset: "BTC", QuoteAsset: "USDT", MarketType: market.MarketTypeFutures, ContractType: market.ContractTypePerpetual, Active: true},
	}
	futuresBook := futuresTickerList{{
		Symbol: "BTC_USDT",
		Bid1: json.RawMessage("1.00"),
		Ask1: json.RawMessage("1.01"),
		Volume24: json.RawMessage("100"),
		Amount24: json.RawMessage("999999"),
		Timestamp: json.RawMessage("1792050000123"),
	}}
	futuresTickers, futuresStats, err := buildFuturesTickers(futuresBook, futuresMarkets, map[string]decimal.Decimal{}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(futuresTickers) != 1 || futuresStats.Valid != 1 || futuresTickers[0].Volume24h != nil {
		t.Fatalf("Futures без contractSize не должны подменять Volume24 оборотом Amount24: tickers=%#v stats=%#v", futuresTickers, futuresStats)
	}
}

// TestFuturesTickerSkipsMalformedTimestamp отклоняет строку котировки с некорректной временной меткой.
func TestFuturesTickerSkipsMalformedTimestamp(t *testing.T) {
	now := time.Now().UTC()
	requested := map[string]market.Market{
		"BTC_USDT": {Exchange: "mexc", Symbol: "BTC_USDT", BaseAsset: "BTC", QuoteAsset: "USDT", MarketType: market.MarketTypeFutures, ContractType: market.ContractTypePerpetual, Active: true},
	}
	book := futuresTickerList{{
		Symbol: "BTC_USDT",
		Bid1: json.RawMessage("1"),
		Ask1: json.RawMessage("2"),
		Timestamp: json.RawMessage(`"not-a-timestamp"`),
	}}
	tickers, stats, err := buildFuturesTickers(book, requested, map[string]decimal.Decimal{}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(tickers) != 0 || stats.Valid != 0 || stats.Skipped != 1 {
		t.Fatalf("котировка с некорректным timestamp должна быть пропущена: tickers=%#v stats=%#v", tickers, stats)
	}
}
