package bitget

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/example/crypto-arbitrage-scanner/internal/market"
	"github.com/example/crypto-arbitrage-scanner/internal/price"
	"github.com/shopspring/decimal"
)

func (a *Adapter) fetchTickers(
	ctx context.Context,
	category string,
	requested map[string]market.Market,
	marketType market.MarketType,
	contractType market.ContractType,
) ([]price.Ticker, TickerStats, error) {
	var response []ticker
	receivedAt, err := a.client.getJSON(ctx, "получить пакетные котировки", tickersEndpoint, category, &response)
	if err != nil {
		return nil, TickerStats{}, err
	}
	if len(response) == 0 {
		return nil, TickerStats{}, fmtEmptyResponse(tickersEndpoint, category)
	}
	stats := TickerStats{Received: len(response)}
	result := make([]price.Ticker, 0, len(requested))
	seen := make(map[string]struct{}, len(requested))
	for _, item := range response {
		symbol := strings.ToUpper(strings.TrimSpace(item.Symbol))
		requestedMarket, ok := requested[symbol]
		if symbol == "" || !ok || !strings.EqualFold(strings.TrimSpace(item.Category), category) {
			stats.Skipped++
			continue
		}
		if _, duplicate := seen[symbol]; duplicate {
			stats.Skipped++
			continue
		}
		bid, err := parsePositiveDecimal(item.Bid1Price)
		if err != nil {
			stats.Skipped++
			continue
		}
		ask, err := parsePositiveDecimal(item.Ask1Price)
		if err != nil || ask.LessThan(bid) {
			stats.Skipped++
			continue
		}
		exchangeTime, err := parseTimestampMilli(item.TimestampMilli)
		if err != nil {
			stats.Skipped++
			continue
		}
		volume, err := parseOptionalVolume(item.Volume24h)
		if err != nil {
			stats.Skipped++
			continue
		}

		ticker := price.Ticker{
			Exchange:          stringExchangeBitget,
			Symbol:            requestedMarket.Symbol,
			MarketType:        marketType,
			ContractType:      contractType,
			Bid:               bid,
			Ask:               ask,
			Volume24h:         volume,
			Timestamp:         exchangeTime,
			ReceivedAt:        receivedAt,
			ExchangeTimestamp: &exchangeTime,
		}
		if !ticker.IsValid() {
			stats.Skipped++
			continue
		}
		seen[symbol] = struct{}{}
		result = append(result, ticker)
	}
	stats.Valid = len(result)
	stats.Skipped = stats.Received - stats.Valid
	return result, stats, nil
}

func parsePositiveDecimal(raw json.RawMessage) (decimal.Decimal, error) {
	text, present, err := decimalText(raw)
	if err != nil {
		return decimal.Zero, err
	}
	if !present {
		return decimal.Zero, fmt.Errorf("цена отсутствует")
	}
	value, err := decimal.NewFromString(text)
	if err != nil {
		return decimal.Zero, fmt.Errorf("некорректная decimal-цена %q: %w", text, err)
	}
	if !value.GreaterThan(decimal.Zero) {
		return decimal.Zero, fmt.Errorf("цена должна быть положительной")
	}
	return value, nil
}

func parseOptionalVolume(raw json.RawMessage) (*decimal.Decimal, error) {
	text, present, err := decimalText(raw)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, nil
	}
	value, err := decimal.NewFromString(text)
	if err != nil {
		return nil, fmt.Errorf("некорректный decimal-объём %q: %w", text, err)
	}
	if value.IsNegative() {
		return nil, fmt.Errorf("объём не может быть отрицательным")
	}
	return &value, nil
}

func decimalText(raw json.RawMessage) (string, bool, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || strings.EqualFold(trimmed, "null") {
		return "", false, nil
	}
	if strings.HasPrefix(trimmed, "\"") {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return "", false, err
		}
		if strings.TrimSpace(value) == "" {
			return "", false, nil
		}
		return value, true, nil
	}
	return trimmed, true, nil
}

func parseTimestampMilli(raw json.RawMessage) (time.Time, error) {
	text, present, err := decimalText(raw)
	if err != nil {
		return time.Time{}, err
	}
	if !present {
		return time.Time{}, fmt.Errorf("временная метка ts отсутствует")
	}
	millis, err := strconv.ParseInt(text, 10, 64)
	if err != nil || millis <= 0 {
		return time.Time{}, fmt.Errorf("некорректная Unix-временная метка в миллисекундах %q", text)
	}
	result := time.UnixMilli(millis).UTC()
	if result.Year() < 1970 || result.Year() > 9999 {
		return time.Time{}, fmt.Errorf("временная метка ts вне допустимого диапазона: %q", text)
	}
	return result, nil
}
