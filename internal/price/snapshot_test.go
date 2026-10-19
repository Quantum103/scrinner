package price

import (
	"testing"
	"time"

	"github.com/example/crypto-arbitrage-scanner/internal/market"
	"github.com/shopspring/decimal"
)

func TestTickerIsFreshRequiresRecentReceiptAndSourceTimestamp(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	eventTime := now.Add(-time.Second)
	base := Ticker{
		Exchange: "binance", Symbol: "BTCUSDT",
		MarketType: market.MarketTypeSpot, ContractType: market.ContractTypeSpot,
		Bid: decimal.NewFromInt(99), Ask: decimal.NewFromInt(100),
		Timestamp: now.Add(-time.Second), ReceivedAt: now.Add(-time.Second),
		ExchangeTimestamp: &eventTime,
	}
	if !base.IsFresh(now, 10*time.Second) {
		t.Fatal("свежая котировка должна приниматься")
	}

	tests := []struct {
		name   string
		mutate func(*Ticker)
	}{
		{
			name: "receipt timestamp missing",
			mutate: func(t *Ticker) { t.ReceivedAt = time.Time{} },
		},
		{
			name: "receipt timestamp stale",
			mutate: func(t *Ticker) { t.ReceivedAt = now.Add(-11 * time.Second) },
		},
		{
			name: "primary timestamp stale",
			mutate: func(t *Ticker) { t.Timestamp = now.Add(-11 * time.Second) },
		},
		{
			name: "exchange timestamp stale",
			mutate: func(t *Ticker) {
				old := now.Add(-11 * time.Second)
				t.ExchangeTimestamp = &old
			},
		},
		{
			name: "receipt timestamp in future",
			mutate: func(t *Ticker) { t.ReceivedAt = now.Add(time.Second) },
		},
		{
			name: "primary timestamp in future",
			mutate: func(t *Ticker) { t.Timestamp = now.Add(time.Second) },
		},
		{
			name: "exchange timestamp in future",
			mutate: func(t *Ticker) {
				future := now.Add(time.Second)
				t.ExchangeTimestamp = &future
			},
		},
		{
			name: "exchange timestamp pointer to zero",
			mutate: func(t *Ticker) {
				zero := time.Time{}
				t.ExchangeTimestamp = &zero
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := base
			test.mutate(&got)
			if got.IsFresh(now, 10*time.Second) {
				t.Fatal("некорректная или устаревшая временная метка не должна считаться свежей")
			}
		})
	}
}

func TestTickerIsFreshRejectsInvalidAgeLimit(t *testing.T) {
	now := time.Now().UTC()
	ticker := Ticker{
		Exchange: "gate", Symbol: "BTC_USDT",
		MarketType: market.MarketTypeSpot, ContractType: market.ContractTypeSpot,
		Bid: decimal.NewFromInt(99), Ask: decimal.NewFromInt(100),
		Timestamp: now, ReceivedAt: now,
	}
	for _, age := range []time.Duration{0, -time.Second} {
		if ticker.IsFresh(now, age) {
			t.Fatalf("maxAge=%s должен быть отклонён", age)
		}
	}
}
