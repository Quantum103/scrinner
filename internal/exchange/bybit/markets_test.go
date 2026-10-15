package bybit

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/example/crypto-arbitrage-scanner/internal/market"
)

func TestDiscoverMarketsFiltersAndPaginates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		category := r.URL.Query().Get("category")
		cursor := r.URL.Query().Get("cursor")

		w.Header().Set("Content-Type", "application/json")
		switch category {
		case spotCategory:
			if r.URL.Query().Get("limit") != "" || r.URL.Query().Get("cursor") != "" {
				t.Fatalf("Spot не должен использовать pagination: %s", r.URL.RawQuery)
			}
			writeJSON(t, w, map[string]any{
				"retCode": 0,
				"retMsg":  "OK",
				"result": map[string]any{
					"category": category,
					"list": []map[string]any{
						{"symbol": "BTCUSDT", "status": "Trading", "baseCoin": "BTC", "quoteCoin": "USDT"},
						{"symbol": "ETHUSDC", "status": "Trading", "baseCoin": "ETH", "quoteCoin": "USDC"},
						{"symbol": "XRPUSDT", "status": "PreLaunch", "baseCoin": "XRP", "quoteCoin": "USDT"},
					},
				},
				"time": 1735809771618,
			})
		case linearCategory:
			if r.URL.Query().Get("limit") != "1000" {
				t.Fatalf("ожидался limit=1000, query=%s", r.URL.RawQuery)
			}
			if cursor == "" {
				writeJSON(t, w, map[string]any{
					"retCode": 0,
					"retMsg":  "OK",
					"result": map[string]any{
						"category":       category,
						"nextPageCursor": "page-2",
						"list": []map[string]any{
							{"symbol": "BTCUSDT", "contractType": "LinearPerpetual", "status": "Trading", "baseCoin": "BTC", "quoteCoin": "USDT", "settleCoin": "USDT"},
							{"symbol": "BTCUSD", "contractType": "InversePerpetual", "status": "Trading", "baseCoin": "BTC", "quoteCoin": "USD", "settleCoin": "BTC"},
						},
					},
					"time": 1735809771618,
				})
			} else if cursor == "page-2" {
				writeJSON(t, w, map[string]any{
					"retCode": 0,
					"retMsg":  "OK",
					"result": map[string]any{
						"category": category,
						"list": []map[string]any{
							{"symbol": "ETHUSDT", "contractType": "LinearPerpetual", "status": "Trading", "baseCoin": "ETH", "quoteCoin": "USDT", "settleCoin": "USDT"},
							{"symbol": "SOLUSDT", "contractType": "LinearPerpetual", "status": "Trading", "baseCoin": "SOL", "quoteCoin": "USDT", "settleCoin": "USDT"},
							{"symbol": "XRPUSDT", "contractType": "LinearPerpetual", "status": "PreLaunch", "baseCoin": "XRP", "quoteCoin": "USDT", "settleCoin": "USDT"},
						},
					},
					"time": 1735809771618,
				})
			} else {
				t.Fatalf("неожиданный cursor=%s", cursor)
			}
		default:
			t.Fatalf("неожиданная category=%s", category)
		}
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	adapter := New(client, nil)

	markets, err := adapter.DiscoverMarkets(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if len(markets) != 4 {
		t.Fatalf("markets=%d, %#v", len(markets), markets)
	}

	seenSpot := false
	seenBTCPerp := false
	seenETHPerp := false
	seenSOLPerp := false
	for _, m := range markets {
		if !m.IsSupported() || m.QuoteAsset != "USDT" || !m.Active {
			t.Fatalf("неподдерживаемый рынок: %#v", m)
		}
		if m.Symbol == "BTCUSDT" && m.IsSpot() {
			seenSpot = true
		}
		if m.Symbol == "BTCUSDT" && m.IsPerpetual() {
			seenBTCPerp = true
		}
		if m.Symbol == "ETHUSDT" && m.IsPerpetual() {
			seenETHPerp = true
		}
		if m.Symbol == "SOLUSDT" && m.IsPerpetual() {
			seenSOLPerp = true
		}
	}
	if !seenSpot || !seenBTCPerp || !seenETHPerp || !seenSOLPerp {
		t.Fatalf("markets не содержат ожидаемые Spot/Perpetual записи: %#v", markets)
	}
}

func TestDiscoverMarketsRejectsMissingInstrumentList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"retCode":0,"retMsg":"OK","result":{"category":"spot"},"time":1735809771618}`))
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	adapter := New(client, nil)

	_, err = adapter.DiscoverMarkets(context.Background())
	if err == nil || !strings.Contains(err.Error(), "result.list") {
		t.Fatalf("ожидалась ошибка отсутствующего списка инструментов, получено: %v", err)
	}
}

func TestDiscoverMarketsRejectsCursorCycle(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		category := r.URL.Query().Get("category")
		cursor := r.URL.Query().Get("cursor")

		if category == spotCategory {
			writeJSON(t, w, map[string]any{
				"retCode": 0,
				"retMsg":  "OK",
				"result": map[string]any{
					"category": category,
					"list":     []map[string]any{},
				},
				"time": 1735809771618,
			})
			return
		}
		if category != linearCategory {
			t.Fatalf("неожиданная category=%s", category)
		}

		nextCursor := "cursor-a"
		switch cursor {
		case "":
			nextCursor = "cursor-a"
		case "cursor-a":
			nextCursor = "cursor-b"
		case "cursor-b":
			nextCursor = "cursor-a"
		default:
			t.Fatalf("неожиданный cursor=%s", cursor)
		}

		writeJSON(t, w, map[string]any{
			"retCode": 0,
			"retMsg":  "OK",
			"result": map[string]any{
				"category":       category,
				"nextPageCursor": nextCursor,
				"list":           []map[string]any{},
			},
			"time": 1735809771618,
		})
	}))
	defer server.Close()

	client, err := NewClient(ClientConfig{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	adapter := New(client, nil)

	_, err = adapter.DiscoverMarkets(context.Background())
	if err == nil || !strings.Contains(err.Error(), "cursor") {
		t.Fatalf("ожидалась ошибка циклического cursor, получено: %v", err)
	}
}

func TestConvertInstrumentUsesStructuredFields(t *testing.T) {
	spot, err := convertSpotInstrument(bybitInstrument{
		Symbol:    "btc-usdt-original",
		Status:    tradingStatus,
		BaseCoin:  "btc",
		QuoteCoin: "usdt",
	})
	if err != nil {
		t.Fatal(err)
	}
	if spot.Symbol != "btc-usdt-original" || spot.BaseAsset != "BTC" || spot.QuoteAsset != "USDT" || !spot.IsSpot() {
		t.Fatalf("unexpected spot=%#v", spot)
	}

	perp, err := convertLinearPerpetualInstrument(bybitInstrument{
		Symbol:       "BTCUSDT",
		ContractType: linearPerpetual,
		Status:       tradingStatus,
		BaseCoin:     "BTC",
		QuoteCoin:    "USDT",
		SettleCoin:   "USDT",
	})
	if err != nil {
		t.Fatal(err)
	}
	if perp.Symbol != "BTCUSDT" || !perp.IsPerpetual() {
		t.Fatalf("unexpected perp=%#v", perp)
	}

	_, err = convertSpotInstrument(bybitInstrument{
		Symbol:    "BTCUSDC",
		Status:    tradingStatus,
		BaseCoin:  "BTC",
		QuoteCoin: "USDC",
	})
	if err == nil {
		t.Fatal("USDC Spot рынок не должен проходить конвертацию")
	}

	_, err = convertLinearPerpetualInstrument(bybitInstrument{
		Symbol:       "BTCUSDT",
		ContractType: "LinearFutures",
		Status:       tradingStatus,
		BaseCoin:     "BTC",
		QuoteCoin:    "USDT",
		SettleCoin:   "USDT",
	})
	if err == nil {
		t.Fatal("срочный Linear Futures не должен проходить конвертацию")
	}

	if market.MarketTypeSpot == market.MarketTypeFutures {
		t.Fatal("market types unexpectedly equal")
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatal(err)
	}
}
