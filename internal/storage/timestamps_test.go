package storage

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestPriceRepositoryRoundTripsTimestampSources(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	markets := NewMarketRepository(db)
	prices := NewPriceRepository(db)

	marketID, err := markets.CreateMarket(ctx, testMarket())
	if err != nil {
		t.Fatal(err)
	}

	snapshotTime := time.Date(2026, 10, 15, 9, 0, 0, 123000000, time.UTC)
	receivedAt := snapshotTime.Add(25 * time.Millisecond)
	exchangeAt := snapshotTime.Add(-10 * time.Millisecond)
	bid, _ := decimal.NewFromString("1.0123456789")
	ask, _ := decimal.NewFromString("1.0123456790")
	volume, _ := decimal.NewFromString("15342.7821")

	_, err = prices.InsertPriceSnapshot(ctx, PriceSnapshot{
		MarketID:          marketID,
		Timestamp:         snapshotTime,
		ReceivedAt:        receivedAt,
		ExchangeTimestamp: &exchangeAt,
		Bid:               bid,
		Ask:               ask,
		Volume24h:         &volume,
	})
	if err != nil {
		t.Fatal(err)
	}

	got, err := prices.GetLatestPrice(ctx, marketID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Timestamp.Equal(snapshotTime) || !got.ReceivedAt.Equal(receivedAt) {
		t.Fatalf("времена снимка/получения не совпали: %#v", got)
	}
	if got.ExchangeTimestamp == nil || !got.ExchangeTimestamp.Equal(exchangeAt) {
		t.Fatalf("биржевой timestamp не сохранился: %#v", got.ExchangeTimestamp)
	}
	if !got.Bid.Equal(bid) || !got.Ask.Equal(ask) || got.Volume24h == nil || !got.Volume24h.Equal(volume) {
		t.Fatalf("финансовые значения изменились после сохранения: %#v", got)
	}
}
