package storage

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/example/crypto-arbitrage-scanner/internal/arbitrage"
	"github.com/example/crypto-arbitrage-scanner/internal/market"
	"github.com/shopspring/decimal"
)

const benchmarkHistoryRows = 5000

var benchmarkHistoryCount int64

func BenchmarkStreamHistoricalObservations(b *testing.B) {
	ctx := context.Background()
	path := filepath.Join(b.TempDir(), "history-benchmark.db")
	db, err := Open(ctx, path)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx, "../../migrations"); err != nil {
		b.Fatal(err)
	}

	marketRepo := NewMarketRepository(db)
	buyMarket := testMarket()
	buyID, err := marketRepo.CreateMarket(ctx, buyMarket)
	if err != nil {
		b.Fatal(err)
	}
	sellMarket := testMarket()
	sellMarket.Exchange = "bybit"
	sellID, err := marketRepo.CreateMarket(ctx, sellMarket)
	if err != nil {
		b.Fatal(err)
	}

	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	buyPrice := decimal.NewFromInt(100)
	sellPrice := decimal.NewFromInt(101)
	opportunities := make([]arbitrage.Opportunity, benchmarkHistoryRows)
	for i := range opportunities {
		opportunities[i] = arbitrage.Opportunity{
			Timestamp: start.Add(time.Duration(i) * time.Second),
			Symbol: "PEPE/USDT", BaseAsset: "PEPE", QuoteAsset: "USDT",
			BuyMarketID: buyID, BuySymbol: buyMarket.Symbol, BuyExchange: buyMarket.Exchange,
			BuyMarketType: market.MarketTypeSpot, BuyContractType: market.ContractTypeSpot,
			BuyPrice: buyPrice,
			SellMarketID: sellID, SellSymbol: sellMarket.Symbol, SellExchange: sellMarket.Exchange,
			SellMarketType: market.MarketTypeSpot, SellContractType: market.ContractTypeSpot,
			SellPrice: sellPrice, SpreadPercent: decimal.NewFromInt(1),
		}
	}
	if err := NewOpportunityRepository(db).InsertOpportunities(ctx, opportunities); err != nil {
		b.Fatal(err)
	}

	repo := NewOpportunityRepository(db)
	want := int64(benchmarkHistoryRows)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		seen := int64(0)
		total, skipped, err := repo.StreamHistoricalObservations(ctx, time.Time{}, time.Time{}, func(item HistoricalObservation) error {
			seen++
			return nil
		})
		if err != nil {
			b.Fatal(err)
		}
		if total != want || seen != want || skipped != 0 {
			b.Fatalf("ошибка чтения истории: total=%d seen=%d skipped=%d, ожидалось %d", total, seen, skipped, want)
		}
		benchmarkHistoryCount = seen
	}
}

func TestHistoricalObservationQueryPlan(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	rows, err := db.SQLDB().QueryContext(ctx, `EXPLAIN QUERY PLAN
		SELECT id, cycle_id, timestamp, symbol, base_asset, quote_asset,
			buy_market_id, buy_exchange, COALESCE(buy_symbol, symbol),
			buy_market_type, buy_contract_type, buy_price,
			sell_market_id, sell_exchange, COALESCE(sell_symbol, symbol),
			sell_market_type, sell_contract_type, sell_price, spread_percent
		FROM opportunities
		WHERE timestamp >= ? AND timestamp < ?
		ORDER BY base_asset, quote_asset, buy_exchange,
			COALESCE(buy_symbol, symbol), buy_market_type, buy_contract_type,
			sell_exchange, COALESCE(sell_symbol, symbol), sell_market_type,
			sell_contract_type, julianday(timestamp), id`,
		"2026-01-01T00:00:00Z", "2026-01-08T00:00:00Z")
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN для исторической статистики: %v", err)
	}
	defer rows.Close()

	details := make([]string, 0, 4)
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(details) == 0 {
		t.Fatal("SQLite не вернул план исторического запроса")
	}
	t.Logf("EXPLAIN QUERY PLAN: %s", strings.Join(details, " | "))
}
