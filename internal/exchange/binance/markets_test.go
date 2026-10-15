package binance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/example/crypto-arbitrage-scanner/internal/market"
)

func TestDiscoverMarketsFiltersSpotAndUSDMPerpetual(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case spotExchangeInfoEndpoint:
			_, _ = w.Write([]byte(`{
				"symbols":[
					{"symbol":"BTCUSDT","status":"TRADING","baseAsset":"BTC","quoteAsset":"USDT","isSpotTradingAllowed":true},
					{"symbol":"BTCUSDC","status":"TRADING","baseAsset":"BTC","quoteAsset":"USDC","isSpotTradingAllowed":true},
					{"symbol":"XRPUSDT","status":"BREAK","baseAsset":"XRP","quoteAsset":"USDT","isSpotTradingAllowed":true},
					{"symbol":"DOGEUSDT","status":"TRADING","baseAsset":"DOGE","quoteAsset":"USDT","isSpotTradingAllowed":false}
				]
			}`))
		case futuresExchangeInfoEndpoint:
			_, _ = w.Write([]byte(`{
				"symbols":[
					{"symbol":"BTCUSDT","pair":"BTCUSDT","contractType":"PERPETUAL","status":"TRADING","baseAsset":"BTC","quoteAsset":"USDT","marginAsset":"USDT","deliveryDate":4133404800000},
					{"symbol":"ETHUSDT","pair":"ETHUSDT","contractType":"CURRENT_QUARTER","status":"TRADING","baseAsset":"ETH","quoteAsset":"USDT","marginAsset":"USDT","deliveryDate":1790000000000},
					{"symbol":"SOLUSDC","pair":"SOLUSDC","contractType":"PERPETUAL","status":"TRADING","baseAsset":"SOL","quoteAsset":"USDC","marginAsset":"USDC","deliveryDate":4133404800000},
					{"symbol":"ADAUSDT","pair":"ADAUSDT","contractType":"PERPETUAL","status":"TRADING","baseAsset":"ADA","quoteAsset":"USDT","marginAsset":"USDC","deliveryDate":4133404800000},
					{"symbol":"XRPUSDT","pair":"XRPUSDT","contractType":"PERPETUAL","status":"PENDING_TRADING","baseAsset":"XRP","quoteAsset":"USDT","marginAsset":"USDT","deliveryDate":4133404800000}
				]
			}`))
		default:
			t.Fatalf("unexpected endpoint %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := testClient(t, server.URL, 0)
	adapter := New(client, nil)
	markets, err := adapter.DiscoverMarkets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(markets) != 2 {
		t.Fatalf("ожидалось 2 подходящих рынка, получено %d: %#v", len(markets), markets)
	}

	foundSpot, foundPerpetual := false, false
	for _, m := range markets {
		if m.Exchange != "binance" || !m.Active || m.QuoteAsset != "USDT" {
			t.Fatalf("неожиданный рынок: %#v", m)
		}
		switch {
		case m.Symbol == "BTCUSDT" && m.IsSpot():
			foundSpot = true
		case m.Symbol == "BTCUSDT" && m.IsPerpetual():
			foundPerpetual = true
		default:
			t.Fatalf("тип рынка или символ ошибочен: %#v", m)
		}
	}
	if !foundSpot || !foundPerpetual {
		t.Fatalf("Spot и Perpetual с одинаковым symbol должны существовать отдельно: %#v", markets)
	}
}

func TestDiscoverMarketsRejectsMissingSymbols(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == spotExchangeInfoEndpoint {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		_, _ = w.Write([]byte(`{"symbols":[]}`))
	}))
	defer server.Close()

	adapter := New(testClient(t, server.URL, 0), nil)
	_, err := adapter.DiscoverMarkets(context.Background())
	if err == nil || !strings.Contains(err.Error(), "не содержит поле symbols") {
		t.Fatalf("ожидалась ошибка отсутствующего списка рынков, получено: %v", err)
	}
}

func TestConvertInstrumentRejectsUnexpectedFields(t *testing.T) {
	allowed := true
	_, err := convertSpotInstrument(spotSymbol{
		Symbol: "BTCUSDC", Status: tradingStatus, BaseAsset: "BTC",
		QuoteAsset: "USDC", IsSpotTradingAllowed: &allowed,
	})
	if err == nil {
		t.Fatal("Spot USDC не должен конвертироваться")
	}

	_, err = convertFuturesInstrument(futuresSymbol{
		Symbol: "BTCUSDT", ContractType: "CURRENT_QUARTER", Status: tradingStatus,
		BaseAsset: "BTC", QuoteAsset: "USDT", MarginAsset: "USDT",
	})
	if err == nil {
		t.Fatal("delivery-контракт не должен конвертироваться")
	}

	if market.MarketTypeSpot == market.MarketTypeFutures {
		t.Fatal("MarketType Spot и Futures неожиданно совпали")
	}
}

func TestDiscoverMarketsRejectsMalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == spotExchangeInfoEndpoint {
			_, _ = w.Write([]byte("{invalid"))
			return
		}
		_ = json.NewEncoder(w).Encode(futuresExchangeInfoResponse{Symbols: []futuresSymbol{}})
	}))
	defer server.Close()

	adapter := New(testClient(t, server.URL, 0), nil)
	_, err := adapter.DiscoverMarkets(context.Background())
	if err == nil || !strings.Contains(err.Error(), "разобрать JSON") {
		t.Fatalf("ожидалась ошибка JSON, получено: %v", err)
	}
}
