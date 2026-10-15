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

	"github.com/example/crypto-arbitrage-scanner/internal/exchange/gate"
	"github.com/example/crypto-arbitrage-scanner/internal/storage"
	"github.com/shopspring/decimal"
)

func TestRunGatePassStoresSpotAndPerpetualInExistingSQLite(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v4/spot/currency_pairs":
			_, _ = io.WriteString(w, "[{\"id\":\"BTC_USDT\",\"base\":\"BTC\",\"quote\":\"USDT\",\"trade_status\":\"tradable\",\"type\":\"normal\",\"delisting_time\":0}]")
		case "/api/v4/spot/tickers":
			_, _ = io.WriteString(w, "[{\"currency_pair\":\"BTC_USDT\",\"highest_bid\":\"65000.123456789123\",\"lowest_ask\":\"65000.223456789123\",\"base_volume\":\"1.23456789\",\"quote_volume\":\"999999\"}]")
		case "/api/v4/futures/usdt/contracts":
			_, _ = io.WriteString(w, "[{\"name\":\"BTC_USDT\",\"type\":\"direct\",\"quanto_multiplier\":\"0.0001\",\"status\":\"trading\",\"in_delisting\":false}]")
		case "/api/v4/futures/usdt/tickers":
			_, _ = io.WriteString(w, "[{\"contract\":\"BTC_USDT\",\"last\":\"1\",\"mark_price\":\"1.5\",\"index_price\":\"1.6\",\"highest_bid\":\"64990.123456789123\",\"lowest_ask\":\"64990.223456789123\",\"volume_24h_base\":\"0.123456789\",\"volume_24h_quote\":\"999999\",\"volume_24h_settle\":\"888888\"}]")
		default:
			t.Fatalf("неожиданный endpoint %s", r.URL.Path)
		}
	}))
	defer server.Close()

	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "gate-test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	client, err := gate.NewClient(gate.ClientConfig{
		BaseURL: server.URL, HTTPClient: &http.Client{Timeout: time.Second},
		MaxRetries: 0, RetryBaseDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := runGatePassWithAdapter(ctx, logger, db, gate.New(client, logger)); err != nil {
		t.Fatal(err)
	}

	var marketCount int
	if err := db.SQLDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM markets WHERE exchange='gate'").Scan(&marketCount); err != nil {
		t.Fatal(err)
	}
	if marketCount != 2 {
		t.Fatalf("ожидалось 2 рынка Spot/Perpetual, сохранено %d", marketCount)
	}
	var snapshotCount int
	if err := db.SQLDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM price_snapshots p JOIN markets m ON m.id=p.market_id WHERE m.exchange='gate'").Scan(&snapshotCount); err != nil {
		t.Fatal(err)
	}
	if snapshotCount != 2 {
		t.Fatalf("ожидалось 2 снимка, сохранено %d", snapshotCount)
	}

	rows, err := db.SQLDB().QueryContext(ctx, "SELECT m.symbol,m.market_type,m.contract_type,p.bid,p.ask,p.volume_24h,p.exchange_timestamp,p.received_at FROM price_snapshots p JOIN markets m ON m.id=p.market_id WHERE m.exchange='gate' ORDER BY m.market_type")
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
		if !receivedAt.Valid {
			t.Fatalf("received_at не сохранён для %s", symbol)
		}
		if exchangeTime.Valid {
			t.Fatalf("Gate не предоставляет timestamp в пакетном ticker API, но сохранён exchange_timestamp=%q", exchangeTime.String)
		}
		switch marketType {
		case "spot":
			seenSpot = true
			if symbol != "BTC_USDT" || contractType != "spot" {
				t.Fatalf("неверно сохранён Spot: %s %s %s", symbol, marketType, contractType)
			}
			bid, err := decimal.NewFromString(bidText)
			want, _ := decimal.NewFromString("65000.123456789123")
			if err != nil || !bid.Equal(want) {
				t.Fatalf("Spot bid потерял точность: %q err=%v", bidText, err)
			}
			gotVolume, err := decimal.NewFromString(volume.String)
			wantVolume, _ := decimal.NewFromString("1.23456789")
			if err != nil || !volume.Valid || !gotVolume.Equal(wantVolume) {
				t.Fatalf("неверный Spot base_volume: %#v err=%v", volume, err)
			}
		case "futures":
			seenPerpetual = true
			if symbol != "BTC_USDT" || contractType != "perpetual" {
				t.Fatalf("неверно сохранён Perpetual: %s %s %s", symbol, marketType, contractType)
			}
			bid, err := decimal.NewFromString(bidText)
			want, _ := decimal.NewFromString("64990.123456789123")
			if err != nil || !bid.Equal(want) {
				t.Fatalf("Futures bid потерял точность: %q err=%v", bidText, err)
			}
			gotVolume, err := decimal.NewFromString(volume.String)
			wantVolume, _ := decimal.NewFromString("0.123456789")
			if err != nil || !volume.Valid || !gotVolume.Equal(wantVolume) {
				t.Fatalf("неверный Futures volume_24h_base: %#v err=%v", volume, err)
			}
		default:
			t.Fatalf("неизвестный market_type %q", marketType)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !seenSpot || !seenPerpetual {
		t.Fatal("оба типа рынка должны быть сохранены отдельно")
	}
}

func TestRunGatePassDisabledDoesNotCreateClientOrAccessDatabase(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := runGatePassIfEnabled(context.Background(), false, "://invalid", logger, nil); err != nil {
		t.Fatalf("выключенный Gate должен пропускаться без ошибки: %v", err)
	}
}
