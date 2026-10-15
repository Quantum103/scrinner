package okx

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/example/crypto-arbitrage-scanner/internal/market"
)

// discoverMarkets получает список активных Spot USDT и линейных USDT SWAP-рынков.
func (a *Adapter) discoverMarkets(ctx context.Context) ([]market.Market, error) {
	var spotInstruments []spotInstrument
	if _, err := a.client.getJSON(
		ctx,
		"получить список Spot-инструментов",
		publicInstrumentsEndpoint,
		url.Values{"instType": []string{"SPOT"}},
		&spotInstruments,
	); err != nil {
		return nil, fmt.Errorf("получить OKX Spot instruments: %w", err)
	}
	if len(spotInstruments) == 0 {
		return nil, fmt.Errorf("OKX Spot instruments вернул пустой список")
	}

	var swapInstruments []swapInstrument
	if _, err := a.client.getJSON(
		ctx,
		"получить список SWAP-контрактов",
		publicInstrumentsEndpoint,
		url.Values{"instType": []string{"SWAP"}},
		&swapInstruments,
	); err != nil {
		return nil, fmt.Errorf("получить OKX SWAP instruments: %w", err)
	}
	if len(swapInstruments) == 0 {
		return nil, fmt.Errorf("OKX SWAP instruments вернул пустой список")
	}

	markets := make([]market.Market, 0, len(spotInstruments)+len(swapInstruments))
	seen := make(map[string]struct{}, len(markets))
	spotCount, swapCount := 0, 0

	for _, instrument := range spotInstruments {
		if !strings.EqualFold(strings.TrimSpace(instrument.InstType), "SPOT") ||
			!strings.EqualFold(strings.TrimSpace(instrument.QuoteCcy), usdtAsset) {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(instrument.State), "live") {
			if strings.TrimSpace(instrument.State) == "" {
				a.logger.Warn("OKX Spot instrument пропущен: отсутствует state", "instId", instrument.InstID)
			}
			continue
		}
		converted, err := convertSpotInstrument(instrument)
		if err != nil {
			a.logger.Warn("OKX Spot instrument пропущен", "instId", instrument.InstID, "error", err)
			continue
		}
		key := internalMarketKey(converted)
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("OKX instruments содержит повторный рынок %q", converted.Symbol)
		}
		seen[key] = struct{}{}
		markets = append(markets, converted)
		spotCount++
	}

	for _, instrument := range swapInstruments {
		if !strings.EqualFold(strings.TrimSpace(instrument.InstType), "SWAP") {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(instrument.SettleCcy), usdtAsset) {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(instrument.State), "live") {
			if strings.TrimSpace(instrument.State) == "" {
				a.logger.Warn("OKX SWAP instrument пропущен: отсутствует state", "instId", instrument.InstID)
			}
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(instrument.CtType), "linear") {
			if strings.TrimSpace(instrument.CtType) == "" {
				a.logger.Warn("OKX SWAP instrument пропущен: отсутствует ctType", "instId", instrument.InstID)
			}
			continue
		}
		converted, err := convertSwapInstrument(instrument)
		if err != nil {
			a.logger.Warn("OKX SWAP instrument пропущен", "instId", instrument.InstID, "error", err)
			continue
		}
		key := internalMarketKey(converted)
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("OKX instruments содержит повторный рынок %q", converted.Symbol)
		}
		seen[key] = struct{}{}
		markets = append(markets, converted)
		swapCount++
	}

	if len(markets) == 0 {
		return nil, fmt.Errorf("OKX instruments не вернул поддерживаемых активных USDT-рынков")
	}

	a.logger.Info(
		"OKX markets fetched",
		"spot_usdt", spotCount,
		"linear_usdt_swap", swapCount,
		"total", len(markets),
	)
	return markets, nil
}

// convertSpotInstrument преобразует метаданные Spot-инструмента во внутреннюю модель.
func convertSpotInstrument(instrument spotInstrument) (market.Market, error) {
	base := strings.ToUpper(strings.TrimSpace(instrument.BaseCcy))
	quote := strings.ToUpper(strings.TrimSpace(instrument.QuoteCcy))
	instID := strings.TrimSpace(instrument.InstID)
	if !strings.EqualFold(strings.TrimSpace(instrument.InstType), "SPOT") ||
		!strings.EqualFold(strings.TrimSpace(instrument.State), "live") ||
		base == "" || quote != usdtAsset || instID == "" {
		return market.Market{}, fmt.Errorf("метаданные не соответствуют активной Spot USDT-паре")
	}
	// Сверяем идентификатор с официальными полями актива, но не определяем рынок по одной строке.
	if !strings.EqualFold(instID, base+"-"+quote) {
		return market.Market{}, fmt.Errorf("instId не согласован с baseCcy/quoteCcy: %q", instID)
	}

	symbol := market.NewSymbol(base, quote)
	normalized, err := market.NormalizeMarket(market.Market{
		Exchange:     "okx",
		Symbol:       instID,
		BaseAsset:    symbol.BaseAsset,
		QuoteAsset:   symbol.QuoteAsset,
		MarketType:   market.MarketTypeSpot,
		ContractType: market.ContractTypeSpot,
		Active:       true,
	})
	if err != nil {
		return market.Market{}, fmt.Errorf("нормализовать OKX Spot %q: %w", instID, err)
	}
	return normalized, nil
}

// convertSwapInstrument проверяет линейный тип, расчётную валюту и структурированные поля SWAP.
func convertSwapInstrument(instrument swapInstrument) (market.Market, error) {
	instID := strings.TrimSpace(instrument.InstID)
	if !strings.EqualFold(strings.TrimSpace(instrument.InstType), "SWAP") ||
		!strings.EqualFold(strings.TrimSpace(instrument.CtType), "linear") ||
		!strings.EqualFold(strings.TrimSpace(instrument.State), "live") ||
		!strings.EqualFold(strings.TrimSpace(instrument.SettleCcy), usdtAsset) ||
		instID == "" {
		return market.Market{}, fmt.Errorf("метаданные не соответствуют активному линейному USDT SWAP")
	}
	if quote := strings.TrimSpace(instrument.QuoteCcy); quote != "" &&
		!strings.EqualFold(quote, usdtAsset) {
		return market.Market{}, fmt.Errorf("quoteCcy=%q не соответствует USDT", quote)
	}

	metadataBases := make([]string, 0, 4)
	if value := strings.TrimSpace(instrument.BaseCcy); value != "" {
		metadataBases = append(metadataBases, value)
	}
	if value := strings.TrimSpace(instrument.CtValCcy); value != "" {
		metadataBases = append(metadataBases, value)
	}
	for _, family := range []string{instrument.Uly, instrument.InstFamily} {
		family = strings.TrimSpace(family)
		if family == "" {
			continue
		}
		parts := strings.Split(family, "-")
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" ||
			!strings.EqualFold(strings.TrimSpace(parts[1]), usdtAsset) {
			return market.Market{}, fmt.Errorf("метаданные underlying %q не указывают пару с USDT", family)
		}
		metadataBases = append(metadataBases, strings.TrimSpace(parts[0]))
	}
	if len(metadataBases) == 0 {
		return market.Market{}, fmt.Errorf("не удалось определить базовый актив из метаданных")
	}

	base := strings.ToUpper(metadataBases[0])
	for _, candidate := range metadataBases[1:] {
		if !strings.EqualFold(strings.TrimSpace(candidate), base) {
			return market.Market{}, fmt.Errorf("противоречивые поля базового актива: %q и %q", base, candidate)
		}
	}
	parts := strings.Split(instID, "-")
	if len(parts) != 3 || !strings.EqualFold(parts[0], base) ||
		!strings.EqualFold(parts[1], usdtAsset) ||
		!strings.EqualFold(parts[2], "SWAP") {
		return market.Market{}, fmt.Errorf("instId не согласован с метаданными линейного USDT SWAP: %q", instID)
	}

	symbol := market.NewSymbol(base, usdtAsset)
	normalized, err := market.NormalizeMarket(market.Market{
		Exchange:     "okx",
		Symbol:       instID,
		BaseAsset:    symbol.BaseAsset,
		QuoteAsset:   symbol.QuoteAsset,
		MarketType:   market.MarketTypeFutures,
		ContractType: market.ContractTypePerpetual,
		Active:       true,
	})
	if err != nil {
		return market.Market{}, fmt.Errorf("нормализовать OKX SWAP %q: %w", instID, err)
	}
	return normalized, nil
}

func internalMarketKey(m market.Market) string {
	return strings.ToUpper(strings.TrimSpace(m.Exchange)) + "|" +
		strings.ToUpper(strings.TrimSpace(m.Symbol)) + "|" +
		string(m.MarketType) + "|" +
		string(m.ContractType)
}
