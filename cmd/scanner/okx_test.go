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

	"github.com/example/crypto-arbitrage-scanner/internal/exchange/okx"
	"github.com/example/crypto-arbitrage-scanner/internal/storage"
	"github.com/shopspring/decimal"
)

func TestRunOKXPassStoresSpotAndSwapInExistingSQLite(t *testing.T) {
	const exchangeTimestamp int64 = 1792050000123
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v5/public/instruments" && r.URL.Query().Get("instType") == "SPOT":
			_, _ = io.WriteString(w, "{\"code\":\"0\",\"msg\":\"\",\"data\":[{\"instType\":\"SPOT\",\"instId\":\"BTC-USDT\",\"baseCcy\":\"BTC\",\"quoteCcy\":\"USDT\",\"state\":\"live\"}]}")
		case r.URL.Path == "/api/v5/public/instruments" && r.URL.Query().Get("instType") == "SWAP":
			_, _ = io.WriteString(w, "{\"code\":\"0\",\"msg\":\"\",\"data\":[{\"instType\":\"SWAP\",\"instId\":\"BTC-USDT-SWAP\",\"settleCcy\":\"USDT\",\"ctType\":\"linear\",\"ctValCcy\":\"BTC\",\"uly\":\"BTC-USDT\",\"state\":\"live\"}]}")
		case r.URL.Path == "/api/v5/market/tickers" && r.URL.Query().Get("instType") == "SPOT":
			_, _ = io.WriteString(w, "{\"code\":\"0\",\"msg\":\"\",\"data\":[{\"instType\":\"SPOT\",\"instId\":\"BTC-USDT\",\"last\":\"1\",\"bidPx\":\"65000.123456789123\",\"askPx\":\"65000.223456789123\",\"ts\":\"1792050000123\",\"vol24h\":\"1.23456789\",\"volCcy24h\":\"999999\"}]}")
		case r.URL.Path == "/api/v5/market/tickers" && r.URL.Query().Get("instType") == "SWAP":
			_, _ = io.WriteString(w, "{\"code\":\"0\",\"msg\":\"\",\"data\":[{\"instType\":\"SWAP\",\"instId\":\"BTC-USDT-SWAP\",\"last\":\"1\",\"bidPx\":\"64990.123456789123\",\"askPx\":\"64990.223456789123\",\"ts\":\"1792050000123\",\"vol24h\":\"100000\",\"volCcy24h\":\"0.123456789\"}]}")
		default:
			t.Fatalf("неожиданный endpoint или instType: %s?%s", r.URL.Path, r.URL.RawQuery)
		}
	}))
	defer server.Close()

	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "okx-test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	client, err := okx.NewClient(okx.ClientConfig{
		BaseURL: server.URL, HTTPClient: &http.Client{Timeout: time.Second},
		MaxRetries: 0, RetryBaseDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := runOKXPassWithAdapter(ctx, logger, db, okx.New(client, logger)); err != nil {
		t.Fatal(err)
	}

	var marketCount int
	if err := db.SQLDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM markets WHERE exchange = 'okx'").Scan(&marketCount); err != nil {
		t.Fatal(err)
	}
	if marketCount != 2 {
		t.Fatalf("ожидалось 2 рынка Spot/SWAP, сохранено %d", marketCount)
	}
	var snapshotCount int
	if err := db.SQLDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM price_snapshots p JOIN markets m ON m.id=p.market_id WHERE m.exchange='okx'").Scan(&snapshotCount); err != nil {
		t.Fatal(err)
	}
	if snapshotCount != 2 {
		t.Fatalf("ожидалось 2 снимка, сохранено %d", snapshotCount)
	}

	rows, err := db.SQLDB().QueryContext(ctx, "SELECT m.symbol, m.market_type, m.contract_type, p.bid, p.ask, p.volume_24h, p.exchange_timestamp, p.received_at FROM price_snapshots p JOIN markets m ON m.id=p.market_id WHERE m.exchange='okx' ORDER BY m.market_type")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	seenSpot, seenSwap := false, false
	for rows.Next() {
		var symbol, marketType, contractType, bidText, askText string
		var volume, exchangeTime, receivedAt sql.NullString
		if err := rows.Scan(&symbol, &marketType, &contractType, &bidText, &askText, &volume, &exchangeTime, &receivedAt); err != nil {
			t.Fatal(err)
		}
		if !receivedAt.Valid {
			t.Fatalf("received_at не сохранён для %s", symbol)
		}
		switch marketType {
		case "spot":
			seenSpot = true
			if symbol != "BTC-USDT" || contractType != "spot" || !exchangeTime.Valid {
				t.Fatalf("неверно сохранён Spot: %s %s %s exchangeTime=%#v", symbol, marketType, contractType, exchangeTime)
			}
			wantBid, _ := decimal.NewFromString("65000.123456789123")
			bid, err := decimal.NewFromString(bidText)
			if err != nil || !bid.Equal(wantBid) {
				t.Fatalf("Spot bid потерял точность: %q err=%v", bidText, err)
			}
			wantVolume, _ := decimal.NewFromString("1.23456789")
			gotVolume, err := decimal.NewFromString(volume.String)
			if err != nil || !volume.Valid || !gotVolume.Equal(wantVolume) {
				t.Fatalf("Spot base volume неверен: %#v err=%v", volume, err)
			}
		case "futures":
			seenSwap = true
			if symbol != "BTC-USDT-SWAP" || contractType != "perpetual" || !exchangeTime.Valid {
				t.Fatalf("неверно сохранён SWAP: %s %s %s exchangeTime=%#v", symbol, marketType, contractType, exchangeTime)
			}
			parsed, err := time.Parse(time.RFC3339Nano, exchangeTime.String)
			if err != nil || parsed.UnixMilli() != exchangeTimestamp {
				t.Fatalf("неверная биржевая отметка времени: %q err=%v", exchangeTime.String, err)
			}
			wantVolume, _ := decimal.NewFromString("0.123456789")
			gotVolume, err := decimal.NewFromString(volume.String)
			if err != nil || !volume.Valid || !gotVolume.Equal(wantVolume) {
				t.Fatalf("SWAP должен сохранять объём в базовом активе: %#v err=%v", volume, err)
			}
		default:
			t.Fatalf("неожиданный тип рынка %q", marketType)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !seenSpot || !seenSwap {
		t.Fatal("оба рынка должны быть сохранены отдельно")
	}
}

func TestRunOKXPassDisabledDoesNotCreateClientOrAccessDatabase(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := runOKXPassIfEnabled(context.Background(), false, "://invalid", logger, nil); err != nil {
		t.Fatalf("выключенный OKX должен пропускаться без ошибки: %v", err)
	}
}
