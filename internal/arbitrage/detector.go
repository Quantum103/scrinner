package arbitrage

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/example/crypto-arbitrage-scanner/internal/market"
	"github.com/example/crypto-arbitrage-scanner/internal/price"
	"github.com/shopspring/decimal"
)

// Quote связывает котировку с нормализованным рынком и сохранённым снимком SQLite.
type Quote struct {
	Market     market.Market
	MarketID   int64
	SnapshotID int64
	Ticker     price.Ticker
}

// Detector обнаруживает межбиржевые возможности по актуальным лучшим bid/ask.
type Detector struct {
	MinSpreadPercent decimal.Decimal
	MaxDataAge       time.Duration
	MaxQuoteSkew     time.Duration
}

// NewDetector создаёт детектор с ограничением свежести и времени между котировками.
func NewDetector(minSpreadPercent decimal.Decimal, maxDataAge time.Duration) (*Detector, error) {
	if minSpreadPercent.IsNegative() {
		return nil, fmt.Errorf("минимальный валовой спред не может быть отрицательным")
	}
	if maxDataAge <= 0 {
		return nil, fmt.Errorf("maxDataAge должен быть положительным")
	}
	return &Detector{
		MinSpreadPercent: minSpreadPercent,
		MaxDataAge:       maxDataAge,
		MaxQuoteSkew:     maxDataAge,
	}, nil
}

// CalculateGrossSpreadPercent вычисляет валовой спред без комиссий и проскальзывания.
func CalculateGrossSpreadPercent(buyAsk, sellBid decimal.Decimal) (decimal.Decimal, error) {
	if !buyAsk.GreaterThan(decimal.Zero) {
		return decimal.Zero, fmt.Errorf("цена покупки по Ask должна быть положительной")
	}
	if !sellBid.GreaterThan(decimal.Zero) {
		return decimal.Zero, fmt.Errorf("цена продажи по Bid должна быть положительной")
	}
	return sellBid.Div(buyAsk).Sub(decimal.NewFromInt(1)).Mul(decimal.NewFromInt(100)), nil
}

// Detect возвращает исторические наблюдения для каждой допустимой направленной пары рынков.
// Каждая строка результата представляет одно направление в одном проходе сканера.
func (d *Detector) Detect(quotes []Quote, now time.Time) []Opportunity {
	if d == nil || now.IsZero() || d.MaxDataAge <= 0 || d.MaxQuoteSkew <= 0 || d.MinSpreadPercent.IsNegative() {
		return nil
	}

	groups := make(map[string][]Quote)
	seen := make(map[string]struct{}, len(quotes))
	for _, quote := range quotes {
		normalized, ok := normalizeQuote(quote, now, d.MaxDataAge)
		if !ok {
			continue
		}
		duplicateKey := marketIdentityKey(normalized.Market)
		if _, exists := seen[duplicateKey]; exists {
			continue
		}
		seen[duplicateKey] = struct{}{}

		// Для детекции группируем по активам, а не по canonical market key:
		// последний включает тип рынка и потому разделяет Spot и Perpetual.
		assetPairKey := normalized.Market.BaseAsset + "\x00" + normalized.Market.QuoteAsset
		groups[assetPairKey] = append(groups[assetPairKey], normalized)
	}

	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	result := make([]Opportunity, 0)
	for _, key := range keys {
		group := groups[key]
		sort.Slice(group, func(i, j int) bool {
			if group[i].Market.Exchange != group[j].Market.Exchange {
				return group[i].Market.Exchange < group[j].Market.Exchange
			}
			if group[i].Market.MarketType != group[j].Market.MarketType {
				return group[i].Market.MarketType < group[j].Market.MarketType
			}
			return group[i].Market.Symbol < group[j].Market.Symbol
		})

		for i := 0; i < len(group); i++ {
			for j := i + 1; j < len(group); j++ {
				first, second := group[i], group[j]
				if strings.EqualFold(first.Market.Exchange, second.Market.Exchange) {
					continue
				}
				if !quoteTimesCompatible(first.Ticker, second.Ticker, now, d.MaxQuoteSkew) {
					continue
				}

				if d.meetsThreshold(first.Ticker.Ask, second.Ticker.Bid) {
					if opportunity, ok := buildOpportunity(first, second, now); ok {
						result = append(result, opportunity)
					}
				}
				if d.meetsThreshold(second.Ticker.Ask, first.Ticker.Bid) {
					if opportunity, ok := buildOpportunity(second, first, now); ok {
						result = append(result, opportunity)
					}
				}
			}
		}
	}
	return result
}

func (d *Detector) meetsThreshold(buyAsk, sellBid decimal.Decimal) bool {
	if !buyAsk.GreaterThan(decimal.Zero) || !sellBid.GreaterThan(buyAsk) {
		return false
	}
	// Сравниваем произведения точно; не округляем результат деления до проверки порога.
	hundred := decimal.NewFromInt(100)
	return sellBid.Mul(hundred).GreaterThanOrEqual(buyAsk.Mul(hundred.Add(d.MinSpreadPercent)))
}

func normalizeQuote(quote Quote, now time.Time, maxAge time.Duration) (Quote, bool) {
	if quote.MarketID <= 0 || quote.SnapshotID <= 0 {
		return Quote{}, false
	}
	normalized, err := market.NormalizeMarket(quote.Market)
	if err != nil || !market.IsSupportedMarket(normalized) {
		return Quote{}, false
	}
	if !strings.EqualFold(strings.TrimSpace(quote.Ticker.Exchange), normalized.Exchange) ||
		!strings.EqualFold(strings.TrimSpace(quote.Ticker.Symbol), normalized.Symbol) ||
		quote.Ticker.MarketType != normalized.MarketType ||
		quote.Ticker.ContractType != normalized.ContractType ||
		!quote.Ticker.IsFresh(now, maxAge) {
		return Quote{}, false
	}
	quote.Market = normalized
	return quote, true
}

func quoteTimesCompatible(first, second price.Ticker, now time.Time, maxSkew time.Duration) bool {
	firstTime := first.ObservationTime()
	secondTime := second.ObservationTime()
	if firstTime.IsZero() || secondTime.IsZero() || maxSkew <= 0 {
		return false
	}
	if !timestampWithinRange(firstTime, now, maxSkew) || !timestampWithinRange(secondTime, now, maxSkew) {
		return false
	}
	delta := firstTime.Sub(secondTime)
	if delta < 0 {
		delta = -delta
	}
	return delta <= maxSkew
}

func timestampWithinRange(value, now time.Time, maxAge time.Duration) bool {
	age := now.Sub(value)
	return age >= 0 && age <= maxAge
}

func buildOpportunity(buy, sell Quote, now time.Time) (Opportunity, bool) {
	spread, err := CalculateGrossSpreadPercent(buy.Ticker.Ask, sell.Ticker.Bid)
	if err != nil || !spread.IsPositive() {
		return Opportunity{}, false
	}
	return Opportunity{
		Timestamp:        now.UTC(),
		Symbol:            market.NewSymbol(buy.Market.BaseAsset, buy.Market.QuoteAsset).String(),
		BaseAsset:         buy.Market.BaseAsset,
		QuoteAsset:        buy.Market.QuoteAsset,
		BuyMarketID:       buy.MarketID,
		BuySymbol:         buy.Market.Symbol,
		BuyExchange:       buy.Market.Exchange,
		BuyMarketType:     buy.Market.MarketType,
		BuyContractType:   buy.Market.ContractType,
		BuyPrice:          buy.Ticker.Ask,
		BuySnapshotID:     buy.SnapshotID,
		SellMarketID:      sell.MarketID,
		SellSymbol:        sell.Market.Symbol,
		SellExchange:      sell.Market.Exchange,
		SellMarketType:    sell.Market.MarketType,
		SellContractType:  sell.Market.ContractType,
		SellPrice:         sell.Ticker.Bid,
		SellSnapshotID:    sell.SnapshotID,
		SpreadPercent:     spread,
	}, true
}

func marketIdentityKey(item market.Market) string {
	return strings.ToLower(item.Exchange) + "|" + strings.ToUpper(item.Symbol) + "|" +
		string(item.MarketType) + "|" + string(item.ContractType)
}
