package market

import (
	"fmt"
	"testing"
)

var benchmarkGroupCount int

func BenchmarkGroupMarkets(b *testing.B) {
	const assetCount = 1000
	exchanges := []string{"binance", "bybit", "mexc", "okx", "gate", "bitget"}
	markets := make([]Market, 0, assetCount*len(exchanges)*2)
	for asset := 0; asset < assetCount; asset++ {
		base := fmt.Sprintf("ASSET%04d", asset)
		for _, exchangeName := range exchanges {
			markets = append(markets,
				Market{Exchange: exchangeName, Symbol: base + "USDT", BaseAsset: base, QuoteAsset: "USDT", MarketType: MarketTypeSpot, ContractType: ContractTypeSpot, Active: true},
				Market{Exchange: exchangeName, Symbol: base + "_USDT", BaseAsset: base, QuoteAsset: "USDT", MarketType: MarketTypeFutures, ContractType: ContractTypePerpetual, Active: true},
			)
		}
	}
	initial, err := GroupMarkets(markets)
	if err != nil {
		b.Fatal(err)
	}
	want := len(initial)
	if want != assetCount*2 {
		b.Fatalf("ожидалось %d групп, получено %d", assetCount*2, want)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		groups, err := GroupMarkets(markets)
		if err != nil {
			b.Fatal(err)
		}
		benchmarkGroupCount = len(groups)
		if benchmarkGroupCount != want {
			b.Fatalf("число групп изменилось: ожидалось %d, получено %d", want, benchmarkGroupCount)
		}
	}
}
