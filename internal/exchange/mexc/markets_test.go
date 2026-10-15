package mexc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/example/crypto-arbitrage-scanner/internal/market"
	"github.com/shopspring/decimal"
)

func TestDiscoverMarketsFiltersSpotAndUSDTPerpetual(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case spotExchangeInfoEndpoint:
			_, _ = w.Write([]byte(`{
				"symbols":[
					{"symbol":"BTCUSDT","status":"1","baseAsset":"BTC","quoteAsset":"USDT","isSpotTradingAllowed":true,"permissions":["SPOT"],"tradeSideType":1},
					{"symbol":"UNIUSDT","status":1,"baseAsset":"UNI","quoteAsset":"USDT","isSpotTradingAllowed":true},
					{"symbol":"BTCUSDC","status":"1","baseAsset":"BTC","quoteAsset":"USDC","isSpotTradingAllowed":true},
					{"symbol":"XRPUSDT","status":"2","baseAsset":"XRP","quoteAsset":"USDT","isSpotTradingAllowed":true},
					{"symbol":"DOGEUSDT","status":"1","baseAsset":"DOGE","quoteAsset":"USDT","isSpotTradingAllowed":false},
					{"symbol":"ETHUSDT","status":"1","baseAsset":"ETH","quoteAsset":"USDT","isSpotTradingAllowed":true,"tradeSideType":3}
				]
			}`))
		case futuresContractInfoEndpoint:
			_, _ = w.Write([]byte(`{
				"success":true,"code":0,
				"data":[
					{"symbol":"BTC_USDT","baseCoin":"BTC","quoteCoin":"USDT","settleCoin":"USDT","futureType":1,"state":0,"type":1,"contractSize":0.0001},
					{"symbol":"ETH_USDT","baseCoin":"ETH","quoteCoin":"USDT","settleCoin":"USDT","futureType":2,"state":1,"type":1,"contractSize":1},
					{"symbol":"SOL_USDC","baseCoin":"SOL","quoteCoin":"USDC","settleCoin":"USDC","futureType":1,"state":0,"type":1,"contractSize":1},
					{"symbol":"ADA_USDT","baseCoin":"ADA","quoteCoin":"USDT","settleCoin":"USDT","futureType":1,"state":3,"type":1,"contractSize":1},
					{"symbol":"XRP_USDT","baseCoin":"XRP","quoteCoin":"USDT","settleCoin":"USDT","futureType":1,"state":4,"type":1,"contractSize":1},
					{"symbol":"DOGE_USDT","baseCoin":"DOGE","quoteCoin":"USDT","settleCoin":"BTC","futureType":1,"state":0,"type":1,"contractSize":1},
					{"symbol":"LTC_USDT","baseCoin":"LTC","quoteCoin":"USDT","settleCoin":"USDT","futureType":1,"state":0,"type":2,"contractSize":1}
				]
			}`))
		default:
			t.Fatalf("неожиданный endpoint: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	adapter := New(testClient(t, server.URL, 0), nil)
	markets, err := adapter.DiscoverMarkets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(markets) != 3 {
		t.Fatalf("ожидалось 3 поддерживаемых рынка, получено %d: %#v", len(markets), markets)
	}

	foundSpotBTC, foundSpotUNI, foundFuturesBTC := false, false, false
	for _, m := range markets {
		if m.Exchange != "mexc" || !m.Active || m.QuoteAsset != "USDT" {
			t.Fatalf("неожиданный рынок: %#v", m)
		}
		switch {
		case m.Symbol == "BTCUSDT" && m.IsSpot():
			foundSpotBTC = true
		case m.Symbol == "UNIUSDT" && m.IsSpot():
			foundSpotUNI = true
		case m.Symbol == "BTC_USDT" && m.IsPerpetual():
			foundFuturesBTC = true
		default:
			t.Fatalf("неверно сопоставлен рынок: %#v", m)
		}
	}
	if !foundSpotBTC || !foundSpotUNI || !foundFuturesBTC {
		t.Fatalf("ожидаемые рынки не найдены: %#v", markets)
	}

	adapter.mu.RLock()
	size := adapter.contractSizes["BTC_USDT"]
	adapter.mu.RUnlock()
	wantSize, _ := decimal.NewFromString("0.0001")
	if !size.Equal(wantSize) {
		t.Fatalf("contractSize потерял точность: %s", size)
	}
}

func TestDiscoverMarketsRejectsMissingRequiredSpotField(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == spotExchangeInfoEndpoint {
			_, _ = w.Write([]byte(`{"symbols":[{"symbol":"BTCUSDT","status":"1","baseAsset":"BTC","quoteAsset":"USDT"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"code":0,"data":[]}`))
	}))
	defer server.Close()

	adapter := New(testClient(t, server.URL, 0), nil)
	_, err := adapter.DiscoverMarkets(context.Background())
	if err == nil || !strings.Contains(err.Error(), "isSpotTradingAllowed") {
		t.Fatalf("ожидалась ошибка отсутствующего поля Spot, получено: %v", err)
	}
}

func TestConvertMarketsUsesStructuredAssets(t *testing.T) {
	allowed := true
	spot, err := convertSpotInstrument(spotSymbol{
		Symbol: "ORIGINALUSDT", Status: []byte(`"1"`), BaseAsset: " original ",
		QuoteAsset: "usdt", IsSpotTradingAllowed: &allowed,
	})
	if err != nil {
		t.Fatal(err)
	}
	if spot.Symbol != "ORIGINALUSDT" || spot.BaseAsset != "ORIGINAL" || spot.QuoteAsset != "USDT" || !spot.IsSpot() {
		t.Fatalf("неожиданный Spot-рынок: %#v", spot)
	}

	futures, err := convertFuturesContract(futuresContract{
		Symbol: "BTC_USDT", BaseCoin: "btc", QuoteCoin: "usdt", SettleCoin: "usdt",
		FutureType: []byte("1"), State: []byte("0"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if futures.Symbol != "BTC_USDT" || futures.BaseAsset != "BTC" || !futures.IsPerpetual() {
		t.Fatalf("неожиданный Futures-рынок: %#v", futures)
	}

	_, err = convertFuturesContract(futuresContract{
		Symbol: "BTC_USDT", BaseCoin: "BTC", QuoteCoin: "USDT", SettleCoin: "USDT",
		FutureType: []byte("2"), State: []byte("1"),
	})
	if err == nil {
		t.Fatal("delivery-контракт не должен конвертироваться как perpetual")
	}
}
