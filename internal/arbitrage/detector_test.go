package arbitrage

import (
	"testing"
	"time"

	"github.com/example/crypto-arbitrage-scanner/internal/market"
	"github.com/example/crypto-arbitrage-scanner/internal/price"
	"github.com/shopspring/decimal"
)

func TestDetectorSupportsAllFourMarketCombinations(t *testing.T) {
	spot := market.MarketTypeSpot
	perpetual := market.MarketTypeFutures
	spotContract := market.ContractTypeSpot
	perpetualContract := market.ContractTypePerpetual
	tests := []struct {
		name       string
		buyType    market.MarketType
		buyContract market.ContractType
		buySymbol  string
		sellType   market.MarketType
		sellContract market.ContractType
		sellSymbol string
	}{
		{"Spot to Spot", spot, spotContract, "BTCUSDT", spot, spotContract, "BTC_USDT"},
		{"Spot to Perpetual", spot, spotContract, "BTCUSDT", perpetual, perpetualContract, "BTCUSDT"},
		{"Perpetual to Spot", perpetual, perpetualContract, "BTC-USDT-SWAP", spot, spotContract, "BTC/USDT"},
		{"Perpetual to Perpetual", perpetual, perpetualContract, "BTC_USDT", perpetual, perpetualContract, "BTC-USDT"},
	}

	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	detector, err := NewDetector(decimal.NewFromInt(1), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for i, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			buy := detectorQuote("binance", test.buySymbol, test.buyType, test.buyContract, "BTC", "USDT", "99", "100", int64(100+i*2), now)
			sell := detectorQuote("bybit", test.sellSymbol, test.sellType, test.sellContract, "BTC", "USDT", "102", "103", int64(101+i*2), now)
			got := detector.Detect([]Quote{buy, sell}, now)
			if len(got) != 1 {
				t.Fatalf("ожидалась одна возможность в направлении покупки/продажи, получено %d: %#v", len(got), got)
			}
			op := got[0]
			if op.BuyExchange != "binance" || op.SellExchange != "bybit" {
				t.Fatalf("перепутано направление: buy=%s sell=%s", op.BuyExchange, op.SellExchange)
			}
			if op.BuySymbol != test.buySymbol || op.SellSymbol != test.sellSymbol {
				t.Fatalf("не сохранены исходные символы: buy=%s sell=%s", op.BuySymbol, op.SellSymbol)
			}
			if op.BuySnapshotID != buy.SnapshotID || op.SellSnapshotID != sell.SnapshotID {
				t.Fatalf("возможность должна ссылаться на использованные снимки: %#v", op)
			}
			if !op.SpreadPercent.Equal(decimal.NewFromInt(2)) {
				t.Fatalf("спред = %s%%, ожидалось 2%%", op.SpreadPercent)
			}
		})
	}
}

func TestDetectorUsesAskToBuyBidToSellAndConsidersReverseDirectionIndependently(t *testing.T) {
	now := time.Now().UTC()
	detector, _ := NewDetector(decimal.NewFromInt(1), 10*time.Second)
	first := detectorQuote("binance", "BTCUSDT", market.MarketTypeSpot, market.ContractTypeSpot, "BTC", "USDT", "99", "100", 1, now)
	second := detectorQuote("bybit", "BTCUSDT", market.MarketTypeFutures, market.ContractTypePerpetual, "BTC", "USDT", "102", "103", 2, now)

	got := detector.Detect([]Quote{first, second}, now)
	if len(got) != 1 {
		t.Fatalf("ожидалось ровно одно направление, получено %d", len(got))
	}
	if got[0].BuyMarketID != first.MarketID || got[0].SellMarketID != second.MarketID {
		t.Fatalf("детектор должен покупать по первому Ask и продавать по второму Bid: %#v", got[0])
	}

	// Если единственное выгодное направление обратное, оно также должно обнаруживаться.
	reverseFirst := detectorQuote("binance", "BTCUSDT", market.MarketTypeSpot, market.ContractTypeSpot, "BTC", "USDT", "101", "102", 3, now)
	reverseSecond := detectorQuote("bybit", "BTCUSDT", market.MarketTypeFutures, market.ContractTypePerpetual, "BTC", "USDT", "99", "100", 4, now)
	got = detector.Detect([]Quote{reverseFirst, reverseSecond}, now)
	if len(got) != 1 || got[0].BuyMarketID != reverseSecond.MarketID || got[0].SellMarketID != reverseFirst.MarketID {
		t.Fatalf("обратное направление не обнаружено корректно: %#v", got)
	}
}

func TestDetectorThresholdUsesExactDecimalComparison(t *testing.T) {
	now := time.Now().UTC()
	detector, _ := NewDetector(decimal.NewFromInt(1), 10*time.Second)
	tests := []struct {
		name string
		bid  string
		want int
	}{
		{"below", "100.999999999999999999", 0},
		{"exact threshold", "101", 1},
		{"above", "101.000000000000000001", 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			buy := detectorQuote("binance", "BTCUSDT", market.MarketTypeSpot, market.ContractTypeSpot, "BTC", "USDT", "99", "100", 1, now)
			sell := detectorQuote("bybit", "BTCUSDT", market.MarketTypeSpot, market.ContractTypeSpot, "BTC", "USDT", test.bid, "102", 2, now)
			got := detector.Detect([]Quote{buy, sell}, now)
			if len(got) != test.want {
				t.Fatalf("получено %d возможностей, ожидалось %d", len(got), test.want)
			}
		})
	}
}

func TestDetectorRejectsSameExchangeDifferentAssetsAndStaleQuotes(t *testing.T) {
	now := time.Now().UTC()
	detector, _ := NewDetector(decimal.NewFromInt(1), 10*time.Second)
	buy := detectorQuote("binance", "BTCUSDT", market.MarketTypeSpot, market.ContractTypeSpot, "BTC", "USDT", "99", "100", 1, now)
	sell := detectorQuote("binance", "BTC_USDT", market.MarketTypeFutures, market.ContractTypePerpetual, "BTC", "USDT", "102", "103", 2, now)
	if got := detector.Detect([]Quote{buy, sell}, now); len(got) != 0 {
		t.Fatalf("нельзя создавать возможности внутри одной биржи: %#v", got)
	}
	sell.Market.BaseAsset = "ETH"
	if got := detector.Detect([]Quote{buy, sell}, now); len(got) != 0 {
		t.Fatalf("разные базовые активы не сопоставляются: %#v", got)
	}
	sell.Market.BaseAsset = "BTC"
	sell.Market.QuoteAsset = "USDC"
	if got := detector.Detect([]Quote{buy, sell}, now); len(got) != 0 {
		t.Fatalf("рынки с валютой кроме USDT должны исключаться: %#v", got)
	}
	sell.Market.QuoteAsset = "USDT"
	old := now.Add(-11 * time.Second)
	sell.Ticker.Timestamp = old
	sell.Ticker.ReceivedAt = old
	if got := detector.Detect([]Quote{buy, sell}, now); len(got) != 0 {
		t.Fatalf("устаревшая котировка не должна участвовать: %#v", got)
	}
}

func TestDetectorRejectsLargeTimestampSkewAndDuplicateDirections(t *testing.T) {
	now := time.Now().UTC()
	detector, _ := NewDetector(decimal.NewFromInt(1), 10*time.Second)
	buy := detectorQuote("binance", "BTCUSDT", market.MarketTypeSpot, market.ContractTypeSpot, "BTC", "USDT", "99", "100", 1, now)
	sell := detectorQuote("bybit", "BTCUSDT", market.MarketTypeSpot, market.ContractTypeSpot, "BTC", "USDT", "102", "103", 2, now)
	oldEvent := now.Add(-6 * time.Second)
	sell.Ticker.ExchangeTimestamp = &oldEvent
	// ReceivedAt и основная метка свежие, но источник сильно отстаёт относительно первой котировки.
	if got := detector.Detect([]Quote{buy, sell}, now); len(got) != 0 {
		t.Fatalf("слишком большой временной разрыв должен отклоняться: %#v", got)
	}

	sell.Ticker.ExchangeTimestamp = nil
	got := detector.Detect([]Quote{buy, sell, sell}, now)
	if len(got) != 1 {
		t.Fatalf("дубликат одной и той же направленной пары не должен размножать возможности: %d", len(got))
	}
}

func detectorQuote(exchangeName, symbol string, marketType market.MarketType, contractType market.ContractType, base, quote, bid, ask string, id int64, now time.Time) Quote {
	bidValue, _ := decimal.NewFromString(bid)
	askValue, _ := decimal.NewFromString(ask)
	receivedAt := now.Add(-time.Second)
	return Quote{
		Market: market.Market{
			Exchange: exchangeName, Symbol: symbol, BaseAsset: base, QuoteAsset: quote,
			MarketType: marketType, ContractType: contractType, Active: true,
		},
		MarketID: id, SnapshotID: id + 1000,
		Ticker: price.Ticker{
			Exchange: exchangeName, Symbol: symbol, MarketType: marketType, ContractType: contractType,
			Bid: bidValue, Ask: askValue, Timestamp: receivedAt, ReceivedAt: receivedAt,
		},
	}
}
