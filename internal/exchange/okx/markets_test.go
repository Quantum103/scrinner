package okx

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/example/crypto-arbitrage-scanner/internal/market"
)

func TestDiscoverMarketsFiltersSpotAndLinearUSDTPerpetual(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("instType") == "SPOT" {
			_, _ = io.WriteString(w, "{\"code\":\"0\",\"msg\":\"\",\"data\":["+
				"{\"instType\":\"SPOT\",\"instId\":\"BTC-USDT\",\"baseCcy\":\"BTC\",\"quoteCcy\":\"USDT\",\"state\":\"live\"},"+
				"{\"instType\":\"SPOT\",\"instId\":\"ETH-USDC\",\"baseCcy\":\"ETH\",\"quoteCcy\":\"USDC\",\"state\":\"live\"},"+
				"{\"instType\":\"SPOT\",\"instId\":\"SOL-USDT\",\"baseCcy\":\"SOL\",\"quoteCcy\":\"USDT\",\"state\":\"suspend\"},"+
				"{\"instType\":\"SPOT\",\"instId\":\"DOGE-USDT\",\"baseCcy\":\"DOGE\",\"quoteCcy\":\"USDT\"}]}")
			return
		}
		_, _ = io.WriteString(w, "{\"code\":\"0\",\"msg\":\"\",\"data\":["+
			"{\"instType\":\"SWAP\",\"instId\":\"BTC-USDT-SWAP\",\"settleCcy\":\"USDT\",\"ctType\":\"linear\",\"ctValCcy\":\"BTC\",\"uly\":\"BTC-USDT\",\"instFamily\":\"BTC-USDT\",\"state\":\"live\"},"+
			"{\"instType\":\"SWAP\",\"instId\":\"ETH-USDT-SWAP\",\"settleCcy\":\"USDT\",\"ctType\":\"inverse\",\"ctValCcy\":\"ETH\",\"uly\":\"ETH-USDT\",\"state\":\"live\"},"+
			"{\"instType\":\"SWAP\",\"instId\":\"SOL-USDC-SWAP\",\"settleCcy\":\"USDC\",\"ctType\":\"linear\",\"ctValCcy\":\"SOL\",\"uly\":\"SOL-USDC\",\"state\":\"live\"},"+
			"{\"instType\":\"FUTURES\",\"instId\":\"ADA-USDT-260101\",\"settleCcy\":\"USDT\",\"ctType\":\"linear\",\"ctValCcy\":\"ADA\",\"uly\":\"ADA-USDT\",\"state\":\"live\"},"+
			"{\"instType\":\"OPTION\",\"instId\":\"BTC-USDT-OPTION\",\"settleCcy\":\"USDT\",\"ctType\":\"linear\",\"ctValCcy\":\"BTC\",\"uly\":\"BTC-USDT\",\"state\":\"live\"}]}")
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, 0, time.Second)
	adapter := New(client, slog.New(slog.NewTextHandler(io.Discard, nil)))
	markets, err := adapter.DiscoverMarkets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(markets) != 2 {
		t.Fatalf("получено %d рынков, ожидалось 2: %#v", len(markets), markets)
	}
	seenSpot, seenSwap := false, false
	for _, item := range markets {
		if !item.Active || item.Exchange != "okx" || item.QuoteAsset != "USDT" {
			t.Fatalf("неожиданный рынок: %#v", item)
		}
		switch {
		case item.IsSpot():
			seenSpot = true
			if item.Symbol != "BTC-USDT" || item.BaseAsset != "BTC" {
				t.Fatalf("неверная Spot-модель: %#v", item)
			}
		case item.IsPerpetual():
			seenSwap = true
			if item.Symbol != "BTC-USDT-SWAP" || item.BaseAsset != "BTC" {
				t.Fatalf("неверная SWAP-модель: %#v", item)
			}
		default:
			t.Fatalf("неподдерживаемый тип рынка: %#v", item)
		}
	}
	if !seenSpot || !seenSwap {
		t.Fatal("Spot и Perpetual должны быть представлены раздельно")
	}
}

func TestConvertSwapInstrumentRequiresConsistentMetadata(t *testing.T) {
	t.Parallel()
	valid := swapInstrument{
		InstType: "SWAP", InstID: "BTC-USDT-SWAP", SettleCcy: "USDT",
		CtType: "linear", CtValCcy: "BTC", Uly: "BTC-USDT",
		InstFamily: "BTC-USDT", State: "live",
	}
	if _, err := convertSwapInstrument(valid); err != nil {
		t.Fatalf("валидный линейный контракт отклонён: %v", err)
	}
	tests := []struct {
		name       string
		mutate     func(*swapInstrument)
	}{
		{name: "inverse", mutate: func(v *swapInstrument) { v.CtType = "inverse" }},
		{name: "USDC settlement", mutate: func(v *swapInstrument) { v.SettleCcy = "USDC" }},
		{name: "suspended", mutate: func(v *swapInstrument) { v.State = "suspend" }},
		{name: "delivery ID", mutate: func(v *swapInstrument) { v.InstID = "BTC-USDT-260101" }},
		{name: "contradictory underlying", mutate: func(v *swapInstrument) { v.InstFamily = "ETH-USDT" }},
		{name: "different quote", mutate: func(v *swapInstrument) { v.QuoteCcy = "USDC" }},
		{name: "missing contract type", mutate: func(v *swapInstrument) { v.CtType = "" }},
		{name: "missing base metadata", mutate: func(v *swapInstrument) { v.CtValCcy = ""; v.Uly = ""; v.InstFamily = "" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			instrument := valid
			tc.mutate(&instrument)
			if _, err := convertSwapInstrument(instrument); err == nil {
				t.Fatal("сомнительные метаданные должны быть отклонены")
			}
		})
	}
}

func TestConvertSpotInstrumentRequiresLiveMetadata(t *testing.T) {
	t.Parallel()
	valid := spotInstrument{InstType: "SPOT", InstID: "BTC-USDT", BaseCcy: "BTC", QuoteCcy: "USDT", State: "live"}
	if _, err := convertSpotInstrument(valid); err != nil {
		t.Fatalf("валидный Spot-инструмент отклонён: %v", err)
	}
	tests := []spotInstrument{
		{InstType: "SPOT", InstID: "BTC-USDT", BaseCcy: "BTC", QuoteCcy: "USDT", State: ""},
		{InstType: "SPOT", InstID: "BTC-USDT", BaseCcy: "BTC", QuoteCcy: "USDT", State: "suspend"},
		{InstType: "SPOT", InstID: "BTC-USDC", BaseCcy: "BTC", QuoteCcy: "USDC", State: "live"},
		{InstType: "SPOT", InstID: "ETH-USDT", BaseCcy: "BTC", QuoteCcy: "USDT", State: "live"},
	}
	for _, instrument := range tests {
		if _, err := convertSpotInstrument(instrument); err == nil {
			t.Fatalf("некорректный инструмент должен быть отклонён: %#v", instrument)
		}
	}
}

func TestDiscoverMarketsRejectsEmptyAndMalformedResponses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		spot string
		swap string
		want string
	}{
		{name: "empty spot", spot: "{\"code\":\"0\",\"msg\":\"\",\"data\":[]}", swap: "{\"code\":\"0\",\"msg\":\"\",\"data\":[]}", want: "пустой список"},
		{name: "malformed spot", spot: "{", swap: "{\"code\":\"0\",\"msg\":\"\",\"data\":[]}", want: "разобрать JSON"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Query().Get("instType") == "SPOT" {
					_, _ = io.WriteString(w, tc.spot)
				} else {
					_, _ = io.WriteString(w, tc.swap)
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

func TestCanonicalMarketKeySeparatesOKXSpotAndSwap(t *testing.T) {
	t.Parallel()
	spot := market.Market{
		Exchange: "okx", Symbol: "BTC-USDT", BaseAsset: "BTC", QuoteAsset: "USDT",
		MarketType: market.MarketTypeSpot, ContractType: market.ContractTypeSpot, Active: true,
	}
	swap := market.Market{
		Exchange: "okx", Symbol: "BTC-USDT-SWAP", BaseAsset: "BTC", QuoteAsset: "USDT",
		MarketType: market.MarketTypeFutures, ContractType: market.ContractTypePerpetual, Active: true,
	}
	spotKey, err := market.CanonicalMarketKey(spot)
	if err != nil {
		t.Fatal(err)
	}
	swapKey, err := market.CanonicalMarketKey(swap)
	if err != nil {
		t.Fatal(err)
	}
	if spotKey == swapKey {
		t.Fatalf("Spot и SWAP не должны делить ключ %q", spotKey)
	}
}
