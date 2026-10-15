package gate

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/example/crypto-arbitrage-scanner/internal/market"
)

func TestDiscoverMarketsFiltersSpotAndUSDTPerpetual(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case spotCurrencyPairsEndpoint:
			_, _ = io.WriteString(w, "["+
				"{\"id\":\"BTC_USDT\",\"base\":\"BTC\",\"quote\":\"USDT\",\"trade_status\":\"tradable\",\"type\":\"normal\",\"delisting_time\":0},"+
				"{\"id\":\"BTC3L_USDT\",\"base\":\"BTC3L\",\"quote\":\"USDT\",\"trade_status\":\"tradable\",\"type\":\"normal\",\"delisting_time\":0},"+
				"{\"id\":\"ETH_USDT\",\"base\":\"ETH\",\"quote\":\"USDT\",\"trade_status\":\"buyable\",\"type\":\"normal\",\"delisting_time\":0},"+
				"{\"id\":\"SOL_USDT\",\"base\":\"SOL\",\"quote\":\"USDT\",\"trade_status\":\"sellable\",\"type\":\"normal\",\"delisting_time\":0},"+
				"{\"id\":\"DOGE_USDT\",\"base\":\"DOGE\",\"quote\":\"USDT\",\"trade_status\":\"untradable\",\"type\":\"normal\",\"delisting_time\":0},"+
				"{\"id\":\"XRP_USDC\",\"base\":\"XRP\",\"quote\":\"USDC\",\"trade_status\":\"tradable\",\"type\":\"normal\",\"delisting_time\":0},"+
				"{\"id\":\"ADA_USDT\",\"base\":\"ADA\",\"quote\":\"USDT\",\"trade_status\":\"tradable\",\"type\":\"premarket\",\"delisting_time\":0},"+
				"{\"id\":\"TRX_USDT\",\"base\":\"TRX\",\"quote\":\"USDT\",\"trade_status\":\"tradable\",\"type\":\"normal\",\"delisting_time\":1790000000},"+
				"{\"id\":\"MALFORMED_USDT\",\"base\":\"OTHER\",\"quote\":\"USDT\",\"trade_status\":\"tradable\",\"type\":\"normal\",\"delisting_time\":0},"+
				"{\"id\":\"MISSING_USDT\",\"base\":\"MISSING\",\"quote\":\"USDT\",\"type\":\"normal\",\"delisting_time\":0}]")
		case futuresContractsEndpoint:
			_, _ = io.WriteString(w, "["+
				"{\"name\":\"BTC_USDT\",\"type\":\"direct\",\"quanto_multiplier\":\"0.0001\",\"status\":\"trading\",\"in_delisting\":false},"+
				"{\"name\":\"ETH_USDT\",\"type\":\"inverse\",\"quanto_multiplier\":\"1\",\"status\":\"trading\",\"in_delisting\":false},"+
				"{\"name\":\"SOL_USDT\",\"type\":\"direct\",\"quanto_multiplier\":\"1\",\"status\":\"delisted\",\"in_delisting\":false},"+
				"{\"name\":\"DOGE_USDT\",\"type\":\"direct\",\"quanto_multiplier\":\"1\",\"status\":\"trading\",\"in_delisting\":true},"+
				"{\"name\":\"XRP_USDT\",\"type\":\"direct\",\"quanto_multiplier\":\"1\",\"status\":\"trading\"},"+
				"{\"name\":\"BAD_USDT\",\"type\":\"direct\",\"quanto_multiplier\":\"0\",\"status\":\"trading\",\"in_delisting\":false},"+
				"{\"name\":\"SOMETHING_BTC\",\"type\":\"direct\",\"quanto_multiplier\":\"1\",\"status\":\"trading\",\"in_delisting\":false},"+
				"{\"name\":\"LTC_USDT\",\"type\":\"direct\",\"quanto_multiplier\":\"0.01\",\"status\":\"trading\",\"in_delisting\":false}]")
		case spotTickersEndpoint:
			_, _ = io.WriteString(w, "["+
				"{\"currency_pair\":\"BTC_USDT\",\"highest_bid\":\"1\",\"lowest_ask\":\"2\"},"+
				"{\"currency_pair\":\"BTC3L_USDT\",\"highest_bid\":\"1\",\"lowest_ask\":\"2\",\"etf_leverage\":\"3\",\"etf_net_value\":\"2\"}]")
		default:
			t.Fatalf("неожиданный endpoint: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	adapter := New(newTestClient(t, server.URL, 0, time.Second), slog.New(slog.NewTextHandler(io.Discard, nil)))
	markets, err := adapter.DiscoverMarkets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(markets) != 3 {
		t.Fatalf("получено %d рынков, ожидалось 3: %#v", len(markets), markets)
	}
	seenSpot, seenBTCPerpetual, seenLTCPerpetual := false, false, false
	for _, m := range markets {
		if !m.Active || m.Exchange != "gate" || m.QuoteAsset != "USDT" {
			t.Fatalf("неожиданный рынок: %#v", m)
		}
		switch {
		case m.IsSpot():
			seenSpot = true
			if m.Symbol != "BTC_USDT" || m.BaseAsset != "BTC" {
				t.Fatalf("неверный Spot-рынок: %#v", m)
			}
		case m.IsPerpetual():
			switch m.Symbol {
			case "BTC_USDT":
				seenBTCPerpetual = true
			case "LTC_USDT":
				seenLTCPerpetual = true
			default:
				t.Fatalf("неожиданный контракт: %#v", m)
			}
		default:
			t.Fatalf("неподдерживаемый рынок: %#v", m)
		}
	}
	if !seenSpot || !seenBTCPerpetual || !seenLTCPerpetual {
		t.Fatalf("Spot/Perpetual потеряны: %#v", markets)
	}
}

func TestConvertSpotRequiresKnownTradableNormalPair(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
		want bool
	}{
		{name: "tradable", body: `{"id":"BTC_USDT","base":"BTC","quote":"USDT","trade_status":"tradable","type":"normal","delisting_time":0}`, want: true},
		{name: "buyable", body: `{"id":"BTC_USDT","base":"BTC","quote":"USDT","trade_status":"buyable","type":"normal","delisting_time":0}`},
		{name: "sellable", body: `{"id":"BTC_USDT","base":"BTC","quote":"USDT","trade_status":"sellable","type":"normal","delisting_time":0}`},
		{name: "untradable", body: `{"id":"BTC_USDT","base":"BTC","quote":"USDT","trade_status":"untradable","type":"normal","delisting_time":0}`},
		{name: "unknown status", body: `{"id":"BTC_USDT","base":"BTC","quote":"USDT","trade_status":"unknown","type":"normal","delisting_time":0}`},
		{name: "premarket", body: `{"id":"BTC_USDT","base":"BTC","quote":"USDT","trade_status":"tradable","type":"premarket","delisting_time":0}`},
		{name: "delisting", body: `{"id":"BTC_USDT","base":"BTC","quote":"USDT","trade_status":"tradable","type":"normal","delisting_time":1790000000}`},
		{name: "missing status", body: `{"id":"BTC_USDT","base":"BTC","quote":"USDT","type":"normal","delisting_time":0}`},
		{name: "mismatch ID", body: `{"id":"ETH_USDT","base":"BTC","quote":"USDT","trade_status":"tradable","type":"normal","delisting_time":0}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var pair spotCurrencyPair
			if err := jsonUnmarshalForTest(tc.body, &pair); err != nil {
				t.Fatal(err)
			}
			_, accepted, err := convertSpotPair(pair)
			if err != nil && tc.want {
				t.Fatal(err)
			}
			if accepted != tc.want {
				t.Fatalf("accepted=%v, ожидалось %v, err=%v", accepted, tc.want, err)
			}
		})
	}
}

func TestConvertFuturesRequiresDirectTradingNonDelistingAndMultiplier(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
		want bool
	}{
		{name: "direct active", body: `{"name":"BTC_USDT","type":"direct","quanto_multiplier":"0.0001","status":"trading","in_delisting":false}`, want: true},
		{name: "inverse", body: `{"name":"BTC_USDT","type":"inverse","quanto_multiplier":"1","status":"trading","in_delisting":false}`},
		{name: "delisted", body: `{"name":"BTC_USDT","type":"direct","quanto_multiplier":"1","status":"delisted","in_delisting":false}`},
		{name: "delisting flag", body: `{"name":"BTC_USDT","type":"direct","quanto_multiplier":"1","status":"trading","in_delisting":true}`},
		{name: "missing delisting flag", body: `{"name":"BTC_USDT","type":"direct","quanto_multiplier":"1","status":"trading"}`},
		{name: "zero multiplier", body: `{"name":"BTC_USDT","type":"direct","quanto_multiplier":"0","status":"trading","in_delisting":false}`},
		{name: "other settle", body: `{"name":"BTC_BTC","type":"direct","quanto_multiplier":"1","status":"trading","in_delisting":false}`},
		{name: "malformed name", body: `{"name":"BTC_USDT_USDT","type":"direct","quanto_multiplier":"1","status":"trading","in_delisting":false}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var contract futuresContract
			if err := jsonUnmarshalForTest(tc.body, &contract); err != nil {
				t.Fatal(err)
			}
			_, accepted, err := convertFuturesContract(contract)
			if err != nil && tc.want {
				t.Fatal(err)
			}
			if accepted != tc.want {
				t.Fatalf("accepted=%v, ожидалось %v, err=%v", accepted, tc.want, err)
			}
		})
	}
}

func TestDiscoverMarketsRejectsEmptyAndMalformedResponses(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		spot    string
		futures string
		want    string
	}{
		{name: "empty spot", spot: "[]", futures: "[]", want: "пустой список"},
		{name: "malformed spot", spot: "[", futures: "[]", want: "разобрать JSON"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == spotCurrencyPairsEndpoint {
					_, _ = io.WriteString(w, tc.spot)
				} else {
					_, _ = io.WriteString(w, tc.futures)
				}
			}))
			defer server.Close()
			adapter := New(newTestClient(t, server.URL, 0, time.Second), slog.New(slog.NewTextHandler(io.Discard, nil)))
			_, err := adapter.DiscoverMarkets(context.Background())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("ожидалась ошибка %q, получено %v", tc.want, err)
			}
		})
	}
}

func TestGateSpotAndPerpetualCanonicalKeysRemainDistinct(t *testing.T) {
	t.Parallel()
	spot := market.Market{
		Exchange: "gate", Symbol: "BTC_USDT", BaseAsset: "BTC", QuoteAsset: "USDT",
		MarketType: market.MarketTypeSpot, ContractType: market.ContractTypeSpot, Active: true,
	}
	perpetual := market.Market{
		Exchange: "gate", Symbol: "BTC_USDT", BaseAsset: "BTC", QuoteAsset: "USDT",
		MarketType: market.MarketTypeFutures, ContractType: market.ContractTypePerpetual, Active: true,
	}
	if internalMarketKey(spot) == internalMarketKey(perpetual) {
		t.Fatal("Spot и Perpetual с одинаковым символом должны иметь разные ключи")
	}
	if _, err := market.NormalizeMarket(spot); err != nil {
		t.Fatal(err)
	}
	if _, err := market.NormalizeMarket(perpetual); err != nil {
		t.Fatal(err)
	}
}

func jsonUnmarshalForTest(input string, target any) error {
	return json.Unmarshal([]byte(input), target)
}
