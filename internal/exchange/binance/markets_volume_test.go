package binance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/example/crypto-arbitrage-scanner/internal/market"
)

func TestFetchSpotVolumesChunksAtOneHundredSymbols(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != spot24hrTickerEndpoint {
			t.Fatalf("неожиданный endpoint: %s", r.URL.Path)
		}
		var symbols []string
		if err := json.Unmarshal([]byte(r.URL.Query().Get("symbols")), &symbols); err != nil {
			t.Fatalf("не разобран query symbols: %v", err)
		}
		if len(symbols) == 0 || len(symbols) > maxSpotSymbols {
			t.Fatalf("размер пакета вне лимита: %d", len(symbols))
		}
		calls++
		if err := json.NewEncoder(w).Encode([]spot24hrTicker{}); err != nil {
			t.Fatal(err)
		}
	}))
	defer server.Close()

	adapter := New(testClient(t, server.URL, 0), nil)
	requested := make(map[string]market.Market, 205)
	for i := 0; i < 205; i++ {
		symbol := fmt.Sprintf("TOKEN%03dUSDT", i)
		requested[symbol] = market.Market{
			Exchange: "binance", Symbol: symbol, BaseAsset: fmt.Sprintf("TOKEN%03d", i),
			QuoteAsset: "USDT", MarketType: market.MarketTypeSpot,
			ContractType: market.ContractTypeSpot, Active: true,
		}
	}

	volumes, err := adapter.fetchSpotVolumes(context.Background(), requested)
	if err != nil {
		t.Fatal(err)
	}
	if len(volumes) != 0 {
		t.Fatalf("ожидалось отсутствие объёмов в пустых ответах: %d", len(volumes))
	}
	if calls != 3 {
		t.Fatalf("для 205 инструментов ожидалось 3 запроса, получено %d", calls)
	}
}
