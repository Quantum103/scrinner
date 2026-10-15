package mexc

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/example/crypto-arbitrage-scanner/internal/market"
	"github.com/shopspring/decimal"
)

// discoverMarkets получает только активные Spot USDT и USDT Perpetual рынки.
func (a *Adapter) discoverMarkets(ctx context.Context) ([]market.Market, error) {
	var spotResponse spotExchangeInfoResponse
	if _, err := a.client.getJSON(
		ctx,
		false,
		"получить список Spot-рынков",
		spotExchangeInfoEndpoint,
		url.Values{},
		&spotResponse,
	); err != nil {
		return nil, fmt.Errorf("получить MEXC Spot exchangeInfo: %w", err)
	}
	if spotResponse.Symbols == nil {
		return nil, fmt.Errorf("MEXC Spot exchangeInfo: ответ не содержит массив symbols")
	}

	var contracts futuresContractList
	if _, err := a.client.getJSON(
		ctx,
		true,
		"получить список Futures-контрактов",
		futuresContractInfoEndpoint,
		url.Values{},
		&contracts,
	); err != nil {
		return nil, fmt.Errorf("получить MEXC Futures contract info: %w", err)
	}
	if contracts == nil {
		return nil, fmt.Errorf("MEXC Futures contract info: ответ не содержит data со списком контрактов")
	}

	markets := make([]market.Market, 0, len(spotResponse.Symbols)+len(contracts))
	seen := make(map[string]struct{}, len(markets))
	spotCount := 0
	perpetualCount := 0
	contractSizes := make(map[string]decimal.Decimal, len(contracts))

	for _, instrument := range spotResponse.Symbols {
		if !strings.EqualFold(strings.TrimSpace(instrument.QuoteAsset), usdtAsset) {
			continue
		}
		status, statusPresent, err := parseIntegerRaw(instrument.Status)
		if err != nil {
			return nil, fmt.Errorf("MEXC Spot %q: некорректный status: %w", instrument.Symbol, err)
		}
		if !statusPresent || status != spotStatusOnline {
			continue
		}
		if strings.TrimSpace(instrument.Symbol) == "" || strings.TrimSpace(instrument.BaseAsset) == "" {
			return nil, fmt.Errorf("MEXC Spot exchangeInfo: у активной USDT-пары отсутствует symbol или baseAsset")
		}
		if instrument.IsSpotTradingAllowed == nil {
			return nil, fmt.Errorf(
				"MEXC Spot exchangeInfo: отсутствует isSpotTradingAllowed для активной USDT-пары %q",
				instrument.Symbol,
			)
		}
		if !*instrument.IsSpotTradingAllowed {
			continue
		}
		if len(instrument.Permissions) > 0 && !containsPermission(instrument.Permissions, "SPOT") {
			continue
		}
		if sideType, present, parseErr := parseIntegerRaw(instrument.TradeSideType); parseErr != nil {
			return nil, fmt.Errorf("MEXC Spot %q: некорректный tradeSideType: %w", instrument.Symbol, parseErr)
		} else if present && sideType != 1 {
			// Для арбитража нужны рынки, где доступны как покупка, так и продажа.
			continue
		}

		m, err := convertSpotInstrument(instrument)
		if err != nil {
			return nil, err
		}
		key := internalMarketKey(m)
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("MEXC Spot exchangeInfo: повторный инструмент %q", m.Symbol)
		}
		seen[key] = struct{}{}
		markets = append(markets, m)
		spotCount++
	}

	for _, contract := range contracts {
		if !strings.EqualFold(strings.TrimSpace(contract.QuoteCoin), usdtAsset) ||
			!strings.EqualFold(strings.TrimSpace(contract.SettleCoin), usdtAsset) {
			continue
		}
		if (contract.APIAllowed != nil && !*contract.APIAllowed) ||
			(contract.IsHidden != nil && *contract.IsHidden) {
			continue
		}

		futureType, typePresent, err := parseIntegerRaw(contract.FutureType)
		if err != nil {
			return nil, fmt.Errorf("MEXC Futures %q: некорректный futureType: %w", contract.Symbol, err)
		}
		state, statePresent, err := parseIntegerRaw(contract.State)
		if err != nil {
			return nil, fmt.Errorf("MEXC Futures %q: некорректный state: %w", contract.Symbol, err)
		}
		if !typePresent || futureType != futuresPerpetual ||
			!statePresent || state != futuresEnabled {
			continue
		}

		if contract.Type != nil && strings.TrimSpace(string(contract.Type)) != "" {
			pairType, present, parseErr := parseIntegerRaw(contract.Type)
			if parseErr != nil {
				return nil, fmt.Errorf("MEXC Futures %q: некорректный type: %w", contract.Symbol, parseErr)
			}
			if present && pairType != futuresTypeNormal {
				continue
			}
		}
		if strings.TrimSpace(contract.Symbol) == "" || strings.TrimSpace(contract.BaseCoin) == "" {
			return nil, fmt.Errorf("MEXC Futures contract info: у активного USDT-контракта отсутствует symbol или baseCoin")
		}

		m, err := convertFuturesContract(contract)
		if err != nil {
			return nil, err
		}
		key := internalMarketKey(m)
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("MEXC Futures contract info: повторный контракт %q", m.Symbol)
		}
		seen[key] = struct{}{}
		markets = append(markets, m)
		perpetualCount++

		if contractSize, parseErr := parseDecimalRaw(contract.ContractSize); parseErr == nil && contractSize.GreaterThan(decimal.Zero) {
			contractSizes[strings.ToUpper(strings.TrimSpace(contract.Symbol))] = contractSize
		}
	}

	if len(markets) == 0 {
		return nil, fmt.Errorf("MEXC Spot/Futures API вернул ноль поддерживаемых USDT-рынков")
	}

	a.mu.Lock()
	a.contractSizes = contractSizes
	a.mu.Unlock()

	a.logger.Info(
		"MEXC markets fetched",
		"spot_usdt", spotCount,
		"perpetual_usdt", perpetualCount,
		"total", len(markets),
	)
	return markets, nil
}

// convertSpotInstrument переводит метаданные активной Spot-пары во внутренний Market.
func convertSpotInstrument(instrument spotSymbol) (market.Market, error) {
	if !strings.EqualFold(strings.TrimSpace(instrument.QuoteAsset), usdtAsset) ||
		strings.TrimSpace(instrument.Symbol) == "" ||
		strings.TrimSpace(instrument.BaseAsset) == "" ||
		instrument.IsSpotTradingAllowed == nil ||
		!*instrument.IsSpotTradingAllowed {
		return market.Market{}, fmt.Errorf("MEXC Spot instrument %q не является поддерживаемой Spot USDT-парой", instrument.Symbol)
	}
	status, present, err := parseIntegerRaw(instrument.Status)
	if err != nil || !present || status != spotStatusOnline {
		return market.Market{}, fmt.Errorf("MEXC Spot instrument %q не имеет активного статуса", instrument.Symbol)
	}

	symbol := market.NewSymbol(instrument.BaseAsset, instrument.QuoteAsset)
	result := market.Market{
		Exchange:     "mexc",
		Symbol:       strings.TrimSpace(instrument.Symbol),
		BaseAsset:    symbol.BaseAsset,
		QuoteAsset:   symbol.QuoteAsset,
		MarketType:   market.MarketTypeSpot,
		ContractType: market.ContractTypeSpot,
		Active:       true,
	}
	normalized, err := market.NormalizeMarket(result)
	if err != nil {
		return market.Market{}, fmt.Errorf("нормализовать MEXC Spot рынок %q: %w", instrument.Symbol, err)
	}
	return normalized, nil
}

// convertFuturesContract переводит активный бессрочный USDT-контракт во внутренний Market.
func convertFuturesContract(contract futuresContract) (market.Market, error) {
	if !strings.EqualFold(strings.TrimSpace(contract.QuoteCoin), usdtAsset) ||
		!strings.EqualFold(strings.TrimSpace(contract.SettleCoin), usdtAsset) ||
		strings.TrimSpace(contract.Symbol) == "" ||
		strings.TrimSpace(contract.BaseCoin) == "" {
		return market.Market{}, fmt.Errorf("MEXC Futures contract %q не является USDT Perpetual", contract.Symbol)
	}

	futureType, typePresent, typeErr := parseIntegerRaw(contract.FutureType)
	state, statePresent, stateErr := parseIntegerRaw(contract.State)
	if typeErr != nil || stateErr != nil || !typePresent || !statePresent ||
		futureType != futuresPerpetual || state != futuresEnabled {
		return market.Market{}, fmt.Errorf("MEXC Futures contract %q не имеет активного perpetual-статуса", contract.Symbol)
	}

	symbol := market.NewSymbol(contract.BaseCoin, contract.QuoteCoin)
	result := market.Market{
		Exchange:     "mexc",
		Symbol:       strings.TrimSpace(contract.Symbol),
		BaseAsset:    symbol.BaseAsset,
		QuoteAsset:   symbol.QuoteAsset,
		MarketType:   market.MarketTypeFutures,
		ContractType: market.ContractTypePerpetual,
		Active:       true,
	}
	normalized, err := market.NormalizeMarket(result)
	if err != nil {
		return market.Market{}, fmt.Errorf("нормализовать MEXC Futures рынок %q: %w", contract.Symbol, err)
	}
	return normalized, nil
}

func internalMarketKey(m market.Market) string {
	return strings.ToUpper(strings.TrimSpace(m.Exchange)) + "|" +
		strings.ToUpper(strings.TrimSpace(m.Symbol)) + "|" +
		string(m.MarketType) + "|" +
		string(m.ContractType)
}
