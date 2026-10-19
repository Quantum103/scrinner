package storage

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/example/crypto-arbitrage-scanner/internal/arbitrage"
	"github.com/example/crypto-arbitrage-scanner/internal/market"
	"github.com/shopspring/decimal"
)

func TestPersistMarketSnapshotsRollsBackMarketsAndSnapshotsOnInvalidInput(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "cycle-rollback.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx, "../../migrations"); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	buy := market.Market{
		Exchange: "binance", Symbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT",
		MarketType: market.MarketTypeSpot, ContractType: market.ContractTypeSpot, Active: true,
	}
	sell := market.Market{
		Exchange: "bybit", Symbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT",
		MarketType: market.MarketTypeFutures, ContractType: market.ContractTypePerpetual, Active: true,
	}
	inputs := []MarketSnapshotInput{
		{Market: buy, Snapshot: PriceSnapshot{
			Timestamp: now, ReceivedAt: now, Bid: decimal.NewFromInt(99), Ask: decimal.NewFromInt(100),
		}},
		{Market: sell, Snapshot: PriceSnapshot{
			Timestamp: now, ReceivedAt: now, Bid: decimal.Zero, Ask: decimal.NewFromInt(102),
		}},
	}
	if _, err := db.PersistMarketSnapshots(ctx, []market.Market{buy, sell}, inputs); err == nil {
		t.Fatal("некорректный снимок должен приводить к ошибке транзакции")
	}

	for _, table := range []string{"markets", "price_snapshots"} {
		var count int
		if err := db.SQLDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("после ошибки транзакции таблица %s содержит %d записей", table, count)
		}
	}
}

func TestPersistMarketSnapshotsReturnsCorrectMarketAndSnapshotIDs(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "cycle-success.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx, "../../migrations"); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	m := market.Market{
		Exchange: "okx", Symbol: "BTC-USDT", BaseAsset: "BTC", QuoteAsset: "USDT",
		MarketType: market.MarketTypeSpot, ContractType: market.ContractTypeSpot, Active: true,
	}
	bid, _ := decimal.NewFromString("99.123456789123456789")
	ask, _ := decimal.NewFromString("100.123456789123456789")
	inputs := []MarketSnapshotInput{{
		Market: m, Snapshot: PriceSnapshot{
			Timestamp: now, ReceivedAt: now, Bid: bid, Ask: ask,
		},
	}}
	saved, err := db.PersistMarketSnapshots(ctx, []market.Market{m}, inputs)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 1 || saved[0].MarketID <= 0 || saved[0].SnapshotID <= 0 ||
		saved[0].Snapshot.MarketID != saved[0].MarketID ||
		saved[0].Snapshot.ID != saved[0].SnapshotID {
		t.Fatalf("неверные ссылки сохранённого снимка: %#v", saved)
	}
	var storedBid string
	if err := db.SQLDB().QueryRowContext(ctx, "SELECT bid FROM price_snapshots WHERE id=?", saved[0].SnapshotID).Scan(&storedBid); err != nil {
		t.Fatal(err)
	}
	if storedBid != bid.String() {
		t.Fatalf("decimal Bid изменился при сохранении: got=%s want=%s", storedBid, bid.String())
	}
}

func TestInsertOpportunitiesRollsBackWholeBatchOnInvalidRecord(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, filepath.Join(t.TempDir(), "opportunity-rollback.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	buyMarket := market.Market{
		Exchange: "binance", Symbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT",
		MarketType: market.MarketTypeSpot, ContractType: market.ContractTypeSpot, Active: true,
	}
	sellMarket := market.Market{
		Exchange: "bybit", Symbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT",
		MarketType: market.MarketTypeFutures, ContractType: market.ContractTypePerpetual, Active: true,
	}
	saved, err := db.PersistMarketSnapshots(ctx, []market.Market{buyMarket, sellMarket}, []MarketSnapshotInput{
		{Market: buyMarket, Snapshot: PriceSnapshot{
			Timestamp: now, ReceivedAt: now, Bid: decimal.NewFromInt(99), Ask: decimal.NewFromInt(100),
		}},
		{Market: sellMarket, Snapshot: PriceSnapshot{
			Timestamp: now, ReceivedAt: now, Bid: decimal.NewFromInt(102), Ask: decimal.NewFromInt(103),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 2 {
		t.Fatalf("ожидалось два сохранённых рынка/снимка, получено %d", len(saved))
	}

	valid := arbitrage.Opportunity{
		Timestamp: now, Symbol: "BTC/USDT", BaseAsset: "BTC", QuoteAsset: "USDT",
		BuyMarketID: saved[0].MarketID, BuySymbol: buyMarket.Symbol,
		BuyExchange: buyMarket.Exchange, BuyMarketType: buyMarket.MarketType,
		BuyContractType: buyMarket.ContractType, BuyPrice: decimal.NewFromInt(100),
		BuySnapshotID: saved[0].SnapshotID,
		SellMarketID:  saved[1].MarketID, SellSymbol: sellMarket.Symbol,
		SellExchange: sellMarket.Exchange, SellMarketType: sellMarket.MarketType,
		SellContractType: sellMarket.ContractType, SellPrice: decimal.NewFromInt(102),
		SellSnapshotID: saved[1].SnapshotID, SpreadPercent: decimal.NewFromInt(2),
	}
	invalid := valid
	invalid.SpreadPercent = decimal.Zero
	if err := NewOpportunityRepository(db).InsertOpportunities(ctx, []arbitrage.Opportunity{valid, invalid}); err == nil {
		t.Fatal("второй некорректный элемент должен приводить к ошибке пакета")
	}
	var count int
	if err := db.SQLDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM opportunities").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("пакет возможностей должен быть отменён целиком, сохранено %d записей", count)
	}
}
