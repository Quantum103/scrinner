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

// fetchInstruments получает полный список инструментов категории.
func (a *Adapter) fetchInstruments(ctx context.Context, category string) ([]bybitInstrument, error) {
	var all []bybitInstrument
	cursor := ""

	for page := 1; ; page++ {
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
		if envelope.Time <= 0 {
			return nil, fmt.Errorf("exchange=bybit operation=get instruments %s: API вернул некорректный time=%d", category, envelope.Time)
		}

		all = append(all, result.List...)

		if category == spotCategory {
			break
		}

		nextCursor := strings.TrimSpace(result.NextPageCursor)
		if nextCursor == "" {
			break
		}
		if nextCursor == cursor {
			return nil, fmt.Errorf("exchange=bybit operation=get instruments %s: pagination cursor не изменился на странице %d", category, page)
		}
		cursor = nextCursor
	}

	return all, nil
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
	symbol := market.NewSymbol(instrument.BaseCoin, instrument.QuoteCoin)
	if !symbol.IsValid() || symbol.QuoteAsset != usdtAsset || symbol.BaseAsset == usdtAsset {
		return market.Market{}, fmt.Errorf("exchange=bybit operation=convert spot instrument symbol=%q: некорректные активы %q/%q", instrument.Symbol, instrument.BaseCoin, instrument.QuoteCoin)
	}

	return market.Market{
		Exchange:     "bybit",
		Symbol:       instrument.Symbol,
		BaseAsset:    symbol.BaseAsset,
		QuoteAsset:   symbol.QuoteAsset,
		MarketType:   market.MarketTypeSpot,
		ContractType: market.ContractTypeSpot,
		Active:       true,
	}, nil
}

// convertLinearPerpetualInstrument преобразует Bybit LinearPerpetual во внутренний Market.
func convertLinearPerpetualInstrument(instrument bybitInstrument) (market.Market, error) {
	symbol := market.NewSymbol(instrument.BaseCoin, instrument.QuoteCoin)
	if !symbol.IsValid() || symbol.QuoteAsset != usdtAsset || symbol.BaseAsset == usdtAsset {
		return market.Market{}, fmt.Errorf("exchange=bybit operation=convert linear instrument symbol=%q: некорректные активы %q/%q", instrument.Symbol, instrument.BaseCoin, instrument.QuoteCoin)
	}

	return market.Market{
		Exchange:     "bybit",
		Symbol:       instrument.Symbol,
		BaseAsset:    symbol.BaseAsset,
		QuoteAsset:   symbol.QuoteAsset,
		MarketType:   market.MarketTypeFutures,
		ContractType: market.ContractTypePerpetual,
		Active:       true,
	}, nil
}
