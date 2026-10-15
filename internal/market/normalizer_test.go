package market

import (
	"testing"
)

func TestNormalizeSymbolFromParts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		base      string
		quote     string
		wantBase  string
		wantQuote string
	}{
		{
			name:      "uppercase",
			base:      "BTC",
			quote:     "USDT",
			wantBase:  "BTC",
			wantQuote: "USDT",
		},
		{
			name:      "lowercase",
			base:      "btc",
			quote:     "usdt",
			wantBase:  "BTC",
			wantQuote: "USDT",
		},
		{
			name:      "mixed case and spaces",
			base:      " Btc ",
			quote:     " UsDt ",
			wantBase:  "BTC",
			wantQuote: "USDT",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := NormalizeSymbolFromParts(tt.base, tt.quote)
			if err != nil {
				t.Fatalf("NormalizeSymbolFromParts() error = %v", err)
			}

			if got.BaseAsset != tt.wantBase {
				t.Fatalf(
					"BaseAsset = %q, want %q",
					got.BaseAsset,
					tt.wantBase,
				)
			}

			if got.QuoteAsset != tt.wantQuote {
				t.Fatalf(
					"QuoteAsset = %q, want %q",
					got.QuoteAsset,
					tt.wantQuote,
				)
			}
		})
	}
}

func TestParseSymbolDelimited(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     string
		wantBase  string
		wantQuote string
	}{
		{
			name:      "dash",
			input:     "BTC-USDT",
			wantBase:  "BTC",
			wantQuote: "USDT",
		},
		{
			name:      "underscore",
			input:     "BTC_USDT",
			wantBase:  "BTC",
			wantQuote: "USDT",
		},
		{
			name:      "slash",
			input:     "BTC/USDT",
			wantBase:  "BTC",
			wantQuote: "USDT",
		},
		{
			name:      "lowercase",
			input:     "btc-usdt",
			wantBase:  "BTC",
			wantQuote: "USDT",
		},
		{
			name:      "mixed case",
			input:     "Btc/Usdt",
			wantBase:  "BTC",
			wantQuote: "USDT",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseSymbol(
				tt.input,
				[]string{"BTC"},
				[]string{"USDT"},
			)
			if err != nil {
				t.Fatalf("ParseSymbol() error = %v", err)
			}

			if got.BaseAsset != tt.wantBase {
				t.Fatalf(
					"BaseAsset = %q, want %q",
					got.BaseAsset,
					tt.wantBase,
				)
			}

			if got.QuoteAsset != tt.wantQuote {
				t.Fatalf(
					"QuoteAsset = %q, want %q",
					got.QuoteAsset,
					tt.wantQuote,
				)
			}
		})
	}
}

func TestParseSymbolConcatenated(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     string
		base      string
		wantBase  string
		wantQuote string
	}{
		{
			name:      "btc",
			input:     "BTCUSDT",
			base:      "BTC",
			wantBase:  "BTC",
			wantQuote: "USDT",
		},
		{
			name:      "eth",
			input:     "ETHUSDT",
			base:      "ETH",
			wantBase:  "ETH",
			wantQuote: "USDT",
		},
		{
			name:      "sol lowercase",
			input:     "solusdt",
			base:      "SOL",
			wantBase:  "SOL",
			wantQuote: "USDT",
		},
		{
			name:      "pepe",
			input:     "PEPEUSDT",
			base:      "PEPE",
			wantBase:  "PEPE",
			wantQuote: "USDT",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseSymbol(
				tt.input,
				[]string{"BTC", "ETH", "SOL", "PEPE"},
				[]string{"USDT"},
			)
			if err != nil {
				t.Fatalf("ParseSymbol() error = %v", err)
			}

			if got.BaseAsset != tt.wantBase {
				t.Fatalf(
					"BaseAsset = %q, want %q",
					got.BaseAsset,
					tt.wantBase,
				)
			}

			if got.QuoteAsset != tt.wantQuote {
				t.Fatalf(
					"QuoteAsset = %q, want %q",
					got.QuoteAsset,
					tt.wantQuote,
				)
			}
		})
	}
}

func TestParseSymbolDoesNotGuessUnknownConcatenatedSymbol(t *testing.T) {
	t.Parallel()

	_, err := ParseSymbol(
		"ABCXYZUSDT",
		[]string{"BTC", "ETH", "SOL"},
		[]string{"USDT"},
	)

	if err == nil {
		t.Fatal("ParseSymbol() expected error for unknown concatenated symbol")
	}
}

func TestParseSymbolRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	tests := []string{
		"",
		"   ",
		"BTC USDT",
		"BTC\nUSDT",
		"BTC--USDT",
		"BTC/USDT/TEST",
	}

	for _, input := range tests {
		t.Run(input, func(t *testing.T) {
			_, err := ParseSymbol(
				input,
				[]string{"BTC"},
				[]string{"USDT"},
			)

			if err == nil {
				t.Fatalf(
					"ParseSymbol(%q) expected error",
					input,
				)
			}
		})
	}
}

func TestCanonicalMarketKey(t *testing.T) {
	t.Parallel()

	spot := Market{
		Exchange:     "mexc",
		Symbol:       "PEPEUSDT",
		BaseAsset:    "pepe",
		QuoteAsset:   "usdt",
		MarketType:   MarketTypeSpot,
		ContractType: ContractTypeSpot,
		Active:       true,
	}

	perpetual := Market{
		Exchange:     "bybit",
		Symbol:       "PEPEUSDT",
		BaseAsset:    "PEPE",
		QuoteAsset:   "USDT",
		MarketType:   MarketTypeFutures,
		ContractType: ContractTypePerpetual,
		Active:       true,
	}

	spotKey, err := CanonicalMarketKey(spot)
	if err != nil {
		t.Fatalf("CanonicalMarketKey(spot) error = %v", err)
	}

	perpetualKey, err := CanonicalMarketKey(perpetual)
	if err != nil {
		t.Fatalf("CanonicalMarketKey(perpetual) error = %v", err)
	}

	if spotKey != "PEPE:USDT:SPOT" {
		t.Fatalf(
			"spot key = %q, want %q",
			spotKey,
			"PEPE:USDT:SPOT",
		)
	}

	if perpetualKey != "PEPE:USDT:PERPETUAL" {
		t.Fatalf(
			"perpetual key = %q, want %q",
			perpetualKey,
			"PEPE:USDT:PERPETUAL",
		)
	}

	if spotKey == perpetualKey {
		t.Fatal("spot and perpetual keys must be different")
	}
}

func TestGroupMarkets(t *testing.T) {
	t.Parallel()

	markets := []Market{
		{
			Exchange:     "binance",
			Symbol:       "PEPEUSDT",
			BaseAsset:    "PEPE",
			QuoteAsset:   "USDT",
			MarketType:   MarketTypeSpot,
			ContractType: ContractTypeSpot,
			Active:       true,
		},
		{
			Exchange:     "bybit",
			Symbol:       "PEPEUSDT",
			BaseAsset:    "PEPE",
			QuoteAsset:   "USDT",
			MarketType:   MarketTypeFutures,
			ContractType: ContractTypePerpetual,
			Active:       true,
		},
		{
			Exchange:     "mexc",
			Symbol:       "PEPE_USDT",
			BaseAsset:    "pepe",
			QuoteAsset:   "usdt",
			MarketType:   MarketTypeSpot,
			ContractType: ContractTypeSpot,
			Active:       true,
		},
		{
			Exchange:     "okx",
			Symbol:       "BTC-USDT",
			BaseAsset:    "BTC",
			QuoteAsset:   "USDT",
			MarketType:   MarketTypeSpot,
			ContractType: ContractTypeSpot,
			Active:       true,
		},
	}

	groups, err := GroupMarkets(markets)
	if err != nil {
		t.Fatalf("GroupMarkets() error = %v", err)
	}

	if len(groups) != 3 {
		t.Fatalf("got %d groups, want 3", len(groups))
	}

	if len(groups["PEPE:USDT:SPOT"]) != 2 {
		t.Fatalf(
			"PEPE spot group contains %d markets, want 2",
			len(groups["PEPE:USDT:SPOT"]),
		)
	}

	if len(groups["PEPE:USDT:PERPETUAL"]) != 1 {
		t.Fatalf(
			"PEPE perpetual group contains %d markets, want 1",
			len(groups["PEPE:USDT:PERPETUAL"]),
		)
	}

	if len(groups["BTC:USDT:SPOT"]) != 1 {
		t.Fatalf(
			"BTC spot group contains %d markets, want 1",
			len(groups["BTC:USDT:SPOT"]),
		)
	}
}

func TestGroupMarketsSkipsInactiveMarkets(t *testing.T) {
	t.Parallel()

	markets := []Market{
		{
			Exchange:     "binance",
			Symbol:       "BTCUSDT",
			BaseAsset:    "BTC",
			QuoteAsset:   "USDT",
			MarketType:   MarketTypeSpot,
			ContractType: ContractTypeSpot,
			Active:       false,
		},
	}

	groups, err := GroupMarkets(markets)
	if err != nil {
		t.Fatalf("GroupMarkets() error = %v", err)
	}

	if len(groups) != 0 {
		t.Fatalf(
			"got %d groups for inactive market, want 0",
			len(groups),
		)
	}
}
