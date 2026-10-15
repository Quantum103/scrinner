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

func newTestDB(t *testing.T) *DB {
	t.Helper()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "test.db")

	db, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	t.Cleanup(func() {
		_ = db.Close()
	})

	if err := db.Migrate(ctx, "../../migrations"); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	if err := db.Migrate(ctx, "../../migrations"); err != nil {
		t.Fatalf("повторная Migrate: %v", err)
	}

	return db
}

func testMarket() market.Market {
	return market.Market{
		Exchange:     "mexc",
		Symbol:       "PEPEUSDT",
		BaseAsset:    "PEPE",
		QuoteAsset:   "USDT",
		MarketType:   market.MarketTypeSpot,
		ContractType: market.ContractTypeSpot,
		Active:       true,
	}
}

func TestMarketRepository(t *testing.T) {
	db := newTestDB(t)
	repository := NewMarketRepository(db)
	ctx := context.Background()

	id, err := repository.CreateMarket(ctx, testMarket())
	if err != nil {
		t.Fatal(err)
	}

	if id <= 0 {
		t.Fatalf("id=%d", id)
	}

	_, err = repository.CreateMarket(ctx, testMarket())
	if err == nil {
		t.Fatal("ожидалась ошибка дубликата")
	}

	id2, err := repository.UpsertMarket(ctx, testMarket())
	if err != nil || id2 != id {
		t.Fatalf(
			"upsert id=%d err=%v",
			id2,
			err,
		)
	}

	futuresMarket := testMarket()
	futuresMarket.MarketType = market.MarketTypeFutures
	futuresMarket.ContractType = market.ContractTypePerpetual
	futuresMarket.Active = false

	if _, err := repository.UpsertMarket(
		ctx,
		futuresMarket,
	); err != nil {
		t.Fatal(err)
	}

	active, err := repository.ListActiveMarkets(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if len(active) != 1 {
		t.Fatalf(
			"active=%d",
			len(active),
		)
	}

	got, err := repository.GetMarketByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}

	if got.Symbol != "PEPEUSDT" {
		t.Fatalf(
			"symbol=%s",
			got.Symbol,
		)
	}
}

func TestPriceRepositoryDecimalAndNullVolume(t *testing.T) {
	db := newTestDB(t)
	marketRepository := NewMarketRepository(db)
	priceRepository := NewPriceRepository(db)
	ctx := context.Background()

	marketID, err := marketRepository.CreateMarket(
		ctx,
		testMarket(),
	)
	if err != nil {
		t.Fatal(err)
	}

	timestamp := time.Date(
		2026,
		10,
		7,
		12,
		0,
		0,
		123456789,
		time.UTC,
	)

	bid, _ := decimal.NewFromString(
		"0.000000123456789",
	)
	ask, _ := decimal.NewFromString(
		"0.000000123456799",
	)

	if _, err := priceRepository.InsertPriceSnapshot(
		ctx,
		PriceSnapshot{
			MarketID:  marketID,
			Timestamp: timestamp,
			Bid:       bid,
			Ask:       ask,
		},
	); err != nil {
		t.Fatal(err)
	}

	got, err := priceRepository.GetLatestPrice(
		ctx,
		marketID,
	)
	if err != nil {
		t.Fatal(err)
	}

	if !got.Bid.Equal(bid) ||
		!got.Ask.Equal(ask) ||
		got.Timestamp.UnixNano() != timestamp.UnixNano() {
		t.Fatalf(
			"precision lost: %#v",
			got,
		)
	}

	if got.Volume24h != nil {
		t.Fatal("volume должен быть NULL")
	}

	preciseBid, _ := decimal.NewFromString(
		"1.0123456789",
	)
	preciseAsk, _ := decimal.NewFromString(
		"1.0123456790",
	)

	if _, err := priceRepository.InsertPriceSnapshot(
		ctx,
		PriceSnapshot{
			MarketID:  marketID,
			Timestamp: timestamp.Add(2 * time.Second),
			Bid:       preciseBid,
			Ask:       preciseAsk,
		},
	); err != nil {
		t.Fatal(err)
	}

	precise, err := priceRepository.GetLatestPrice(
		ctx,
		marketID,
	)
	if err != nil {
		t.Fatal(err)
	}

	if !precise.Bid.Equal(preciseBid) ||
		!precise.Ask.Equal(preciseAsk) {
		t.Fatal(
			"точность decimal 1.0123456789 потеряна",
		)
	}

	volume, _ := decimal.NewFromString(
		"15342.7821",
	)

	timestamp2 := timestamp.Add(time.Second)

	if err := priceRepository.InsertPriceSnapshots(
		ctx,
		[]PriceSnapshot{
			{
				MarketID:  marketID,
				Timestamp: timestamp2,
				Bid:       bid,
				Ask:       ask,
				Volume24h: &volume,
			},
		},
	); err != nil {
		t.Fatal(err)
	}

	history, err := priceRepository.GetPriceHistory(
		ctx,
		marketID,
		timestamp.Add(-time.Second),
		timestamp2.Add(time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}

	if len(history) != 3 {
		t.Fatalf(
			"history=%d",
			len(history),
		)
	}

	if history[1].Volume24h == nil ||
		!history[1].Volume24h.Equal(volume) {
		t.Fatal("volume precision lost")
	}
}

func TestOpportunityRepository(t *testing.T) {
	db := newTestDB(t)
	marketRepository := NewMarketRepository(db)
	opportunityRepository := NewOpportunityRepository(db)
	ctx := context.Background()

	buyMarket := testMarket()

	buyMarketID, err := marketRepository.CreateMarket(
		ctx,
		buyMarket,
	)
	if err != nil {
		t.Fatal(err)
	}

	sellMarket := testMarket()
	sellMarket.Exchange = "bybit"
	sellMarket.MarketType = market.MarketTypeFutures
	sellMarket.ContractType = market.ContractTypePerpetual

	sellMarketID, err := marketRepository.CreateMarket(
		ctx,
		sellMarket,
	)
	if err != nil {
		t.Fatal(err)
	}

	buyPrice, _ := decimal.NewFromString(
		"0.00001000",
	)
	sellPrice, _ := decimal.NewFromString(
		"0.00001012",
	)
	spread, _ := decimal.NewFromString("1.2")

	now := time.Now().UTC()

	opportunity := arbitrage.Opportunity{
		Timestamp:        now,
		Symbol:           "PEPEUSDT",
		BaseAsset:        "PEPE",
		QuoteAsset:       "USDT",
		BuyMarketID:      buyMarketID,
		BuyExchange:      "mexc",
		BuyMarketType:    buyMarket.MarketType,
		BuyContractType:  buyMarket.ContractType,
		BuyPrice:         buyPrice,
		SellMarketID:     sellMarketID,
		SellExchange:     "bybit",
		SellMarketType:   sellMarket.MarketType,
		SellContractType: sellMarket.ContractType,
		SellPrice:        sellPrice,
		SpreadPercent:    spread,
	}

	if _, err := opportunityRepository.InsertOpportunity(
		ctx,
		opportunity,
	); err != nil {
		t.Fatal(err)
	}

	got, err := opportunityRepository.GetOpportunitiesBySymbol(
		ctx,
		"PEPEUSDT",
		now.Add(-time.Second),
		now.Add(time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 1 ||
		!got[0].BuyPrice.Equal(buyPrice) ||
		!got[0].SellPrice.Equal(sellPrice) ||
		!got[0].SpreadPercent.Equal(spread) {
		t.Fatalf(
			"bad opportunity: %#v",
			got,
		)
	}

	got, err = opportunityRepository.GetOpportunitiesByExchanges(
		ctx,
		"mexc",
		"bybit",
		now.Add(-time.Second),
		now.Add(time.Second),
	)
	if err != nil || len(got) != 1 {
		t.Fatalf(
			"exchange filter err=%v len=%d",
			err,
			len(got),
		)
	}
}

func TestForeignKeysAndRollback(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()

	if _, err := db.SQLDB().ExecContext(
		ctx,
		`INSERT INTO price_snapshots(
			market_id,
			timestamp,
			bid,
			ask
		)
		VALUES (999, ?, ?, ?)`,
		time.Now().UTC().Format(time.RFC3339Nano),
		"1",
		"2",
	); err == nil {
		t.Fatal("foreign key должен быть включён")
	}

	tx, err := db.SQLDB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO markets(
			exchange,
			symbol,
			base_asset,
			quote_asset,
			market_type,
			contract_type,
			active,
			created_at,
			updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"x",
		"YUSDT",
		"Y",
		"USDT",
		"spot",
		"spot",
		1,
		"2026-01-01T00:00:00Z",
		"2026-01-01T00:00:00Z",
	); err != nil {
		t.Fatal(err)
	}

	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	var count int

	if err := db.SQLDB().QueryRowContext(
		ctx,
		`SELECT COUNT(*)
		 FROM markets
		 WHERE exchange = 'x'`,
	).Scan(&count); err != nil {
		t.Fatal(err)
	}

	if count != 0 {
		t.Fatal("rollback не сработал")
	}
}

// func TestInvalidData(t *testing.T) {
// 	db := newTestDB(t)
// 	repository := NewPriceRepository(db)

// 	err := repository.InsertPriceSnapshot(
// 		context.Background(),
// 		PriceSnapshot{
// 			MarketID:  1,
// 			Timestamp: time.Now(),
// 			Bid:       decimal.Zero,
// 			Ask:       decimal.NewFromInt(1),
// 		},
// 	)
// 	if err == nil {
// 		t.Fatal(
// 			"ожидалась ошибка для нулевого bid",
// 		)
// 	}
// }
