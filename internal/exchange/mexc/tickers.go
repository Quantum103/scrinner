package mexc

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/example/crypto-arbitrage-scanner/internal/market"
	"github.com/example/crypto-arbitrage-scanner/internal/price"
	"github.com/shopspring/decimal"
)

// collectTickersWithStats собирает пакетные котировки для поддерживаемых рынков.
func (a *Adapter) collectTickersWithStats(
	ctx context.Context,
	markets []market.Market,
) ([]price.Ticker, TickerStats, error) {
	spotMarkets := make(map[string]market.Market)
	futuresMarkets := make(map[string]market.Market)
	for _, item := range markets {
		normalized, err := market.NormalizeMarket(item)
		if err != nil ||
			!strings.EqualFold(strings.TrimSpace(normalized.Exchange), "mexc") ||
			!normalized.IsSupported() ||
			!strings.EqualFold(strings.TrimSpace(normalized.QuoteAsset), usdtAsset) {
			continue
		}

		symbol := strings.ToUpper(strings.TrimSpace(normalized.Symbol))
		if symbol == "" {
			continue
		}
		switch {
		case normalized.IsSpot():
			spotMarkets[symbol] = normalized
		case normalized.IsPerpetual() &&
			strings.EqualFold(strings.TrimSpace(normalized.QuoteAsset), usdtAsset):
			futuresMarkets[symbol] = normalized
		}
	}

	tickers := make([]price.Ticker, 0, len(spotMarkets)+len(futuresMarkets))
	stats := TickerStats{}
	if len(spotMarkets) == 0 && len(futuresMarkets) == 0 {
		return tickers, stats, nil
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

	if len(spotMarkets) > 0 {
		enabledCategories++
		go func() {
			result, categoryStats, err := a.fetchSpotTickers(requestCtx, spotMarkets)
			results <- categoryResult{tickers: result, stats: categoryStats, err: err, label: "Spot"}
		}()
	}
	if len(futuresMarkets) > 0 {
		enabledCategories++
		go func() {
			result, categoryStats, err := a.fetchFuturesTickers(requestCtx, futuresMarkets)
			results <- categoryResult{tickers: result, stats: categoryStats, err: err, label: "Futures"}
		}()
	}

	for i := 0; i < enabledCategories; i++ {
		category := <-results
		if category.err != nil {
			cancel()
			return nil, stats, fmt.Errorf("получить MEXC %s tickers: %w", category.label, category.err)
		}
		tickers = append(tickers, category.tickers...)
		stats.Received += category.stats.Received
		stats.Valid += category.stats.Valid
		stats.Skipped += category.stats.Skipped
	}
	a.logger.Info(
		"MEXC tickers fetched",
		"received", stats.Received,
		"valid", stats.Valid,
		"skipped", stats.Skipped,
	)
	return tickers, stats, nil
}

func (a *Adapter) fetchSpotTickers(
	ctx context.Context,
	requested map[string]market.Market,
) ([]price.Ticker, TickerStats, error) {
	type result struct {
		book       spotTickerList
		stats      spotStatsList
		receivedAt time.Time
		err        error
		kind       string
	}
	results := make(chan result, 2)
	requestCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		var book spotTickerList
		receivedAt, err := a.client.getJSON(
			requestCtx, false, "получить Spot bookTicker",
			spotBookTickerEndpoint, url.Values{}, &book,
		)
		results <- result{book: book, receivedAt: receivedAt, err: err, kind: "bookTicker"}
	}()
	go func() {
		var stats spotStatsList
		_, err := a.client.getJSON(
			requestCtx, false, "получить Spot статистику за 24 часа",
			spot24hrTickerEndpoint, url.Values{}, &stats,
		)
		results <- result{stats: stats, err: err, kind: "24h volume"}
	}()

	var book spotTickerList
	var statsResponse spotStatsList
	var receivedAt time.Time
	for range 2 {
		item := <-results
		if item.err != nil {
			cancel()
			return nil, TickerStats{}, fmt.Errorf("получить MEXC Spot %s: %w", item.kind, item.err)
		}
		if item.kind == "bookTicker" {
			book, receivedAt = item.book, item.receivedAt
		} else {
			statsResponse = item.stats
		}
	}
	if len(book) == 0 {
		return nil, TickerStats{}, fmt.Errorf("MEXC Spot bookTicker: ответ не содержит котировок")
	}
	if statsResponse == nil {
		return nil, TickerStats{}, fmt.Errorf("MEXC Spot ticker/24hr: ожидался массив или объект статистики")
	}

	volumes := make(map[string]*decimal.Decimal, len(statsResponse))
	for _, item := range statsResponse {
		symbol := strings.ToUpper(strings.TrimSpace(item.Symbol))
		if symbol == "" {
			continue
		}
		if _, requestedSymbol := requested[symbol]; !requestedSymbol {
			continue
		}
		if volume := parseOptionalVolume(item.Volume); volume != nil {
			volumes[symbol] = volume
		}
	}

	return buildSpotTickers(book, requested, volumes, receivedAt)
}

func (a *Adapter) fetchFuturesTickers(
	ctx context.Context,
	requested map[string]market.Market,
) ([]price.Ticker, TickerStats, error) {
	var response futuresTickerList
	receivedAt, err := a.client.getJSON(
		ctx,
		true,
		"получить Futures tickers",
		futuresTickerEndpoint,
		url.Values{},
		&response,
	)
	if err != nil {
		return nil, TickerStats{}, err
	}
	if len(response) == 0 {
		return nil, TickerStats{}, fmt.Errorf("MEXC Futures ticker: ответ не содержит котировок")
	}

	a.mu.RLock()
	contractSizes := make(map[string]decimal.Decimal, len(a.contractSizes))
	for symbol, size := range a.contractSizes {
		contractSizes[symbol] = size
	}
	a.mu.RUnlock()

	return buildFuturesTickers(response, requested, contractSizes, receivedAt)
}

func buildSpotTickers(
	book spotTickerList,
	requested map[string]market.Market,
	volumes map[string]*decimal.Decimal,
	receivedAt time.Time,
) ([]price.Ticker, TickerStats, error) {
	stats := TickerStats{Received: len(book)}
	result := make([]price.Ticker, 0, len(requested))
	seen := make(map[string]struct{}, len(requested))
	for _, item := range book {
		symbol := strings.ToUpper(strings.TrimSpace(item.Symbol))
		if symbol == "" {
			stats.Skipped++
			continue
		}
		if _, ok := requested[symbol]; !ok {
			stats.Skipped++
			continue
		}
		if _, duplicate := seen[symbol]; duplicate {
			stats.Skipped++
			continue
		}
		seen[symbol] = struct{}{}

		bid, err := parseDecimalRaw(item.BidPrice)
		if err != nil || !bid.GreaterThan(decimal.Zero) {
			stats.Skipped++
			continue
		}
		ask, err := parseDecimalRaw(item.AskPrice)
		if err != nil || !ask.GreaterThan(decimal.Zero) || ask.LessThan(bid) {
			stats.Skipped++
			continue
		}

		ticker := price.Ticker{
			Exchange:     "mexc",
			Symbol:       requested[symbol].Symbol,
			MarketType:   market.MarketTypeSpot,
			ContractType: market.ContractTypeSpot,
			Bid:          bid,
			Ask:          ask,
			Volume24h:    volumes[symbol],
			Timestamp:    receivedAt,
			ReceivedAt:   receivedAt,
		}
		if !ticker.IsValid() {
			stats.Skipped++
			continue
		}
		result = append(result, ticker)
	}
	stats.Valid = len(result)
	return result, stats, nil
}

func buildFuturesTickers(
	book futuresTickerList,
	requested map[string]market.Market,
	contractSizes map[string]decimal.Decimal,
	receivedAt time.Time,
) ([]price.Ticker, TickerStats, error) {
	stats := TickerStats{Received: len(book)}
	result := make([]price.Ticker, 0, len(requested))
	seen := make(map[string]struct{}, len(requested))
	for _, item := range book {
		symbol := strings.ToUpper(strings.TrimSpace(item.Symbol))
		if symbol == "" {
			stats.Skipped++
			continue
		}
		if _, ok := requested[symbol]; !ok {
			stats.Skipped++
			continue
		}
		if _, duplicate := seen[symbol]; duplicate {
			stats.Skipped++
			continue
		}
		seen[symbol] = struct{}{}

		bid, err := parseDecimalRaw(item.Bid1)
		if err != nil || !bid.GreaterThan(decimal.Zero) {
			stats.Skipped++
			continue
		}
		ask, err := parseDecimalRaw(item.Ask1)
		if err != nil || !ask.GreaterThan(decimal.Zero) || ask.LessThan(bid) {
			stats.Skipped++
			continue
		}

		timestamp := receivedAt
		var exchangeTimestamp *time.Time
		milliseconds, timestampPresent, timestampErr := parseIntegerRaw(item.Timestamp)
		if timestampErr != nil {
			stats.Skipped++
			continue
		}
		if timestampPresent && milliseconds > 0 {
			value := time.UnixMilli(milliseconds).UTC()
			timestamp = value
			exchangeTimestamp = &value
		}

		var volume *decimal.Decimal
		if contracts := parseOptionalVolume(item.Volume24); contracts != nil {
			if contractSize, ok := contractSizes[symbol]; ok && contractSize.GreaterThan(decimal.Zero) {
				baseVolume := contracts.Mul(contractSize)
				volume = &baseVolume
			}
		}

		ticker := price.Ticker{
			Exchange:          "mexc",
			Symbol:            requested[symbol].Symbol,
			MarketType:        market.MarketTypeFutures,
			ContractType:      market.ContractTypePerpetual,
			Bid:               bid,
			Ask:               ask,
			Volume24h:         volume,
			Timestamp:         timestamp,
			ReceivedAt:        receivedAt,
			ExchangeTimestamp: exchangeTimestamp,
		}
		if !ticker.IsValid() {
			stats.Skipped++
			continue
		}
		result = append(result, ticker)
	}
	stats.Valid = len(result)
	return result, stats, nil
}

func parseDecimalRaw(raw json.RawMessage) (decimal.Decimal, error) {
	value := strings.TrimSpace(string(raw))
	if value == "" || value == "null" {
		return decimal.Zero, fmt.Errorf("decimal отсутствует")
	}
	if len(value) > 0 && value[0] == '"' {
		var decoded string
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return decimal.Zero, err
		}
		value = strings.TrimSpace(decoded)
	}
	return decimal.NewFromString(value)
}

func parseOptionalVolume(raw json.RawMessage) *decimal.Decimal {
	value, err := parseDecimalRaw(raw)
	if err != nil || value.IsNegative() {
		return nil
	}
	return &value
}
