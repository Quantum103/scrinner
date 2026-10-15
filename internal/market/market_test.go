package market

import (
	"testing"
)

func TestValidateMarket(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		market  Market
		wantErr bool
	}{
		{
			name: "valid spot",
			market: Market{
				Exchange:     "mexc",
				Symbol:       "PEPE_USDT",
				BaseAsset:    "PEPE",
				QuoteAsset:   "USDT",
				MarketType:   MarketTypeSpot,
				ContractType: ContractTypeSpot,
				Active:       true,
			},
			wantErr: false,
		},
		{
			name: "valid perpetual",
			market: Market{
				Exchange:     "bybit",
				Symbol:       "PEPEUSDT",
				BaseAsset:    "PEPE",
				QuoteAsset:   "USDT",
				MarketType:   MarketTypeFutures,
				ContractType: ContractTypePerpetual,
				Active:       true,
			},
			wantErr: false,
		},
		{
			name: "empty exchange",
			market: Market{
				Exchange:     "",
				Symbol:       "BTCUSDT",
				BaseAsset:    "BTC",
				QuoteAsset:   "USDT",
				MarketType:   MarketTypeSpot,
				ContractType: ContractTypeSpot,
				Active:       true,
			},
			wantErr: true,
		},
		{
			name: "empty symbol",
			market: Market{
				Exchange:     "binance",
				Symbol:       "",
				BaseAsset:    "BTC",
				QuoteAsset:   "USDT",
				MarketType:   MarketTypeSpot,
				ContractType: ContractTypeSpot,
				Active:       true,
			},
			wantErr: true,
		},
		{
			name: "empty base",
			market: Market{
				Exchange:     "binance",
				Symbol:       "USDT",
				BaseAsset:    "",
				QuoteAsset:   "USDT",
				MarketType:   MarketTypeSpot,
				ContractType: ContractTypeSpot,
				Active:       true,
			},
			wantErr: true,
		},
		{
			name: "empty quote",
			market: Market{
				Exchange:     "binance",
				Symbol:       "BTC",
				BaseAsset:    "BTC",
				QuoteAsset:   "",
				MarketType:   MarketTypeSpot,
				ContractType: ContractTypeSpot,
				Active:       true,
			},
			wantErr: true,
		},
		{
			name: "unknown market type",
			market: Market{
				Exchange:     "binance",
				Symbol:       "BTCUSDT",
				BaseAsset:    "BTC",
				QuoteAsset:   "USDT",
				MarketType:   MarketType("unknown"),
				ContractType: ContractTypeSpot,
				Active:       true,
			},
			wantErr: true,
		},
		{
			name: "unknown contract type",
			market: Market{
				Exchange:     "binance",
				Symbol:       "BTCUSDT",
				BaseAsset:    "BTC",
				QuoteAsset:   "USDT",
				MarketType:   MarketTypeSpot,
				ContractType: ContractType("unknown"),
				Active:       true,
			},
			wantErr: true,
		},
		{
			name: "spot with perpetual contract",
			market: Market{
				Exchange:     "binance",
				Symbol:       "BTCUSDT",
				BaseAsset:    "BTC",
				QuoteAsset:   "USDT",
				MarketType:   MarketTypeSpot,
				ContractType: ContractTypePerpetual,
				Active:       true,
			},
			wantErr: true,
		},
		{
			name: "perpetual with spot contract",
			market: Market{
				Exchange:     "binance",
				Symbol:       "BTCUSDT",
				BaseAsset:    "BTC",
				QuoteAsset:   "USDT",
				MarketType:   MarketTypeFutures,
				ContractType: ContractTypeSpot,
				Active:       true,
			},
			wantErr: true,
		},
		{
			name: "delivery futures",
			market: Market{
				Exchange:     "binance",
				Symbol:       "BTCUSDT",
				BaseAsset:    "BTC",
				QuoteAsset:   "USDT",
				MarketType:   MarketTypeFutures,
				ContractType: ContractTypeDelivery,
				Active:       true,
			},
			wantErr: true,
		},
		{
			name: "unsupported quote",
			market: Market{
				Exchange:     "binance",
				Symbol:       "BTCUSDC",
				BaseAsset:    "BTC",
				QuoteAsset:   "USDC",
				MarketType:   MarketTypeSpot,
				ContractType: ContractTypeSpot,
				Active:       true,
			},
			wantErr: true,
		},
		{
			name: "same base and quote",
			market: Market{
				Exchange:     "binance",
				Symbol:       "BTCBTC",
				BaseAsset:    "BTC",
				QuoteAsset:   "BTC",
				MarketType:   MarketTypeSpot,
				ContractType: ContractTypeSpot,
				Active:       true,
			},
			wantErr: true,
		},
		{
			name: "inactive but structurally valid",
			market: Market{
				Exchange:     "binance",
				Symbol:       "BTCUSDT",
				BaseAsset:    "BTC",
				QuoteAsset:   "USDT",
				MarketType:   MarketTypeSpot,
				ContractType: ContractTypeSpot,
				Active:       false,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateMarket(tt.market)

			if tt.wantErr && err == nil {
				t.Fatal("ValidateMarket() expected error")
			}

			if !tt.wantErr && err != nil {
				t.Fatalf(
					"ValidateMarket() unexpected error = %v",
					err,
				)
			}
		})
	}
}

func TestIsSupportedMarket(t *testing.T) {
	t.Parallel()

	validSpot := Market{
		Exchange:     "mexc",
		Symbol:       "BTCUSDT",
		BaseAsset:    "BTC",
		QuoteAsset:   "USDT",
		MarketType:   MarketTypeSpot,
		ContractType: ContractTypeSpot,
		Active:       true,
	}

	if !IsSupportedMarket(validSpot) {
		t.Fatal("valid active spot market must be supported")
	}

	validSpot.Active = false

	if IsSupportedMarket(validSpot) {
		t.Fatal("inactive market must not be supported")
	}
}

func TestNormalizeMarket(t *testing.T) {
	t.Parallel()

	input := Market{
		Exchange:     " mexc ",
		Symbol:       "PEPE_USDT",
		BaseAsset:    " pepe ",
		QuoteAsset:   " usdt ",
		MarketType:   MarketTypeSpot,
		ContractType: ContractTypeSpot,
		Active:       true,
	}

	got, err := NormalizeMarket(input)
	if err != nil {
		t.Fatalf("NormalizeMarket() error = %v", err)
	}

	if got.Exchange != "mexc" {
		t.Fatalf("Exchange = %q, want %q", got.Exchange, "mexc")
	}

	if got.BaseAsset != "PEPE" {
		t.Fatalf("BaseAsset = %q, want %q", got.BaseAsset, "PEPE")
	}

	if got.QuoteAsset != "USDT" {
		t.Fatalf("QuoteAsset = %q, want %q", got.QuoteAsset, "USDT")
	}

	if got.Symbol != "PEPE_USDT" {
		t.Fatalf(
			"Symbol = %q, want original symbol %q",
			got.Symbol,
			"PEPE_USDT",
		)
	}
}
