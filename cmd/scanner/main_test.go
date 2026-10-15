package main

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/example/crypto-arbitrage-scanner/internal/exchange/mexc"
	"github.com/example/crypto-arbitrage-scanner/internal/storage"
	"github.com/shopspring/decimal"
)

func TestRunMEXCPassStoresSpotAndPerpetualSnapshots(t *testing.T) {
	const futuresTimestamp int64 = 1792050000123
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v3/exchangeInfo":
			_, _ = w.Write([]byte(`{"symbols":[{"symbol":"BTCUSDT","status":"1","baseAsset":"BTC","quoteAsset":"USDT","isSpotTradingAllowed":true,"permissions":["SPOT"],"tradeSideType":1}]}`))
		case "/api/v3/ticker/bookTicker":
			_, _ = w.Write([]byte(`[{"symbol":"BTCUSDT","bidPrice":"65000.123456789","askPrice":"65000.223456789","bidQty":"1","askQty":"2"}]`))
		case "/api/v3/ticker/24hr":
			_, _ = w.Write([]byte(`[{"symbol":"BTCUSDT","volume":"12.3456","quoteVolume":"999999999"}]`))
		case "/api/v1/contract/detail/country":
			_, _ = w.Write([]byte(`{"success":true,"code":0,"data":[{"symbol":"BTC_USDT","baseCoin":"BTC","quoteCoin":"USDT","settleCoin":"USDT","futureType":1,"state":0,"type":1,"contractSize":0.0001}]}`))
		case "/api/v1/contract/ticker":
			_, _ = w.Write([]byte(`{"success":true,"code":0,"data":[{"symbol":"BTC_USDT","bid1":64990.123456789,"ask1":64990.223456789,"volume24":1000,"amount24":64990200,"timestamp":1792050000123}]}`))
		default:
			t.Fatalf("неожиданный endpoint %s", r.URL.Path)
		}
	}))
	defer server.Close()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "mexc-test.db")
	db, err := storage.Open(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx, "../../migrations"); err != nil {
		t.Fatal(err)
	}

	client, err := mexc.NewClient(mexc.ClientConfig{
		SpotBaseURL: server.URL,
		FuturesBaseURL: server.URL,
		HTTPClient: &http.Client{Timeout: time.Second},
		MaxRetries: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(testLogWriter{t}, nil))
	adapter := mexc.New(client, logger)

	if err := runMEXCPassWithAdapter(ctx, logger, db, adapter); err != nil {
		t.Fatal(err)
	}

	var marketCount int
	if err := db.SQLDB().QueryRowContext(ctx, `SELECT COUNT(*) FROM markets WHERE exchange = 'mexc'`).Scan(&marketCount); err != nil {
		t.Fatal(err)
	}
	if marketCount != 2 {
		t.Fatalf("ожидалось 2 отдельных рынка Spot/Perpetual, сохранено %d", marketCount)
	}

	var snapshotCount int
	if err := db.SQLDB().QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM price_snapshots AS p
		JOIN markets AS m ON m.id = p.market_id
		WHERE m.exchange = 'mexc'
	`).Scan(&snapshotCount); err != nil {
		t.Fatal(err)
	}
	if snapshotCount != 2 {
		t.Fatalf("ожидалось 2 снимка, сохранено %d", snapshotCount)
	}

	type storedSnapshot struct {
		symbol             string
		marketType         string
		bid                string
		ask                string
		volume             sql.NullString
		receivedAt         sql.NullString
		exchangeTimestamp  sql.NullString
	}
	rows, err := db.SQLDB().QueryContext(ctx, `
		SELECT m.symbol, m.market_type, p.bid, p.ask, p.volume_24h, p.received_at, p.exchange_timestamp
		FROM price_snapshots AS p
		JOIN markets AS m ON m.id = p.market_id
		WHERE m.exchange = 'mexc'
		ORDER BY m.market_type
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	seenSpot, seenFutures := false, false
	for rows.Next() {
		var got storedSnapshot
		if err := rows.Scan(&got.symbol, &got.marketType, &got.bid, &got.ask, &got.volume, &got.receivedAt, &got.exchangeTimestamp); err != nil {
			t.Fatal(err)
		}
		if !got.receivedAt.Valid {
			t.Fatalf("received_at не сохранён: %#v", got)
		}
		switch got.marketType {
		case "spot":
			seenSpot = true
			wantBid, _ := decimal.NewFromString("65000.123456789")
			bid, err := decimal.NewFromString(got.bid)
			if err != nil || !bid.Equal(wantBid) {
				t.Fatalf("Spot bid потерял точность: %q err=%v", got.bid, err)
			}
			if got.symbol != "BTCUSDT" || got.exchangeTimestamp.Valid {
				t.Fatalf("неверный Spot market/timestamp: %#v", got)
			}
			wantVolume, _ := decimal.NewFromString("12.3456")
			volume, err := decimal.NewFromString(got.volume.String)
			if err != nil || !got.volume.Valid || !volume.Equal(wantVolume) {
				t.Fatalf("неверный Spot base volume: %#v err=%v", got.volume, err)
			}
		case "futures":
			seenFutures = true
			if got.symbol != "BTC_USDT" || !got.exchangeTimestamp.Valid {
				t.Fatalf("неверный Futures market/timestamp: %#v", got)
			}
			timestamp, err := time.Parse(time.RFC3339Nano, got.exchangeTimestamp.String)
			if err != nil || timestamp.UnixMilli() != futuresTimestamp {
				t.Fatalf("неверный exchange timestamp: %q err=%v", got.exchangeTimestamp.String, err)
			}
			wantVolume, _ := decimal.NewFromString("0.1")
			volume, err := decimal.NewFromString(got.volume.String)
			if err != nil || !got.volume.Valid || !volume.Equal(wantVolume) {
				t.Fatalf("неверный Futures base volume: %#v err=%v", got.volume, err)
			}
		default:
			t.Fatalf("неожиданный тип рынка %q", got.marketType)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !seenSpot || !seenFutures {
		t.Fatal("Spot и Perpetual должны быть сохранены отдельно")
	}
}

func TestRunMEXCPassDisabledDoesNotCreateClientOrUseDatabase(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(testLogWriter{t}, nil))
	if err := runMEXCPassIfEnabled(context.Background(), false, logger, nil); err != nil {
		t.Fatalf("выключенный MEXC должен пропускаться без ошибки: %v", err)
	}
}

type testLogWriter struct {
	t *testing.T
}

func (w testLogWriter) Write(data []byte) (int, error) {
	w.t.Log(strings.TrimSpace(string(data)))
	return len(data), nil
}
