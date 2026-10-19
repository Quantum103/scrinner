package main

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/example/crypto-arbitrage-scanner/internal/exchange/bitget"
	"github.com/example/crypto-arbitrage-scanner/internal/storage"
)

func TestRunBitgetPassStoresSpotAndPerpetualInExistingSQLite(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v3/market/instruments":
			switch r.URL.Query().Get("category") {
			case "SPOT":
				_, _ = io.WriteString(w, `{"code":"00000","msg":"success","data":[{"category":"SPOT","symbol":"BTCUSDT","baseCoin":"BTC","quoteCoin":"USDT","status":"online","symbolType":"crypto","instrumentType":"crypto","isReality":"no"}]}`)
			case "USDT-FUTURES":
				_, _ = io.WriteString(w, `{"code":"00000","msg":"success","data":[{"category":"USDT-FUTURES","symbol":"BTCUSDT","baseCoin":"BTC","quoteCoin":"USDT","status":"online","type":"perpetual","symbolType":"crypto","instrumentType":"crypto","isRwa":"NO"}]}`)
			default:
				http.Error(w, "unknown category", http.StatusBadRequest)
			}
		case "/api/v3/market/tickers":
			switch r.URL.Query().Get("category") {
			case "SPOT":
				_, _ = io.WriteString(w, `{"code":"00000","msg":"success","data":[{"category":"SPOT","symbol":"BTCUSDT","bid1Price":"65000.123456789123456789","ask1Price":"65000.223456789123456789","lastPrice":"1","volume24h":"1.23456789","turnover24h":"999999","ts":"1792440000123"}]}`)
			case "USDT-FUTURES":
				_, _ = io.WriteString(w, `{"code":"00000","msg":"success","data":[{"category":"USDT-FUTURES","symbol":"BTCUSDT","bid1Price":"64990.123456789123456789","ask1Price":"64990.223456789123456789","lastPrice":"1","markPrice":"1.5","indexPrice":"1.6","volume24h":"0.123456789","turnover24h":"888888","ts":"1792440001123"}]}`)
			default:
				http.Error(w, "unknown category", http.StatusBadRequest)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "bitget-test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx, "../../migrations"); err != nil {
		t.Fatal(err)
	}

	client, err := bitget.NewClient(bitget.ClientConfig{
		BaseURL: server.URL, HTTPClient: &http.Client{Timeout: time.Second},
		MaxRetries: 0, RetryBaseDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	adapter := bitget.New(client, logger)
	if err := runBitgetPassWithAdapter(ctx, logger, db, adapter); err != nil {
		t.Fatal(err)
	}

	var marketCount int
	if err := db.SQLDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM markets WHERE exchange='bitget'").Scan(&marketCount); err != nil {
		t.Fatal(err)
	}
	if marketCount != 2 {
		t.Fatalf("ожидалось 2 рынка Spot/Perpetual, сохранено %d", marketCount)
	}
	var snapshotCount int
	if err := db.SQLDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM price_snapshots p JOIN markets m ON m.id=p.market_id WHERE m.exchange='bitget'").Scan(&snapshotCount); err != nil {
		t.Fatal(err)
	}
	if snapshotCount != 2 {
		t.Fatalf("ожидалось 2 снимка, сохранено %d", snapshotCount)
	}

	rows, err := db.SQLDB().QueryContext(ctx, "SELECT m.symbol,m.market_type,m.contract_type,p.bid,p.ask,p.volume_24h,p.exchange_timestamp,p.received_at FROM price_snapshots p JOIN markets m ON m.id=p.market_id WHERE m.exchange='bitget' ORDER BY m.market_type")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()

	seenSpot, seenPerpetual := false, false
	for rows.Next() {
		var symbol, marketType, contractType, bidText, askText string
		var volume, exchangeTime, receivedAt sql.NullString
		if err := rows.Scan(&symbol, &marketType, &contractType, &bidText, &askText, &volume, &exchangeTime, &receivedAt); err != nil {
			t.Fatal(err)
		}
		if !receivedAt.Valid || !exchangeTime.Valid {
			t.Fatalf("временные метки должны быть сохранены: received=%#v exchange=%#v", receivedAt, exchangeTime)
		}
		if symbol != "BTCUSDT" {
			t.Fatalf("исходный символ изменился: %q", symbol)
		}
		switch marketType {
		case "spot":
			seenSpot = true
			if contractType != "spot" || bidText != "65000.123456789123456789" || askText != "65000.223456789123456789" || !volume.Valid || volume.String != "1.23456789" {
				t.Fatalf("неверно сохранён Spot: symbol=%s contract=%s bid=%s ask=%s volume=%#v", symbol, contractType, bidText, askText, volume)
			}
		case "futures":
			seenPerpetual = true
			if contractType != "perpetual" || bidText != "64990.123456789123456789" || askText != "64990.223456789123456789" || !volume.Valid || volume.String != "0.123456789" {
				t.Fatalf("неверно сохранён Perpetual: symbol=%s contract=%s bid=%s ask=%s volume=%#v", symbol, contractType, bidText, askText, volume)
			}
		default:
			t.Fatalf("неизвестный тип рынка %q", marketType)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !seenSpot || !seenPerpetual {
		t.Fatal("Spot и Perpetual должны храниться отдельно")
	}
}

func TestRunBitgetPassDisabledDoesNotAccessNetworkOrDatabase(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := runBitgetPassIfEnabled(context.Background(), false, logger, nil); err != nil {
		t.Fatalf("выключенный Bitget должен пропускаться без создания клиента: %v", err)
	}
}
