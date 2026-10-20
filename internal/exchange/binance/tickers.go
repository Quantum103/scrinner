package binance

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

// collectTickers получает валидные тикеры для переданных рынков Binance.
func (a *Adapter) collectTickers(ctx context.Context, markets []market.Market) ([]price.Ticker, error) {
	tickers, _, err := a.collectTickersWithStats(ctx, markets)
	return tickers, err
}

// collectTickersWithStats получает bid/ask пакетно и возвращает статистику фильтрации.
func (a *Adapter) collectTickersWithStats(
	ctx context.Context,
	markets []market.Market,
) ([]price.Ticker, TickerStats, error) {
	spotMarkets := make(map[string]market.Market)
	futuresMarkets := make(map[string]market.Market)

	for _, item := range markets {
		normalized, err := market.NormalizeMarket(item)
		if err != nil {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(normalized.Exchange), "binance") ||
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
		case normalized.IsPerpetual():
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
			results <- categoryResult{tickers: result, stats: categoryStats, err: err, label: "USD-M Futures"}
		}()
	}

	for i := 0; i < enabledCategories; i++ {
		category := <-results
		if category.err != nil {
			cancel()
			return nil, stats, fmt.Errorf("получить Binance %s tickers: %w", category.label, category.err)
		}
		tickers = append(tickers, category.tickers...)
		stats.Received += category.stats.Received
		stats.Valid += category.stats.Valid
		stats.Skipped += category.stats.Skipped
	}
	a.logger.Info(
		"Binance tickers fetched",
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
		book      []bookTicker
		volumes   map[string]*decimal.Decimal
		receivedAt time.Time
		err       error
		kind      string
	}
	results := make(chan result, 2)
	requestCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		var book []bookTicker
		receivedAt, err := a.client.getJSON(
			requestCtx, false, "получить Spot bookTicker",
			spotBookTickerEndpoint, url.Values{}, &book,
		)
		results <- result{book: book, receivedAt: receivedAt, err: err, kind: "bookTicker"}
	}()
	go func() {
		volumes, err := a.fetchSpotVolumes(requestCtx, requested)
		results <- result{volumes: volumes, err: err, kind: "24h volume"}
	}()

	var book []bookTicker
	var volumes map[string]*decimal.Decimal
	var receivedAt time.Time
	for range 2 {
		item := <-results
		if item.err != nil {
			cancel()
			return nil, TickerStats{}, fmt.Errorf("получить Binance Spot %s: %w", item.kind, item.err)
		}
		if item.kind == "bookTicker" {
			book, receivedAt = item.book, item.receivedAt
		} else {
			volumes = item.volumes
		}
	}
	if book == nil {
		return nil, TickerStats{}, fmt.Errorf("Binance Spot bookTicker: ожидался JSON-массив")
	}
	return buildTickers("binance", market.MarketTypeSpot, market.ContractTypeSpot, book, requested, volumes, receivedAt)
}

func (a *Adapter) fetchFuturesTickers(
	ctx context.Context,
	requested map[string]market.Market,
) ([]price.Ticker, TickerStats, error) {
	type result struct {
		book       []bookTicker
		response   []futures24hrTicker
		receivedAt time.Time
		err        error
		kind       string
	}
	results := make(chan result, 2)
	requestCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		var book []bookTicker
		receivedAt, err := a.client.getJSON(
			requestCtx, true, "получить USD-M Futures bookTicker",
			futuresBookTickerEndpoint, url.Values{}, &book,
		)
		results <- result{book: book, receivedAt: receivedAt, err: err, kind: "bookTicker"}
	}()
	go func() {
		var response []futures24hrTicker
		_, err := a.client.getJSON(
			requestCtx, true, "получить USD-M Futures статистику за 24 часа",
			futures24hrTickerEndpoint, url.Values{}, &response,
		)
		results <- result{response: response, err: err, kind: "24h volume"}
	}()

	var book []bookTicker
	var response []futures24hrTicker
	var receivedAt time.Time
	for range 2 {
		item := <-results
		if item.err != nil {
			cancel()
			return nil, TickerStats{}, fmt.Errorf("получить Binance USD-M Futures %s: %w", item.kind, item.err)
		}
		if item.kind == "bookTicker" {
			book, receivedAt = item.book, item.receivedAt
		} else {
			response = item.response
		}
	}
	if book == nil {
		return nil, TickerStats{}, fmt.Errorf("Binance USD-M Futures bookTicker: ожидался JSON-массив")
	}
	if response == nil {
		return nil, TickerStats{}, fmt.Errorf("Binance USD-M Futures ticker/24hr: ожидался JSON-массив")
	}

	volumes := make(map[string]*decimal.Decimal, len(response))
	for _, item := range response {
		symbol := strings.ToUpper(strings.TrimSpace(item.Symbol))
		if symbol == "" {
			continue
		}
		if value := parseVolume(item.Volume); value != nil {
			volumes[symbol] = value
		}
	}
	return buildTickers("binance", market.MarketTypeFutures, market.ContractTypePerpetual, book, requested, volumes, receivedAt)
}

// fetchSpotVolumes запрашивает статистику не более чем для 100 символов за запрос.
func (a *Adapter) fetchSpotVolumes(
	ctx context.Context,
	requested map[string]market.Market,
) (map[string]*decimal.Decimal, error) {
	symbols := make([]string, 0, len(requested))
	for symbol := range requested {
		symbols = append(symbols, symbol)
	}

	volumes := make(map[string]*decimal.Decimal, len(symbols))
	for start := 0; start < len(symbols); start += maxSpotSymbols {
		end := start + maxSpotSymbols
		if end > len(symbols) {
			end = len(symbols)
		}
		encoded, err := json.Marshal(symbols[start:end])
		if err != nil {
			return nil, fmt.Errorf("сформировать список Binance Spot symbols: %w", err)
		}

		query := url.Values{}
		query.Set("symbols", string(encoded))
		var response []spot24hrTicker
		if _, err := a.client.getJSON(
			ctx,
			false,
			"получить Spot статистику за 24 часа",
			spot24hrTickerEndpoint,
			query,
			&response,
		); err != nil {
			return nil, err
		}
		if response == nil {
			return nil, fmt.Errorf("Binance Spot ticker/24hr: ожидался JSON-массив")
		}

		for _, item := range response {
			symbol := strings.ToUpper(strings.TrimSpace(item.Symbol))
			if symbol == "" {
				continue
			}
			if value := parseVolume(item.Volume); value != nil {
				volumes[symbol] = value
			}
		}
	}
	return volumes, nil
}

func buildTickers(
	exchangeName string,
	marketType market.MarketType,
	contractType market.ContractType,
	book []bookTicker,
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

		bid, err := decimal.NewFromString(strings.TrimSpace(item.BidPrice))
		if err != nil || !bid.GreaterThan(decimal.Zero) {
			stats.Skipped++
			continue
		}
		ask, err := decimal.NewFromString(strings.TrimSpace(item.AskPrice))
		if err != nil || !ask.GreaterThan(decimal.Zero) || ask.LessThan(bid) {
			stats.Skipped++
			continue
		}

		timestamp := receivedAt
		var exchangeTimestamp *time.Time
		if item.Time != nil && *item.Time > 0 {
			value := time.UnixMilli(*item.Time).UTC()
			exchangeTimestamp = &value
			timestamp = value
		}

		ticker := price.Ticker{
			Exchange:          exchangeName,
			Symbol:            requested[symbol].Symbol,
			MarketType:        marketType,
			ContractType:      contractType,
			Bid:               bid,
			Ask:               ask,
			Volume24h:         volumes[symbol],
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

func parseVolume(raw string) *decimal.Decimal {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	value, err := decimal.NewFromString(raw)
	if err != nil || value.IsNegative() {
		return nil
	}
	return &value
}
