package arbitrage

import (
	"fmt"
	"testing"
	"time"

	"github.com/example/crypto-arbitrage-scanner/internal/market"
	"github.com/example/crypto-arbitrage-scanner/internal/price"
	"github.com/shopspring/decimal"
)

var benchmarkOpportunityCount int

func BenchmarkDetectorDetect(b *testing.B) {
	const assetCount = 100
	exchanges := []string{"binance", "bybit", "mexc", "okx", "gate", "bitget"}
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	quotes := make([]Quote, 0, assetCount*len(exchanges)*2)
	id := int64(1)
	for asset := 0; asset < assetCount; asset++ {
		base := fmt.Sprintf("ASSET%03d", asset)
		for exchangeIndex, exchangeName := range exchanges {
			for _, typ := range []struct {
				marketType market.MarketType
				contract market.ContractType
			}{
				{market.MarketTypeSpot, market.ContractTypeSpot},
				{market.MarketTypeFutures, market.ContractTypePerpetual},
			} {
				tickerTime := now.Add(-time.Second)
				symbol := base + "USDT"
				if typ.marketType == market.MarketTypeFutures { symbol = base + "_USDT" }
				quotes = append(quotes, Quote{
					Market: market.Market{Exchange:exchangeName, Symbol:symbol, BaseAsset:base, QuoteAsset:"USDT", MarketType:typ.marketType, ContractType:typ.contract, Active:true},
					MarketID:id, SnapshotID:id+1_000_000,
					Ticker:price.Ticker{Exchange:exchangeName, Symbol:symbol, MarketType:typ.marketType, ContractType:typ.contract, Bid:decimal.NewFromInt(int64(100+exchangeIndex)), Ask:decimal.NewFromInt(int64(101+exchangeIndex)), Timestamp:tickerTime, ReceivedAt:tickerTime},
				})
				id++
			}
		}
	}
	detector, err := NewDetector(decimal.NewFromInt(1), 10*time.Second)
	if err != nil { b.Fatal(err) }
	want := len(detector.Detect(quotes, now))
	if want == 0 { b.Fatal("синтетический набор должен обнаруживать возможности") }
	b.ReportAllocs()
	b.ResetTimer()
	for i:=0; i<b.N; i++ {
		benchmarkOpportunityCount=len(detector.Detect(quotes, now))
		if benchmarkOpportunityCount != want { b.Fatalf("результат изменился: ожидалось %d, получено %d",want,benchmarkOpportunityCount) }
	}
}
