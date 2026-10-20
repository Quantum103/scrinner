package okx

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/example/crypto-arbitrage-scanner/internal/market"
	"github.com/example/crypto-arbitrage-scanner/internal/price"
	"github.com/shopspring/decimal"
)

// collectTickersWithStats собирает пакетные котировки для поддерживаемых рынков OKX.
func (a *Adapter) collectTickersWithStats(
	ctx context.Context,
	markets []market.Market,
) ([]price.Ticker, TickerStats, error) {
	spotMarkets := make(map[string]market.Market)
	swapMarkets := make(map[string]market.Market)
	for _, item := range markets {
		normalized, err := market.NormalizeMarket(item)
		if err != nil ||
			!strings.EqualFold(strings.TrimSpace(normalized.Exchange), "okx") ||
			!normalized.IsSupported() ||
			!strings.EqualFold(strings.TrimSpace(normalized.QuoteAsset), usdtAsset) {
			continue
		}
		switch {
		case normalized.IsSpot():
			spotMarkets[normalized.Symbol] = normalized
		case normalized.IsPerpetual():
			swapMarkets[normalized.Symbol] = normalized
		}
	}

	tickers := make([]price.Ticker, 0, len(spotMarkets)+len(swapMarkets))
	stats := TickerStats{}
	if len(spotMarkets) == 0 && len(swapMarkets) == 0 {
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
			result, categoryStats, err := a.fetchTickers(requestCtx, "SPOT", spotMarkets)
			results <- categoryResult{tickers: result, stats: categoryStats, err: err, label: "Spot"}
		}()
	}
	if len(swapMarkets) > 0 {
		enabledCategories++
		go func() {
			result, categoryStats, err := a.fetchTickers(requestCtx, "SWAP", swapMarkets)
			results <- categoryResult{tickers: result, stats: categoryStats, err: err, label: "SWAP"}
		}()
	}

	for i := 0; i < enabledCategories; i++ {
		category := <-results
		if category.err != nil {
			cancel()
			return nil, stats, fmt.Errorf("получить OKX %s tickers: %w", category.label, category.err)
		}
		tickers = append(tickers, category.tickers...)
		stats.Received += category.stats.Received
		stats.Valid += category.stats.Valid
		stats.Skipped += category.stats.Skipped
	}
	a.logger.Info(
		"OKX tickers fetched",
		"received", stats.Received,
		"valid", stats.Valid,
		"skipped", stats.Skipped,
	)
	return tickers, stats, nil
}

func (a *Adapter) fetchTickers(
	ctx context.Context,
	instType string,
	requested map[string]market.Market,
) ([]price.Ticker, TickerStats, error) {
	var response []marketTicker
	receivedAt, err := a.client.getJSON(
		ctx,
		"получить пакетные "+instType+" tickers",
		marketTickersEndpoint,
		url.Values{"instType": []string{instType}},
		&response,
	)
	if err != nil {
		return nil, TickerStats{}, err
	}
	if len(response) == 0 {
		return nil, TickerStats{}, fmt.Errorf("OKX market/tickers?instType=%s вернул пустой список", instType)
	}
	return buildTickers(response, requested, instType, receivedAt)
}

// buildTickers сопоставляет данные только по точному instId и типу инструмента.
func buildTickers(
	response []marketTicker,
	requested map[string]market.Market,
	instType string,
	receivedAt time.Time,
) ([]price.Ticker, TickerStats, error) {
	stats := TickerStats{Received: len(response)}
	result := make([]price.Ticker, 0, len(requested))
	seen := make(map[string]struct{}, len(requested))

	for _, item := range response {
		if !strings.EqualFold(strings.TrimSpace(item.InstType), instType) {
			stats.Skipped++
			continue
		}
		m, ok := requested[item.InstID]
		if !ok || strings.TrimSpace(item.InstID) == "" {
			stats.Skipped++
			continue
		}
		if _, duplicate := seen[item.InstID]; duplicate {
			stats.Skipped++
			continue
		}
		seen[item.InstID] = struct{}{}

		bid, err := parseDecimalRaw(item.BidPx)
		if err != nil || !bid.GreaterThan(decimal.Zero) {
			stats.Skipped++
			continue
		}
		ask, err := parseDecimalRaw(item.AskPx)
		if err != nil || !ask.GreaterThan(decimal.Zero) || ask.LessThan(bid) {
			stats.Skipped++
			continue
		}
		milliseconds, present, err := parseRawString(item.Timestamp)
		if err != nil || !present {
			stats.Skipped++
			continue
		}
		millis, err := strconv.ParseInt(strings.TrimSpace(milliseconds), 10, 64)
		if err != nil || millis <= 0 {
			stats.Skipped++
			continue
		}
		timestamp := time.UnixMilli(millis).UTC()

		var volume *decimal.Decimal
		volumeRaw := item.Vol24h
		if instType == "SWAP" {
			// Для деривативов volCcy24h выражен в базовой валюте, а vol24h — в контрактах.
			volumeRaw = item.VolCcy24h
		}
		if parsedVolume, parseErr := parseDecimalRaw(volumeRaw); parseErr == nil &&
			!parsedVolume.IsNegative() {
			volume = &parsedVolume
		}

		ticker := price.Ticker{
			Exchange:          "okx",
			Symbol:            m.Symbol,
			MarketType:        m.MarketType,
			ContractType:      m.ContractType,
			Bid:               bid,
			Ask:               ask,
			Volume24h:         volume,
			Timestamp:         timestamp,
			ReceivedAt:        receivedAt,
			ExchangeTimestamp: &timestamp,
		}
		if !ticker.IsValid() {
			stats.Skipped++
			continue
		}
		result = append(result, ticker)
	}

	stats.Valid = len(result)
	stats.Skipped = stats.Received - stats.Valid
	return result, stats, nil
}

func parseDecimalRaw(raw []byte) (decimal.Decimal, error) {
	value, present, err := parseRawString(raw)
	if err != nil {
		return decimal.Zero, err
	}
	if !present || value == "" {
		return decimal.Zero, fmt.Errorf("decimal отсутствует")
	}
	return decimal.NewFromString(value)
}
