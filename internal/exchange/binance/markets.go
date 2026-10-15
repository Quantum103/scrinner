package binance

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/example/crypto-arbitrage-scanner/internal/market"
)

// discoverMarkets получает только активные Spot USDT и USDⓈ-M USDT Perpetual.
func (a *Adapter) discoverMarkets(ctx context.Context) ([]market.Market, error) {
	var spotResponse spotExchangeInfoResponse
	if _, err := a.client.getJSON(
		ctx,
		false,
		"получить список Spot инструментов",
		spotExchangeInfoEndpoint,
		url.Values{},
		&spotResponse,
	); err != nil {
		return nil, fmt.Errorf("получить Binance Spot exchangeInfo: %w", err)
	}
	if spotResponse.Symbols == nil {
		return nil, fmt.Errorf("Binance Spot exchangeInfo: ответ не содержит поле symbols")
	}

	var futuresResponse futuresExchangeInfoResponse
	if _, err := a.client.getJSON(
		ctx,
		true,
		"получить список USD-M Futures инструментов",
		futuresExchangeInfoEndpoint,
		url.Values{},
		&futuresResponse,
	); err != nil {
		return nil, fmt.Errorf("получить Binance USD-M Futures exchangeInfo: %w", err)
	}
	if futuresResponse.Symbols == nil {
		return nil, fmt.Errorf("Binance USD-M Futures exchangeInfo: ответ не содержит поле symbols")
	}

	markets := make([]market.Market, 0, len(spotResponse.Symbols)+len(futuresResponse.Symbols))
	seen := make(map[string]struct{}, len(markets))
	spotCount := 0
	perpetualCount := 0

	for _, instrument := range spotResponse.Symbols {
		if !isSpotUSDTCandidate(instrument) {
			continue
		}
		if instrument.IsSpotTradingAllowed == nil {
			return nil, fmt.Errorf(
				"Binance Spot exchangeInfo: отсутствует isSpotTradingAllowed для активного USDT-инструмента %q",
				instrument.Symbol,
			)
		}
		if !*instrument.IsSpotTradingAllowed {
			continue
		}

		m, err := convertSpotInstrument(instrument)
		if err != nil {
			return nil, err
		}
		key := internalMarketKey(m)
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("Binance Spot exchangeInfo: повторный инструмент %q", m.Symbol)
		}
		seen[key] = struct{}{}
		markets = append(markets, m)
		spotCount++
	}

	for _, instrument := range futuresResponse.Symbols {
		if !isUSDTPerpetualCandidate(instrument) {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(instrument.MarginAsset), usdtAsset) {
			continue
		}

		m, err := convertFuturesInstrument(instrument)
		if err != nil {
			return nil, err
		}
		key := internalMarketKey(m)
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("Binance USD-M Futures exchangeInfo: повторный инструмент %q", m.Symbol)
		}
		seen[key] = struct{}{}
		markets = append(markets, m)
		perpetualCount++
	}

	a.logger.Info(
		"Binance markets fetched",
		"spot_usdt", spotCount,
		"perpetual_usdt", perpetualCount,
		"total", len(markets),
	)
	return markets, nil
}

func isSpotUSDTCandidate(instrument spotSymbol) bool {
	return instrument.Status == tradingStatus &&
		strings.EqualFold(strings.TrimSpace(instrument.QuoteAsset), usdtAsset)
}

func isUSDTPerpetualCandidate(instrument futuresSymbol) bool {
	return instrument.Status == tradingStatus &&
		instrument.ContractType == perpetualType &&
		strings.EqualFold(strings.TrimSpace(instrument.QuoteAsset), usdtAsset)
}

// convertSpotInstrument переводит структурированные поля Spot инструмента во внутренний Market.
func convertSpotInstrument(instrument spotSymbol) (market.Market, error) {
	if !isSpotUSDTCandidate(instrument) ||
		instrument.IsSpotTradingAllowed == nil ||
		!*instrument.IsSpotTradingAllowed {
		return market.Market{}, fmt.Errorf(
			"Binance Spot instrument %q не является активным Spot USDT",
			instrument.Symbol,
		)
	}
	if strings.TrimSpace(instrument.Symbol) == "" ||
		strings.TrimSpace(instrument.BaseAsset) == "" ||
		strings.TrimSpace(instrument.QuoteAsset) == "" {
		return market.Market{}, fmt.Errorf(
			"Binance Spot exchangeInfo: отсутствуют обязательные поля symbol/baseAsset/quoteAsset",
		)
	}

	symbol := market.NewSymbol(instrument.BaseAsset, instrument.QuoteAsset)
	result := market.Market{
		Exchange:     "binance",
		Symbol:       strings.TrimSpace(instrument.Symbol),
		BaseAsset:    symbol.BaseAsset,
		QuoteAsset:   symbol.QuoteAsset,
		MarketType:   market.MarketTypeSpot,
		ContractType: market.ContractTypeSpot,
		Active:       true,
	}
	normalized, err := market.NormalizeMarket(result)
	if err != nil {
		return market.Market{}, fmt.Errorf(
			"нормализовать Binance Spot рынок %q: %w",
			instrument.Symbol,
			err,
		)
	}
	return normalized, nil
}

// convertFuturesInstrument переводит только бессрочный USDⓈ-M контракт во внутренний Market.
func convertFuturesInstrument(instrument futuresSymbol) (market.Market, error) {
	if !isUSDTPerpetualCandidate(instrument) ||
		!strings.EqualFold(strings.TrimSpace(instrument.MarginAsset), usdtAsset) {
		return market.Market{}, fmt.Errorf(
			"Binance Futures instrument %q не является USDT-margined Perpetual",
			instrument.Symbol,
		)
	}
	if strings.TrimSpace(instrument.Symbol) == "" ||
		strings.TrimSpace(instrument.BaseAsset) == "" ||
		strings.TrimSpace(instrument.QuoteAsset) == "" {
		return market.Market{}, fmt.Errorf(
			"Binance USD-M Futures exchangeInfo: отсутствуют обязательные поля symbol/baseAsset/quoteAsset",
		)
	}

	symbol := market.NewSymbol(instrument.BaseAsset, instrument.QuoteAsset)
	result := market.Market{
		Exchange:     "binance",
		Symbol:       strings.TrimSpace(instrument.Symbol),
		BaseAsset:    symbol.BaseAsset,
		QuoteAsset:   symbol.QuoteAsset,
		MarketType:   market.MarketTypeFutures,
		ContractType: market.ContractTypePerpetual,
		Active:       true,
	}
	normalized, err := market.NormalizeMarket(result)
	if err != nil {
		return market.Market{}, fmt.Errorf(
			"нормализовать Binance Perpetual рынок %q: %w",
			instrument.Symbol,
			err,
		)
	}
	return normalized, nil
}

func internalMarketKey(m market.Market) string {
	return strings.ToUpper(strings.TrimSpace(m.Exchange)) + "|" +
		strings.ToUpper(strings.TrimSpace(m.Symbol)) + "|" +
		string(m.MarketType) + "|" +
		string(m.ContractType)
}
