package gate

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/example/crypto-arbitrage-scanner/internal/market"
	"github.com/example/crypto-arbitrage-scanner/internal/price"
	"github.com/shopspring/decimal"
)

// collectTickersWithStats получает пакетные котировки только для поддерживаемых рынков Gate.
func (a *Adapter) collectTickersWithStats(
	ctx context.Context,
	markets []market.Market,
) ([]price.Ticker, TickerStats, error) {
	spotMarkets := make(map[string]market.Market)
	futuresMarkets := make(map[string]market.Market)
	for _, item := range markets {
		normalized, err := market.NormalizeMarket(item)
		if err != nil ||
			!strings.EqualFold(strings.TrimSpace(normalized.Exchange), "gate") ||
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

	result := make([]price.Ticker, 0, len(spotMarkets)+len(futuresMarkets))
	stats := TickerStats{}
	if len(spotMarkets) == 0 && len(futuresMarkets) == 0 {
		return result, stats, nil
	}

	if len(spotMarkets) > 0 {
		tickers, tickStats, err := a.fetchSpotTickers(ctx, spotMarkets)
		if err != nil {
			return nil, stats, fmt.Errorf("получить Gate Spot tickers: %w", err)
		}
		result = append(result, tickers...)
		stats.Received += tickStats.Received
		stats.Valid += tickStats.Valid
		stats.Skipped += tickStats.Skipped
	}
	if len(futuresMarkets) > 0 {
		tickers, tickStats, err := a.fetchFuturesTickers(ctx, futuresMarkets)
		if err != nil {
			return nil, stats, fmt.Errorf("получить Gate USDT futures tickers: %w", err)
		}
		result = append(result, tickers...)
		stats.Received += tickStats.Received
		stats.Valid += tickStats.Valid
		stats.Skipped += tickStats.Skipped
	}

	a.logger.Info("Gate tickers fetched", "received", stats.Received, "valid", stats.Valid, "skipped", stats.Skipped)
	return result, stats, nil
}

func (a *Adapter) fetchSpotTickers(
	ctx context.Context,
	requested map[string]market.Market,
) ([]price.Ticker, TickerStats, error) {
	a.mu.Lock()
	if a.hasSpotTickerCache {
		response := append([]spotTicker(nil), a.spotTickerCache...)
		receivedAt := a.spotTickerReceivedAt
		a.spotTickerCache = nil
		a.spotTickerReceivedAt = time.Time{}
		a.hasSpotTickerCache = false
		a.mu.Unlock()
		if len(response) == 0 || receivedAt.IsZero() {
			return nil, TickerStats{}, fmt.Errorf("Gate Spot ticker cache пуст или не содержит времени получения")
		}
		return buildSpotTickers(response, requested, receivedAt)
	}
	a.mu.Unlock()

	var response []spotTicker
	receivedAt, err := a.client.getJSON(ctx, "получить Spot tickers", spotTickersEndpoint, &response)
	if err != nil {
		return nil, TickerStats{}, err
	}
	if response == nil || len(response) == 0 {
		return nil, TickerStats{}, fmt.Errorf("Gate Spot tickers вернул пустой список")
	}
	return buildSpotTickers(response, requested, receivedAt)
}

func (a *Adapter) fetchFuturesTickers(
	ctx context.Context,
	requested map[string]market.Market,
) ([]price.Ticker, TickerStats, error) {
	var response []futuresTicker
	receivedAt, err := a.client.getJSON(ctx, "получить USDT futures tickers", futuresTickersEndpoint, &response)
	if err != nil {
		return nil, TickerStats{}, err
	}
	if response == nil || len(response) == 0 {
		return nil, TickerStats{}, fmt.Errorf("Gate futures/usdt/tickers вернул пустой список")
	}
	return buildFuturesTickers(response, requested, receivedAt)
}

func buildSpotTickers(
	response []spotTicker,
	requested map[string]market.Market,
	receivedAt time.Time,
) ([]price.Ticker, TickerStats, error) {
	stats := TickerStats{Received: len(response)}
	result := make([]price.Ticker, 0, len(requested))
	seen := make(map[string]struct{}, len(requested))
	for _, item := range response {
		symbol := strings.ToUpper(strings.TrimSpace(item.CurrencyPair))
		m, ok := requested[symbol]
		if symbol == "" || !ok {
			stats.Skipped++
			continue
		}
		if _, duplicate := seen[symbol]; duplicate {
			stats.Skipped++
			continue
		}
		seen[symbol] = struct{}{}

		// Gate отдаёт поля ETF только для ETF-пар; такие тикеры не смешиваются с обычным Spot.
		if isSpecialETF(item) {
			stats.Skipped++
			continue
		}
		bid, err := parsePositiveDecimal(item.HighestBid)
		if err != nil {
			stats.Skipped++
			continue
		}
		ask, err := parsePositiveDecimal(item.LowestAsk)
		if err != nil || ask.LessThan(bid) {
			stats.Skipped++
			continue
		}

		volume, volumeErr := parseOptionalVolume(item.BaseVolume)
		if volumeErr != nil {
			stats.Skipped++
			continue
		}
		ticker := price.Ticker{
			Exchange: "gate", Symbol: m.Symbol,
			MarketType: market.MarketTypeSpot, ContractType: market.ContractTypeSpot,
			Bid: bid, Ask: ask, Volume24h: volume,
			Timestamp: receivedAt, ReceivedAt: receivedAt,
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

func buildFuturesTickers(
	response []futuresTicker,
	requested map[string]market.Market,
	receivedAt time.Time,
) ([]price.Ticker, TickerStats, error) {
	stats := TickerStats{Received: len(response)}
	result := make([]price.Ticker, 0, len(requested))
	seen := make(map[string]struct{}, len(requested))
	for _, item := range response {
		symbol := strings.ToUpper(strings.TrimSpace(item.Contract))
		m, ok := requested[symbol]
		if symbol == "" || !ok {
			stats.Skipped++
			continue
		}
		if _, duplicate := seen[symbol]; duplicate {
			stats.Skipped++
			continue
		}
		seen[symbol] = struct{}{}

		bid, err := parsePositiveDecimal(item.HighestBid)
		if err != nil {
			stats.Skipped++
			continue
		}
		ask, err := parsePositiveDecimal(item.LowestAsk)
		if err != nil || ask.LessThan(bid) {
			stats.Skipped++
			continue
		}

		volume, volumeErr := parseOptionalVolume(item.Volume24hBase)
		if volumeErr != nil {
			stats.Skipped++
			continue
		}
		ticker := price.Ticker{
			Exchange: "gate", Symbol: m.Symbol,
			MarketType: market.MarketTypeFutures, ContractType: market.ContractTypePerpetual,
			Bid: bid, Ask: ask, Volume24h: volume,
			Timestamp: receivedAt, ReceivedAt: receivedAt,
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

func parsePositiveDecimal(raw json.RawMessage) (decimal.Decimal, error) {
	value, present, err := parseDecimal(raw)
	if err != nil {
		return decimal.Zero, err
	}
	if !present {
		return decimal.Zero, fmt.Errorf("цена отсутствует")
	}
	result, err := decimal.NewFromString(value)
	if err != nil {
		return decimal.Zero, err
	}
	if !result.GreaterThan(decimal.Zero) {
		return decimal.Zero, fmt.Errorf("цена должна быть положительной")
	}
	return result, nil
}

func parseOptionalVolume(raw json.RawMessage) (*decimal.Decimal, error) {
	value, present, err := parseDecimal(raw)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, nil
	}
	result, err := decimal.NewFromString(value)
	if err != nil {
		return nil, err
	}
	if result.IsNegative() {
		return nil, fmt.Errorf("объём не может быть отрицательным")
	}
	return &result, nil
}

func isSpecialETF(item spotTicker) bool {
	for _, raw := range []json.RawMessage{item.ETFNetValue, item.ETFPreNetValue, item.ETFLeverage} {
		value, present, err := parseDecimal(raw)
		if err == nil && present {
			parsed, parseErr := decimal.NewFromString(value)
			if parseErr == nil && parsed.GreaterThan(decimal.Zero) {
				return true
			}
		}
	}
	return false
}
