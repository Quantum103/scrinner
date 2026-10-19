package storage

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/example/crypto-arbitrage-scanner/internal/market"
	"github.com/shopspring/decimal"
)

func TestHistoricalStatisticsMigrationPreservesLegacyRows(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	oldMigrations := filepath.Join(root, "old-migrations")
	if err := os.MkdirAll(oldMigrations, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"001_initial.sql", "002_price_snapshot_timestamps.sql", "003_opportunity_snapshot_refs.sql",
	} {
		content, err := os.ReadFile(filepath.Join("../../migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(oldMigrations, name), content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	db, err := Open(ctx, filepath.Join(root, "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx, oldMigrations); err != nil {
		t.Fatal(err)
	}

	buy := market.Market{
		Exchange: "binance", Symbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT",
		MarketType: market.MarketTypeSpot, ContractType: market.ContractTypeSpot, Active: true,
	}
	sell := market.Market{
		Exchange: "bybit", Symbol: "BTC_USDT", BaseAsset: "BTC", QuoteAsset: "USDT",
		MarketType: market.MarketTypeFutures, ContractType: market.ContractTypePerpetual, Active: true,
	}
	buyID, err := NewMarketRepository(db).UpsertMarket(ctx, buy)
	if err != nil {
		t.Fatal(err)
	}
	sellID, err := NewMarketRepository(db).UpsertMarket(ctx, sell)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC).Format(time.RFC3339Nano)
	_, err = db.SQLDB().ExecContext(ctx, `INSERT INTO opportunities(
		timestamp, symbol, base_asset, quote_asset,
		buy_market_id, buy_exchange, buy_market_type, buy_contract_type, buy_price,
		sell_market_id, sell_exchange, sell_market_type, sell_contract_type, sell_price, spread_percent,
		buy_symbol, sell_symbol, buy_snapshot_id, sell_snapshot_id
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, NULL)`,
		at, "BTC/USDT", "BTC", "USDT",
		buyID, buy.Exchange, buy.MarketType, buy.ContractType, "100",
		sellID, sell.Exchange, sell.MarketType, sell.ContractType, "101", "1",
		buy.Symbol, sell.Symbol)
	if err != nil {
		t.Fatal(err)
	}

	if err := db.Migrate(ctx, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.SQLDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM opportunities").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("историческая строка потеряна после миграции: count=%d", count)
	}
	var cycleID sql.NullString
	if err := db.SQLDB().QueryRowContext(ctx, "SELECT cycle_id FROM opportunities LIMIT 1").Scan(&cycleID); err != nil {
		t.Fatal(err)
	}
	if cycleID.Valid {
		t.Fatalf("нельзя выдумывать cycle_id для старой строки: %#v", cycleID)
	}
	var spread string
	if err := db.SQLDB().QueryRowContext(ctx, "SELECT spread_percent FROM opportunities LIMIT 1").Scan(&spread); err != nil {
		t.Fatal(err)
	}
	if !decimal.RequireFromString(spread).Equal(decimal.NewFromInt(1)) {
		t.Fatalf("спред изменился после миграции: %s", spread)
	}
}
