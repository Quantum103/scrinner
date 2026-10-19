package bitget

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/example/crypto-arbitrage-scanner/internal/market"
)

func TestDiscoverMarketsUsesMetadataAndFiltersUnsupportedInstruments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != instrumentsEndpoint {
			t.Errorf("неожиданный запрос: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("category") {
		case spotCategory:
			_, _ = io.WriteString(w, `{"code":"00000","msg":"success","data":[
				{"category":"SPOT","symbol":"BTCUSDT","baseCoin":"BTC","quoteCoin":"USDT","status":"online","symbolType":"crypto","instrumentType":"crypto","isReality":"no"},
				{"category":"SPOT","symbol":"XRPUSDT","baseCoin":"XRP","quoteCoin":"USDT","status":"online","symbolType":"crypto","instrumentType":""},
				{"category":"SPOT","symbol":"STOCKUSDT","baseCoin":"STOCK","quoteCoin":"USDT","status":"online","symbolType":"stock","instrumentType":"stock"},
				{"category":"SPOT","symbol":"ETFUSDT","baseCoin":"ETF","quoteCoin":"USDT","status":"online","symbolType":"stock","instrumentType":"etf"},
				{"category":"SPOT","symbol":"RWAUSDT","baseCoin":"RWA","quoteCoin":"USDT","status":"online","symbolType":"crypto","instrumentType":"crypto","isRwa":"YES"},
				{"category":"SPOT","symbol":"RTUSDT","baseCoin":"RT","quoteCoin":"USDT","status":"online","symbolType":"crypto","instrumentType":"crypto","isReality":"yes"},
				{"category":"SPOT","symbol":"LTCUSDC","baseCoin":"LTC","quoteCoin":"USDC","status":"online","symbolType":"crypto","instrumentType":"crypto"},
				{"category":"SPOT","symbol":"LIMITUSDT","baseCoin":"LIMIT","quoteCoin":"USDT","status":"limit_open","symbolType":"crypto","instrumentType":"crypto"},
				{"category":"SPOT","symbol":"UNKUSDT","baseCoin":"UNK","quoteCoin":"USDT","status":"online"},
				{"category":"USDT-FUTURES","symbol":"WRONGUSDT","baseCoin":"WRONG","quoteCoin":"USDT","status":"online","symbolType":"crypto","instrumentType":"crypto"}
			]}`)
		case futuresCategory:
			_, _ = io.WriteString(w, `{"code":"00000","msg":"success","data":[
				{"category":"USDT-FUTURES","symbol":"BTCUSDT","baseCoin":"BTC","quoteCoin":"USDT","status":"online","type":"perpetual","symbolType":"crypto","instrumentType":"crypto","isRwa":"NO"},
				{"category":"USDT-FUTURES","symbol":"ETHUSDT_20261225","baseCoin":"ETH","quoteCoin":"USDT","status":"online","type":"delivery","symbolType":"crypto","instrumentType":"crypto"},
				{"category":"USDT-FUTURES","symbol":"LIMITFUTUSDT","baseCoin":"LIMITFUT","quoteCoin":"USDT","status":"limit_close","type":"perpetual","symbolType":"crypto","instrumentType":"crypto"},
				{"category":"USDT-FUTURES","symbol":"RWAUSDT","baseCoin":"RWA","quoteCoin":"USDT","status":"online","type":"perpetual","symbolType":"crypto","instrumentType":"crypto","isRwa":"YES"},
				{"category":"USDT-FUTURES","symbol":"COINUSDC","baseCoin":"COIN","quoteCoin":"USDC","status":"online","type":"perpetual","symbolType":"crypto","instrumentType":"crypto"},
				{"category":"USDT-FUTURES","symbol":"UNKFUTUSDT","baseCoin":"UNKFUT","quoteCoin":"USDT","status":"online","symbolType":"crypto","instrumentType":"crypto","type":"quarterly"},
				{"category":"SPOT","symbol":"WRONGUSDT","baseCoin":"WRONG","quoteCoin":"USDT","status":"online","type":"perpetual","symbolType":"crypto","instrumentType":"crypto"}
			]}`)
		default:
			http.Error(w, "category invalid", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	adapter := New(testClient(t, server.URL, 0), slog.New(slog.NewTextHandler(io.Discard, nil)))
	markets, err := adapter.DiscoverMarkets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(markets) != 3 {
		t.Fatalf("ожидалось два Spot-рынка и один Perpetual, получено %d: %#v", len(markets), markets)
	}
	found := make(map[string]market.Market)
	for _, item := range markets {
		found[string(item.MarketType)+"|"+item.Symbol] = item
		if item.Exchange != "bitget" || !item.Active || item.QuoteAsset != "USDT" {
			t.Fatalf("некорректный рынок Bitget: %#v", item)
		}
	}
	if found["spot|BTCUSDT"].BaseAsset != "BTC" || found["spot|BTCUSDT"].Symbol != "BTCUSDT" {
		t.Fatalf("Spot должен сохранять исходные symbol и baseCoin: %#v", found["spot|BTCUSDT"])
	}
	if found["spot|XRPUSDT"].BaseAsset != "XRP" {
		t.Fatalf("обычный рынок с одним классификатором должен приниматься: %#v", found["spot|XRPUSDT"])
	}
	if found["futures|BTCUSDT"].ContractType != market.ContractTypePerpetual {
		t.Fatalf("USDT-FUTURES должен быть представлен как Perpetual: %#v", found["futures|BTCUSDT"])
	}
}

func TestConvertInstrumentRejectsUnknownMetadataAndKeepsSymbolUnchanged(t *testing.T) {
	base := instrument{
		Category: spotCategory, Symbol: "1INCHUSDT", BaseCoin: "1INCH", QuoteCoin: "USDT",
		Status: "online", SymbolType: "crypto", InstrumentType: "crypto",
	}
	got, ok, err := convertInstrument(base, spotCategory)
	if err != nil || !ok || got.Symbol != "1INCHUSDT" || got.BaseAsset != "1INCH" {
		t.Fatalf("неверная нормализация: market=%#v ok=%t err=%v", got, ok, err)
	}
	for _, item := range []instrument{
		{Category: spotCategory, Symbol: "BTCUSDT", BaseCoin: "BTC", QuoteCoin: "USDT", Status: "unknown", SymbolType: "crypto"},
		{Category: spotCategory, Symbol: "BTCUSDT", BaseCoin: "BTC", QuoteCoin: "USDT", Status: "online", SymbolType: "unknown-kind"},
		{Category: futuresCategory, Symbol: "BTCUSDT", BaseCoin: "BTC", QuoteCoin: "USDT", Status: "online", InstrumentType: "crypto"},
		{Category: futuresCategory, Symbol: "BTCUSDT", BaseCoin: "BTC", QuoteCoin: "USDT", Status: "online", Type: "perpetual", InstrumentType: "crypto", IsRWA: "MAYBE"},
	} {
		if _, _, err := convertInstrument(item, item.Category); err == nil {
			t.Fatalf("неизвестные или недостаточные метаданные должны диагностироваться: %#v", item)
		}
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
	spotGroup := groups["BTC:USDT:SPOT"]
	perpetualGroup := groups["BTC:USDT:PERPETUAL"]
	if len(spotGroup) != len(exchanges) || len(perpetualGroup) != len(exchanges) {
		t.Fatalf("рынки должны сопоставляться по отдельности: Spot=%d Perpetual=%d", len(spotGroup), len(perpetualGroup))
	}
	spotKey, err := market.CanonicalMarketKey(markets[0])
	if err != nil || spotKey != "BTC:USDT:SPOT" {
		t.Fatalf("неверный Spot canonical key: %q err=%v", spotKey, err)
	}
	perpetualKey, err := market.CanonicalMarketKey(markets[1])
	if err != nil || perpetualKey != "BTC:USDT:PERPETUAL" {
		t.Fatalf("неверный Perpetual canonical key: %q err=%v", perpetualKey, err)
	}
}
