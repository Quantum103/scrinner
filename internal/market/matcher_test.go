package market

import (
	"testing"
)

func testSpotMarket(exchange, symbol, base string) Market {
	return Market{
		Exchange:     exchange,
		Symbol:       symbol,
		BaseAsset:    base,
		QuoteAsset:   "USDT",
		MarketType:   MarketTypeSpot,
		ContractType: ContractTypeSpot,
		Active:       true,
	}
}

func testPerpetualMarket(exchange, symbol, base string) Market {
	return Market{
		Exchange:     exchange,
		Symbol:       symbol,
		BaseAsset:    base,
		QuoteAsset:   "USDT",
		MarketType:   MarketTypeFutures,
		ContractType: ContractTypePerpetual,
		Active:       true,
	}
}

func TestAreMarketsComparableAllMarketTypeCombinations(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		a    Market
		b    Market
	}{
		{
			name: "spot to spot",
			a:    testSpotMarket("mexc", "PEPE_USDT", "PEPE"),
			b:    testSpotMarket("bybit", "PEPEUSDT", "PEPE"),
		},
		{
			name: "spot to perpetual",
			a:    testSpotMarket("mexc", "PEPE_USDT", "PEPE"),
			b:    testPerpetualMarket("bybit", "PEPEUSDT", "PEPE"),
		},
		{
			name: "perpetual to spot",
			a:    testPerpetualMarket("bybit", "PEPEUSDT", "PEPE"),
			b:    testSpotMarket("mexc", "PEPE_USDT", "PEPE"),
		},
		{
			name: "perpetual to perpetual",
			a:    testPerpetualMarket("bybit", "PEPEUSDT", "PEPE"),
			b:    testPerpetualMarket("mexc", "PEPE_USDT", "PEPE"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !AreMarketsComparable(tt.a, tt.b) {
				t.Fatal("AreMarketsComparable() = false, want true")
			}
		})
	}
}

func TestAreMarketsComparableRejectsDifferentBaseAssets(t *testing.T) {
	t.Parallel()

	a := testSpotMarket("binance", "BTCUSDT", "BTC")
	b := testSpotMarket("bybit", "ETHUSDT", "ETH")

	if AreMarketsComparable(a, b) {
		t.Fatal("different base assets must not be comparable")
	}
}

func TestAreMarketsComparableRejectsDifferentQuoteAssets(t *testing.T) {
	t.Parallel()

	a := testSpotMarket("binance", "BTCUSDT", "BTC")

	b := Market{
		Exchange:     "bybit",
		Symbol:       "BTCUSDC",
		BaseAsset:    "BTC",
		QuoteAsset:   "USDC",
		MarketType:   MarketTypeSpot,
		ContractType: ContractTypeSpot,
		Active:       true,
	}

	if AreMarketsComparable(a, b) {
		t.Fatal("different quote assets must not be comparable")
	}
}

func TestAreMarketsComparableRejectsSameExchange(t *testing.T) {
	t.Parallel()

	a := testSpotMarket("bybit", "BTCUSDT", "BTC")
	b := testPerpetualMarket("BYBIT", "BTCUSDT", "BTC")

	if AreMarketsComparable(a, b) {
		t.Fatal("markets on the same exchange must not be comparable")
	}
}

func TestAreMarketsComparableRejectsInactiveMarket(t *testing.T) {
	t.Parallel()

	a := testSpotMarket("mexc", "BTCUSDT", "BTC")
	b := testSpotMarket("bybit", "BTCUSDT", "BTC")

	b.Active = false

	if AreMarketsComparable(a, b) {
		t.Fatal("inactive market must not be comparable")
	}
}

func TestAreMarketsComparableRejectsUnsupportedContract(t *testing.T) {
	t.Parallel()

	a := testSpotMarket("mexc", "BTCUSDT", "BTC")

	b := Market{
		Exchange:     "bybit",
		Symbol:       "BTCUSDT",
		BaseAsset:    "BTC",
		QuoteAsset:   "USDT",
		MarketType:   MarketTypeFutures,
		ContractType: ContractTypeDelivery,
		Active:       true,
	}

	if AreMarketsComparable(a, b) {
		t.Fatal("delivery futures must not be comparable")
	}
}

func TestMarketIdentityEqual(t *testing.T) {
	t.Parallel()

	a := testSpotMarket("bybit", "BTCUSDT", "BTC")
	b := testSpotMarket("BYBIT", "BTCUSDT", "BTC")
	c := testPerpetualMarket("bybit", "BTCUSDT", "BTC")

	if !MarketIdentityEqual(a, b) {
		t.Fatal("same market identity must be equal")
	}

	if MarketIdentityEqual(a, c) {
		t.Fatal("spot and perpetual markets must not have equal identity")
	}
}

func TestValidateComparableMarketsReturnsReason(t *testing.T) {
	t.Parallel()

	a := testSpotMarket("mexc", "BTCUSDT", "BTC")
	b := testSpotMarket("bybit", "ETHUSDT", "ETH")

	err := ValidateComparableMarkets(a, b)
	if err == nil {
		t.Fatal("ValidateComparableMarkets() expected error")
	}
}
