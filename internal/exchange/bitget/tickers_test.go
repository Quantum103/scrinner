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
	"github.com/shopspring/decimal"
)

func TestCollectTickersUsesBestPricesBaseVolumeAndMilliseconds(t *testing.T) {
	const timestampMilli int64 = 1792440000123
	spotPayload := `{"code":"00000","msg":"success","data":[
		{"category":"SPOT","symbol":"BTCUSDT","bid1Price":"65000.123456789123456789","ask1Price":"65000.223456789123456789","bid1Size":"0.100","ask1Size":"0.200","lastPrice":"1","volume24h":"123.4567890123456789","turnover24h":"987654321.123","ts":"1792440000123"},
		{"category":"SPOT","symbol":"BADUSDT","bid1Price":"0","ask1Price":"2","volume24h":"1","ts":"1792440000123"},
		{"category":"SPOT","symbol":"UNKNOWNUSDT","bid1Price":"1","ask1Price":"2","ts":"1792440000123"},
		{"category":"SPOT","symbol":"MISSINGTSUSDT","bid1Price":"1","ask1Price":"2"}
	]}`
	futuresPayload := `{"code":"00000","msg":"success","data":[
		{"category":"USDT-FUTURES","symbol":"BTCUSDT","bid1Price":"64990.123456789123456789","ask1Price":"64990.223456789123456789","bid1Size":"2","ask1Size":"3","lastPrice":"1","markPrice":"1.5","indexPrice":"1.6","volume24h":"0.01234567890123456789","turnover24h":"777777.123","ts":1792440000123},
		{"category":"USDT-FUTURES","symbol":"BADFUTUSDT","bid1Price":"1","ask1Price":"-1","ts":"1792440000123"}
	]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != tickersEndpoint {
			t.Errorf("неожиданный запрос: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("category") {
		case spotCategory:
			_, _ = io.WriteString(w, spotPayload)
		case futuresCategory:
			_, _ = io.WriteString(w, futuresPayload)
		default:
			t.Errorf("неверная category: %q", r.URL.Query().Get("category"))
			http.Error(w, "invalid category", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	adapter := New(testClient(t, server.URL, 0), slog.New(slog.NewTextHandler(io.Discard, nil)))
	markets := []market.Market{
		{Exchange: "bitget", Symbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT", MarketType: market.MarketTypeSpot, ContractType: market.ContractTypeSpot, Active: true},
		{Exchange: "bitget", Symbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT", MarketType: market.MarketTypeFutures, ContractType: market.ContractTypePerpetual, Active: true},
		{Exchange: "binance", Symbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT", MarketType: market.MarketTypeSpot, ContractType: market.ContractTypeSpot, Active: true},
	}
	before := time.Now().UTC()
	tickers, stats, err := adapter.CollectTickersWithStats(context.Background(), markets)
	if err != nil {
		t.Fatal(err)
	}
	if len(tickers) != 2 || stats.Received != 6 || stats.Valid != 2 || stats.Skipped != 4 {
		t.Fatalf("неверные результаты/статистика: len=%d stats=%#v", len(tickers), stats)
	}

	wantTimestamp := time.UnixMilli(timestampMilli).UTC()
	seen := map[market.MarketType]bool{}
	for _, got := range tickers {
		if !got.IsValid() || got.Exchange != "bitget" || got.Symbol != "BTCUSDT" {
			t.Fatalf("невалидный тикер: %#v", got)
		}
		if !got.Timestamp.Equal(wantTimestamp) || got.Timestamp.Location() != time.UTC {
			t.Fatalf("ts не преобразован из миллисекунд в UTC: got=%s want=%s", got.Timestamp, wantTimestamp)
		}
		if got.ExchangeTimestamp == nil || !got.ExchangeTimestamp.Equal(wantTimestamp) {
			t.Fatalf("exchange_timestamp должен содержать ts API: %#v", got)
		}
		if got.ReceivedAt.Before(before) || got.ReceivedAt.Location() != time.UTC {
			t.Fatalf("неверное локальное время получения: %#v", got.ReceivedAt)
		}
		seen[got.MarketType] = true
		switch got.MarketType {
		case market.MarketTypeSpot:
			assertDecimal(t, got.Bid.String(), "65000.123456789123456789")
			assertDecimal(t, got.Ask.String(), "65000.223456789123456789")
			if got.Volume24h == nil {
				t.Fatal("Spot volume24h должен быть сохранён")
			}
			assertDecimal(t, got.Volume24h.String(), "123.4567890123456789")
		case market.MarketTypeFutures:
			assertDecimal(t, got.Bid.String(), "64990.123456789123456789")
			assertDecimal(t, got.Ask.String(), "64990.223456789123456789")
			if got.Volume24h == nil {
				t.Fatal("Futures volume24h должен быть сохранён")
			}
			assertDecimal(t, got.Volume24h.String(), "0.01234567890123456789")
		default:
			t.Fatalf("неожиданный тип рынка: %s", got.MarketType)
		}
	}
	if !seen[market.MarketTypeSpot] || !seen[market.MarketTypeFutures] {
		t.Fatalf("Spot и Perpetual должны сохраняться раздельно: %#v", seen)
	}
}

func TestParseTimestampMilliRejectsMissingMalformedAndNonpositiveValues(t *testing.T) {
	for _, raw := range []string{"", "null", `"bad"`, `"0"`, `"-1"`, `1.5`} {
		t.Run(raw, func(t *testing.T) {
			if _, err := parseTimestampMilli([]byte(raw)); err == nil {
				t.Fatalf("ожидалась ошибка для ts=%s", raw)
			}
		})
	}
}

func TestParsePositiveDecimalRejectsNonpositiveAndMalformedValues(t *testing.T) {
	for _, raw := range []string{"", "null", `"0"`, `"-1"`, `"bad"`} {
		t.Run(raw, func(t *testing.T) {
			if _, err := parsePositiveDecimal([]byte(raw)); err == nil {
				t.Fatalf("ожидалась ошибка для цены=%s", raw)
			}
		})
	}
}

func assertDecimal(t *testing.T, gotText, wantText string) {
	t.Helper()
	got, err := decimal.NewFromString(gotText)
	if err != nil {
		t.Fatal(err)
	}
	want, err := decimal.NewFromString(wantText)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(want) {
		t.Fatalf("decimal отличается: got=%s want=%s", got, want)
	}
}
