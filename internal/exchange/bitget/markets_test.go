package bitget

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/example/crypto-arbitrage-scanner/internal/market"
)

func newTestClient(t *testing.T, baseURL string, retries int, timeout time.Duration) *Client {
	t.Helper()
	client, err := NewClient(ClientConfig{
		BaseURL: baseURL, HTTPClient: &http.Client{Timeout: timeout},
		MaxRetries: retries, RetryBaseDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestDiscoverMarketsUsesMetadataAndFiltersUnsupportedInstruments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != instrumentsEndpoint {
			t.Errorf("неожиданный запрос: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("category") {
		case spotCategory:
			_, _ = io.WriteString(w, `{"code":"00000","msg":"success","data":[
				{"category":"SPOT","symbol":"BTCUSDT","baseCoin":"BTC","quoteCoin":"USDT","status":"online","symbolType":"crypto","isReality":"no"},
				{"category":"SPOT","symbol":"1INCHUSDT","baseCoin":"1INCH","quoteCoin":"USDT","status":"online","symbolType":"crypto"},
				{"category":"SPOT","symbol":"STOCKUSDT","baseCoin":"STOCK","quoteCoin":"USDT","status":"online","symbolType":"stock"},
				{"category":"SPOT","symbol":"RWAUSDT","baseCoin":"RWA","quoteCoin":"USDT","status":"online","symbolType":"crypto","isRwa":"YES"},
				{"category":"SPOT","symbol":"REALITYUSDT","baseCoin":"REALITY","quoteCoin":"USDT","status":"online","symbolType":"crypto","isReality":"yes"},
				{"category":"SPOT","symbol":"LIMITUSDT","baseCoin":"LIMIT","quoteCoin":"USDT","status":"limit_open","symbolType":"crypto"},
				{"category":"SPOT","symbol":"USDCUSDC","baseCoin":"USDC","quoteCoin":"USDC","status":"online","symbolType":"crypto"},
				{"category":"SPOT","symbol":"UNKNOWNUSDT","baseCoin":"UNKNOWN","quoteCoin":"USDT","status":"online","symbolType":"unknown"}
			]}`)
		case futuresCategory:
			_, _ = io.WriteString(w, `{"code":"00000","msg":"success","data":[
				{"category":"USDT-FUTURES","symbol":"BTCUSDT","baseCoin":"BTC","quoteCoin":"USDT","status":"online","type":"perpetual","symbolType":"crypto","isRwa":"NO"},
				{"category":"USDT-FUTURES","symbol":"ETHUSDT_20261225","baseCoin":"ETH","quoteCoin":"USDT","status":"online","type":"delivery","symbolType":"crypto"},
				{"category":"USDT-FUTURES","symbol":"LIMITFUTUSDT","baseCoin":"LIMITFUT","quoteCoin":"USDT","status":"limit_close","type":"perpetual","symbolType":"crypto"},
				{"category":"USDT-FUTURES","symbol":"RWAUSDT","baseCoin":"RWA","quoteCoin":"USDT","status":"online","type":"perpetual","symbolType":"crypto","isRwa":"YES"},
				{"category":"USDT-FUTURES","symbol":"COINUSDC","baseCoin":"COIN","quoteCoin":"USDC","status":"online","type":"perpetual","symbolType":"crypto"},
				{"category":"USDT-FUTURES","symbol":"WRONGUSDT","baseCoin":"WRONG","quoteCoin":"USDT","status":"online","type":"perpetual","symbolType":"crypto","category":"SPOT"}
			]}`)
		default:
			http.Error(w, "category invalid", http.StatusBadRequest)
		}
	}))
	defer server.Close()
	adapter := New(newTestClient(t, server.URL, 0, time.Second), slog.New(slog.NewTextHandler(io.Discard, nil)))
	markets, err := adapter.DiscoverMarkets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(markets) != 3 {
		t.Fatalf("ожидалось два Spot и один Perpetual, получено %d: %#v", len(markets), markets)
	}
	found := make(map[string]market.Market)
	for _, item := range markets {
		found[string(item.MarketType)+"|"+item.Symbol] = item
		if item.Exchange != "bitget" || !item.Active || item.QuoteAsset != "USDT" {
			t.Fatalf("неверный рынок Bitget: %#v", item)
		}
	}
	if found["spot|1INCHUSDT"].BaseAsset != "1INCH" || found["spot|1INCHUSDT"].Symbol != "1INCHUSDT" {
		t.Fatalf("не сохранены исходный symbol и baseCoin: %#v", found["spot|1INCHUSDT"])
	}
	if found["futures|BTCUSDT"].ContractType != market.ContractTypePerpetual {
		t.Fatalf("неверный тип USDT Perpetual: %#v", found["futures|BTCUSDT"])
	}
}

func TestBitgetCanonicalKeysKeepSpotAndPerpetualDistinctAcrossAdapters(t *testing.T) {
	exchanges := []string{"binance", "bybit", "mexc", "okx", "gate", "bitget"}
	markets := make([]market.Market, 0, len(exchanges)*2)
	for _, name := range exchanges {
		markets = append(markets,
			market.Market{Exchange: name, Symbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT", MarketType: market.MarketTypeSpot, ContractType: market.ContractTypeSpot, Active: true},
			market.Market{Exchange: name, Symbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT", MarketType: market.MarketTypeFutures, ContractType: market.ContractTypePerpetual, Active: true},
		)
	}
	groups, err := market.GroupMarkets(markets)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups["BTC:USDT:SPOT"]) != len(exchanges) || len(groups["BTC:USDT:PERPETUAL"]) != len(exchanges) {
		t.Fatalf("Spot и Perpetual должны группироваться отдельно: %#v", groups)
	}
}

func TestConvertInstrumentRejectsUnknownMetadataAndKeepsOriginalSymbol(t *testing.T) {
	base := instrument{Category: spotCategory, Symbol: "1INCHUSDT", BaseCoin: "1INCH", QuoteCoin: "USDT", Status: "online", SymbolType: "crypto"}
	got, ok, err := convertInstrument(base, spotCategory)
	if err != nil || !ok || got.Symbol != "1INCHUSDT" || got.BaseAsset != "1INCH" {
		t.Fatalf("неверная нормализация: market=%#v ok=%t err=%v", got, ok, err)
	}
	for _, item := range []instrument{
		{Category: spotCategory, Symbol: "BTCUSDT", BaseCoin: "BTC", QuoteCoin: "USDT", Status: "unknown", SymbolType: "crypto"},
		{Category: spotCategory, Symbol: "BTCUSDT", BaseCoin: "BTC", QuoteCoin: "USDT", Status: "online", SymbolType: "unknown-kind"},
		{Category: futuresCategory, Symbol: "BTCUSDT", BaseCoin: "BTC", QuoteCoin: "USDT", Status: "online", InstrumentType: "crypto"},
		{Category: futuresCategory, Symbol: "BTCUSDT", BaseCoin: "BTC", QuoteCoin: "USDT", Status: "online", Type: "perpetual", SymbolType: "crypto", IsRWA: "MAYBE"},
	} {
		if _, _, err := convertInstrument(item, item.Category); err == nil {
			t.Fatalf("ожидалась диагностика неизвестных метаданных: %#v", item)
		}
	}
}
