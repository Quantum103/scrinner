package bybit

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/example/crypto-arbitrage-scanner/internal/market"
)

// discoverMarkets получает и фильтрует только нужные Spot и Linear Perpetual рынки.
func (a *Adapter) discoverMarkets(ctx context.Context) ([]market.Market, error) {
	a.logger.Info("fetching Bybit spot instruments")
	spotInstruments, err := a.fetchInstruments(ctx, spotCategory)
	if err != nil {
		return nil, fmt.Errorf("получить Bybit Spot instruments: %w", err)
	}

	a.logger.Info("fetching Bybit linear instruments")
	linearInstruments, err := a.fetchInstruments(ctx, linearCategory)
	if err != nil {
		return nil, fmt.Errorf("получить Bybit Linear instruments: %w", err)
	}

	markets := make([]market.Market, 0, len(spotInstruments)+len(linearInstruments))
	spotCount := 0
	perpetualCount := 0

	for _, instrument := range spotInstruments {
		if !isUSDTSpotInstrument(instrument) {
			continue
		}
		m, err := convertSpotInstrument(instrument)
		if err != nil {
			return nil, err
		}
		markets = append(markets, m)
		spotCount++
	}

	for _, instrument := range linearInstruments {
		if !isUSDTLinearPerpetual(instrument) {
			continue
		}
		m, err := convertLinearPerpetualInstrument(instrument)
		if err != nil {
			return nil, err
		}
		markets = append(markets, m)
		perpetualCount++
	}

	a.logger.Info("Bybit markets fetched", "spot_usdt", spotCount, "perpetual_usdt", perpetualCount, "total", len(markets))
	return markets, nil
}

// fetchInstruments получает полный список инструментов категории с защитой от зацикливания pagination.
func (a *Adapter) fetchInstruments(ctx context.Context, category string) ([]bybitInstrument, error) {
	all := make([]bybitInstrument, 0)
	cursor := ""
	seenCursors := make(map[string]struct{})

	for page := 1; page <= maxInstrumentPages; page++ {
		query := url.Values{}
		query.Set("category", category)
		query.Set("status", tradingStatus)

		if category == linearCategory {
			query.Set("limit", "1000")
			if cursor != "" {
				query.Set("cursor", cursor)
			}
		}

		var result instrumentsResult
		envelope, err := a.client.getJSON(ctx, "get instruments "+category, instrumentEndpoint, query, &result)
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(strings.TrimSpace(result.Category), category) {
			return nil, fmt.Errorf("exchange=bybit operation=get instruments %s: API вернул category=%q", category, result.Category)
		}
		if result.List == nil {
			return nil, fmt.Errorf("exchange=bybit operation=get instruments %s: ответ не содержит result.list", category)
		}
		if envelope.Time <= 0 {
			return nil, fmt.Errorf("exchange=bybit operation=get instruments %s: API вернул некорректный time=%d", category, envelope.Time)
		}

		all = append(all, result.List...)

		if category == spotCategory {
			return all, nil
		}

		nextCursor := strings.TrimSpace(result.NextPageCursor)
		if nextCursor == "" {
			return all, nil
		}
		if _, exists := seenCursors[nextCursor]; exists {
			return nil, fmt.Errorf(
				"exchange=bybit operation=get instruments %s: pagination повторно вернула cursor на странице %d",
				category,
				page,
			)
		}
		seenCursors[nextCursor] = struct{}{}
		cursor = nextCursor
	}

	return nil, fmt.Errorf(
		"exchange=bybit operation=get instruments %s: превышен лимит pagination в %d страниц",
		category,
		maxInstrumentPages,
	)
}

// isUSDTSpotInstrument проверяет Spot по структурированным полям API.
func isUSDTSpotInstrument(instrument bybitInstrument) bool {
	return instrument.Status == tradingStatus &&
		strings.EqualFold(strings.TrimSpace(instrument.QuoteCoin), usdtAsset) &&
		strings.TrimSpace(instrument.Symbol) != "" &&
		strings.TrimSpace(instrument.BaseCoin) != ""
}

// isUSDTLinearPerpetual проверяет именно USDT-margined perpetual по API-полям.
func isUSDTLinearPerpetual(instrument bybitInstrument) bool {
	return instrument.Status == tradingStatus &&
		instrument.ContractType == linearPerpetual &&
		strings.EqualFold(strings.TrimSpace(instrument.QuoteCoin), usdtAsset) &&
		strings.EqualFold(strings.TrimSpace(instrument.SettleCoin), usdtAsset) &&
		strings.TrimSpace(instrument.Symbol) != "" &&
		strings.TrimSpace(instrument.BaseCoin) != ""
}

// convertSpotInstrument преобразует структурированную Spot запись Bybit во внутренний Market.
func convertSpotInstrument(instrument bybitInstrument) (market.Market, error) {
	if !isUSDTSpotInstrument(instrument) {
		return market.Market{}, fmt.Errorf(
			"exchange=bybit operation=convert spot instrument symbol=%q: инструмент не является активным Spot USDT",
			instrument.Symbol,
		)
	}

	symbol := market.NewSymbol(instrument.BaseCoin, instrument.QuoteCoin)
	result := market.Market{
		Exchange:     "bybit",
		Symbol:       instrument.Symbol,
		BaseAsset:    symbol.BaseAsset,
		QuoteAsset:   symbol.QuoteAsset,
		MarketType:   market.MarketTypeSpot,
		ContractType: market.ContractTypeSpot,
		Active:       true,
	}

	normalized, err := market.NormalizeMarket(result)
	if err != nil {
		return market.Market{}, fmt.Errorf(
			"exchange=bybit operation=normalize spot market symbol=%q: %w",
			instrument.Symbol,
			err,
		)
	}
	return normalized, nil
}

// convertLinearPerpetualInstrument преобразует Bybit LinearPerpetual во внутренний Market.
func convertLinearPerpetualInstrument(instrument bybitInstrument) (market.Market, error) {
	if !isUSDTLinearPerpetual(instrument) {
		return market.Market{}, fmt.Errorf(
			"exchange=bybit operation=convert linear instrument symbol=%q: инструмент не является активным USDT-margined perpetual",
			instrument.Symbol,
		)
	}

	symbol := market.NewSymbol(instrument.BaseCoin, instrument.QuoteCoin)
	result := market.Market{
		Exchange:     "bybit",
		Symbol:       instrument.Symbol,
		BaseAsset:    symbol.BaseAsset,
		QuoteAsset:   symbol.QuoteAsset,
		MarketType:   market.MarketTypeFutures,
		ContractType: market.ContractTypePerpetual,
		Active:       true,
	}

	normalized, err := market.NormalizeMarket(result)
	if err != nil {
		return market.Market{}, fmt.Errorf(
			"exchange=bybit operation=normalize linear market symbol=%q: %w",
			instrument.Symbol,
			err,
		)
	}
	return normalized, nil
}
