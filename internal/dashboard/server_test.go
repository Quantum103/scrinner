package dashboard

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/example/crypto-arbitrage-scanner/internal/storage"
)

func newDashboardTestDB(t *testing.T) *storage.DB {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "dashboard.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, filepath.Join("..", "..", "migrations")); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("закрыть SQLite: %v", err)
		}
	})
	return db
}

func TestDashboardAPIUsesLatestCompletedCycle(t *testing.T) {
	db := newDashboardTestDB(t)
	now := time.Now().UTC()

	insertTestCycle(t, db, "older-cycle", now.Add(-10*time.Minute), now.Add(-9*time.Minute), "success")
	insertTestCycle(t, db, "latest-completed-cycle", now.Add(-2*time.Minute), now.Add(-time.Minute), "success")
	insertTestCycle(t, db, "running-cycle", now.Add(-time.Second), time.Time{}, "running")

	buyMarketID := insertTestMarket(t, db, "binance", "TESTUSDT", "TEST", now)
	sellMarketID := insertTestMarket(t, db, "bybit", "TESTUSDT", "TEST", now)
	insertTestOpportunity(t, db, "older-cycle", buyMarketID, sellMarketID, "8.5", now.Add(-9*time.Minute))
	insertTestOpportunity(t, db, "latest-completed-cycle", buyMarketID, sellMarketID, "2.25", now.Add(-time.Minute))

	handler := NewHandler(db.SQLDB())
	request := httptest.NewRequest(http.MethodGet, "/api/dashboard", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload Payload
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("разобрать JSON: %v", err)
	}
	if payload.CurrentCycle == nil || payload.CurrentCycle.ID != "running-cycle" {
		t.Fatalf("текущий цикл не определён корректно: %#v", payload.CurrentCycle)
	}
	if payload.CurrentCycle.FinishedAt != "" || payload.CurrentCycle.Status != "running" {
		t.Fatalf("текущий цикл должен отображаться как выполняющийся: %#v", payload.CurrentCycle)
	}
	if payload.LatestCompletedCycle == nil || payload.LatestCompletedCycle.ID != "latest-completed-cycle" {
		t.Fatalf("последний завершённый цикл не определён корректно: %#v", payload.LatestCompletedCycle)
	}
	if len(payload.Opportunities) != 1 {
		t.Fatalf("ожидался один сигнал из последнего завершённого цикла, получено %d", len(payload.Opportunities))
	}
	if payload.Opportunities[0].SpreadPercent != "2.25" {
		t.Fatalf("панель вернула сигнал из старого цикла: %#v", payload.Opportunities[0])
	}
}

func TestDashboardPageIsServedLocally(t *testing.T) {
	handler := NewHandler(nil)
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	if got := response.Header().Get("Content-Type"); !strings.Contains(got, "text/html") {
		t.Fatalf("Content-Type = %q, хотели HTML", got)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	for _, wanted := range []string{"Возможности арбитража", "Купить · Ask", "Продать · Bid", "/api/dashboard"} {
		if !strings.Contains(string(body), wanted) {
			t.Errorf("страница не содержит %q", wanted)
		}
	}
	if got := response.Header().Get("Content-Security-Policy"); got == "" {
		t.Error("не задан Content-Security-Policy")
	}
}

func TestDashboardAPIRejectsNonGET(t *testing.T) {
	handler := NewHandler(nil)
	request := httptest.NewRequest(http.MethodPost, "/api/dashboard", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, хотели 405", response.Code)
	}
	if got := response.Header().Get("Allow"); got != http.MethodGet {
		t.Fatalf("Allow = %q, хотели GET", got)
	}
}

func insertTestCycle(t *testing.T, db *storage.DB, id string, started, finished time.Time, status string) {
	t.Helper()
	var finishedValue any
	if !finished.IsZero() {
		finishedValue = finished.Format(time.RFC3339Nano)
	}
	_, err := db.SQLDB().ExecContext(context.Background(), `INSERT INTO scan_cycles (
		id, started_at, finished_at, status, enabled_exchanges, succeeded_exchanges,
		failed_exchanges, markets_discovered, tickers_received, tickers_accepted,
		tickers_rejected, snapshots_saved, opportunities_detected, opportunities_saved,
		persistence_errors, min_spread_percent, error
	) VALUES (?, ?, ?, ?, 2, 2, 0, 2, 2, 2, 0, 2, 1, 1, 0, '1', NULL)`,
		id, started.Format(time.RFC3339Nano), finishedValue, status)
	if err != nil {
		t.Fatalf("вставить тестовый цикл: %v", err)
	}
}

func insertTestMarket(t *testing.T, db *storage.DB, exchange, symbol, base string, now time.Time) int64 {
	t.Helper()
	result, err := db.SQLDB().ExecContext(context.Background(), `INSERT INTO markets (
		exchange, symbol, base_asset, quote_asset, market_type, contract_type,
		active, created_at, updated_at
	) VALUES (?, ?, ?, 'USDT', 'spot', 'spot', 1, ?, ?)`,
		exchange, symbol, base, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano))
	if err != nil {
		t.Fatalf("вставить тестовый рынок: %v", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("получить id тестового рынка: %v", err)
	}
	return id
}

func insertTestOpportunity(t *testing.T, db *storage.DB, cycleID string, buyMarketID, sellMarketID int64, spread string, timestamp time.Time) {
	t.Helper()
	_, err := db.SQLDB().ExecContext(context.Background(), `INSERT INTO opportunities (
		timestamp, symbol, base_asset, quote_asset, buy_market_id, buy_exchange,
		buy_market_type, buy_contract_type, buy_price, sell_market_id, sell_exchange,
		sell_market_type, sell_contract_type, sell_price, spread_percent, buy_symbol,
		sell_symbol, cycle_id
	) VALUES (?, 'TEST/USDT', 'TEST', 'USDT', ?, 'binance', 'spot', 'spot', '1.1',
		?, 'bybit', 'spot', 'spot', '1.2', ?, 'TESTUSDT', 'TESTUSDT', ?)`,
		timestamp.Format(time.RFC3339Nano), buyMarketID, sellMarketID, spread, cycleID)
	if err != nil {
		t.Fatalf("вставить тестовую возможность: %v", err)
	}
}
