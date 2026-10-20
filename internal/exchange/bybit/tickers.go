package bybit

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/example/crypto-arbitrage-scanner/internal/market"
	"github.com/example/crypto-arbitrage-scanner/internal/price"
	"github.com/shopspring/decimal"
)

// TickerStats содержит сводную статистику одного прохода получения тикеров.
type TickerStats struct {
	Received int
	Valid    int
	Skipped  int
}

// collectTickers получает тикеры отдельно для Spot и Linear.
func (a *Adapter) collectTickers(ctx context.Context, markets []market.Market) ([]price.Ticker, error) {
	tickers, _, err := a.collectTickersWithStats(ctx, markets)
	return tickers, err
}

// collectTickersWithStats получает тикеры и возвращает статистику фильтрации.
func (a *Adapter) collectTickersWithStats(ctx context.Context, markets []market.Market) ([]price.Ticker, TickerStats, error) {
	spotSymbols := make(map[string]struct{})
	linearSymbols := make(map[string]struct{})

	for _, m := range markets {
		normalized, err := market.NormalizeMarket(m)
		if err != nil {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(normalized.Exchange), "bybit") || !normalized.IsSupported() {
			continue
		}
		symbol := strings.TrimSpace(normalized.Symbol)
		if symbol == "" {
			continue
		}
		if normalized.IsSpot() {
			spotSymbols[symbol] = struct{}{}
		}
		if normalized.IsPerpetual() {
			linearSymbols[symbol] = struct{}{}
		}
	}

	result := make([]price.Ticker, 0, len(spotSymbols)+len(linearSymbols))
	stats := TickerStats{}
	if len(spotSymbols) == 0 && len(linearSymbols) == 0 {
		return result, stats, nil
	}

	type categoryResult struct {
		tickers []price.Ticker
		stats   TickerStats
		err     error
		label   string
	}
	enabledCategories := 0
	results := make(chan categoryResult, 2)
	requestCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	if len(spotSymbols) > 0 {
		enabledCategories++
		a.logger.Info("fetching Bybit spot tickers")
		go func() {
			tickers, tickStats, err := a.fetchTickers(requestCtx, spotCategory, spotSymbols)
			results <- categoryResult{tickers: tickers, stats: tickStats, err: err, label: "Spot"}
		}()
	}
	if len(linearSymbols) > 0 {
		enabledCategories++
		a.logger.Info("fetching Bybit linear tickers")
		go func() {
			tickers, tickStats, err := a.fetchTickers(requestCtx, linearCategory, linearSymbols)
			results <- categoryResult{tickers: tickers, stats: tickStats, err: err, label: "Linear"}
		}()
	}

	for i := 0; i < enabledCategories; i++ {
		category := <-results
		if category.err != nil {
			cancel()
			return nil, stats, fmt.Errorf("получить Bybit %s tickers: %w", category.label, category.err)
		}
		result = append(result, category.tickers...)
		stats.Received += category.stats.Received
		stats.Valid += category.stats.Valid
		stats.Skipped += category.stats.Skipped
	}
	a.logger.Info("Bybit tickers fetched", "received", stats.Received, "valid", stats.Valid, "skipped", stats.Skipped)
	return result, stats, nil
}

// fetchTickers получает все тикеры категории одним запросом.
func (a *Adapter) fetchTickers(ctx context.Context, category string, requested map[string]struct{}) ([]price.Ticker, TickerStats, error) {
	query := url.Values{}
	query.Set("category", category)

	var result tickerResult
	envelope, err := a.client.getJSON(ctx, "get tickers "+category, tickersEndpoint, query, &result)
	if err != nil {
		return nil, TickerStats{}, err
	}
	if !strings.EqualFold(strings.TrimSpace(result.Category), category) {
		return nil, TickerStats{}, fmt.Errorf("exchange=bybit operation=get tickers %s: API вернул category=%q", category, result.Category)
	}
	if result.List == nil {
		return nil, TickerStats{}, fmt.Errorf("exchange=bybit operation=get tickers %s: ответ не содержит result.list", category)
	}
	if envelope.Time <= 0 {
		return nil, TickerStats{}, fmt.Errorf("exchange=bybit operation=get tickers %s: API вернул некорректный time=%d", category, envelope.Time)
	}

	// Поле envelope.time относится ко времени ответа API в целом, а не к отдельной котировке.
	// Поэтому для контроля свежести используем локальный момент получения и не выдаём server time за ts конкретного тикера.
	if time.UnixMilli(envelope.Time).IsZero() {
		return nil, TickerStats{}, fmt.Errorf("exchange=bybit operation=get tickers %s: невозможно проверить time=%d", category, envelope.Time)
	}
	receivedAt := time.Now().UTC()

	stats := TickerStats{Received: len(result.List)}
	validTickers := make([]price.Ticker, 0, len(result.List))

	for _, item := range result.List {
		symbol := strings.TrimSpace(item.Symbol)
		if symbol == "" {
			stats.Skipped++
			continue
		}
		if _, ok := requested[symbol]; !ok {
			stats.Skipped++
			continue
		}

		bid, err := decimal.NewFromString(strings.TrimSpace(item.Bid1Price))
		if err != nil || !bid.GreaterThan(decimal.Zero) {
			stats.Skipped++
			continue
		}
		ask, err := decimal.NewFromString(strings.TrimSpace(item.Ask1Price))
		if err != nil || !ask.GreaterThan(decimal.Zero) || ask.LessThan(bid) {
			stats.Skipped++
			continue
		}

		var volume *decimal.Decimal
		if rawVolume := strings.TrimSpace(item.Volume24h); rawVolume != "" {
			parsedVolume, parseErr := decimal.NewFromString(rawVolume)
			if parseErr == nil && !parsedVolume.IsNegative() {
				volume = &parsedVolume
			}
		}

		marketType := market.MarketTypeSpot
		contractType := market.ContractTypeSpot
		if category == linearCategory {
			marketType = market.MarketTypeFutures
			contractType = market.ContractTypePerpetual
		}

		ticker := price.Ticker{
			Exchange:     "bybit",
			Symbol:       symbol,
			MarketType:   marketType,
			ContractType: contractType,
			Bid:          bid,
			Ask:          ask,
			Volume24h:    volume,
			Timestamp:    receivedAt,
			ReceivedAt:   receivedAt,
		}
		if !ticker.IsValid() {
			stats.Skipped++
			continue
		}

		validTickers = append(validTickers, ticker)
	}

	stats.Valid = len(validTickers)
	return validTickers, stats, nil
}
