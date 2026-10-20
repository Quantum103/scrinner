package gate

import (
	"context"
	"fmt"
	"strings"

	"github.com/example/crypto-arbitrage-scanner/internal/market"
	"github.com/shopspring/decimal"
)

// discoverMarkets получает активные Spot USDT и линейные USDT Perpetual-рынки.
func (a *Adapter) discoverMarkets(ctx context.Context) ([]market.Market, error) {
	var spotPairs []spotCurrencyPair
	if _, err := a.client.getJSON(ctx, "получить Spot currency pairs", spotCurrencyPairsEndpoint, &spotPairs); err != nil {
		return nil, fmt.Errorf("получить Gate Spot currency pairs: %w", err)
	}
	if spotPairs == nil || len(spotPairs) == 0 {
		return nil, fmt.Errorf("Gate Spot currency_pairs вернул пустой список")
	}

	// Пакетные Spot-тикеры содержат ETF-специфичные поля, которых нет в метаданных пары.
	var spotTickerResponse []spotTicker
	spotTickerReceivedAt, err := a.client.getJSON(ctx, "получить Spot tickers для фильтрации ETF", spotTickersEndpoint, &spotTickerResponse)
	if err != nil {
		return nil, fmt.Errorf("получить Gate Spot tickers при обнаружении рынков: %w", err)
	}
	if spotTickerResponse == nil || len(spotTickerResponse) == 0 {
		return nil, fmt.Errorf("Gate Spot tickers вернул пустой список при обнаружении рынков")
	}
	spotETFs := make(map[string]struct{})
	for _, ticker := range spotTickerResponse {
		if isSpecialETF(ticker) {
			spotETFs[strings.ToUpper(strings.TrimSpace(ticker.CurrencyPair))] = struct{}{}
		}
	}

	var contracts []futuresContract
	if _, err := a.client.getJSON(ctx, "получить USDT Perpetual contracts", futuresContractsEndpoint, &contracts); err != nil {
		return nil, fmt.Errorf("получить Gate USDT futures contracts: %w", err)
	}
	if contracts == nil || len(contracts) == 0 {
		return nil, fmt.Errorf("Gate futures/usdt/contracts вернул пустой список")
	}

	markets := make([]market.Market, 0, len(spotPairs)+len(contracts))
	seen := make(map[string]struct{}, len(markets))
	spotCount, perpetualCount := 0, 0


	for _, pair := range spotPairs {
		if !strings.EqualFold(strings.TrimSpace(pair.Quote), usdtAsset) {
			continue
		}
		if _, isETF := spotETFs[strings.ToUpper(strings.TrimSpace(pair.ID))]; isETF {
			a.logger.Debug("Gate ETF-пара исключена из обычной Spot-выборки", "pair", pair.ID)
			continue
		}
		converted, supported, err := convertSpotPair(pair)
		if err != nil {
			a.logger.Warn("Gate Spot-пара пропущена", "pair", pair.ID, "error", err)
			continue
		}
		if !supported {
			continue
		}
		key := internalMarketKey(converted)
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("Gate Spot currency_pairs содержит повторную пару %q", converted.Symbol)
		}
		seen[key] = struct{}{}
		markets = append(markets, converted)
		spotCount++
	}

	for _, contract := range contracts {
		converted, supported, err := convertFuturesContract(contract)
		if err != nil {
			a.logger.Warn("Gate USDT-контракт пропущен", "contract", contract.Name, "error", err)
			continue
		}
		if !supported {
			continue
		}
		key := internalMarketKey(converted)
		if _, duplicate := seen[key]; duplicate {
			return nil, fmt.Errorf("Gate futures contracts содержит повторный контракт %q", converted.Symbol)
		}
		seen[key] = struct{}{}
		markets = append(markets, converted)
		perpetualCount++
	}

	if len(markets) == 0 {
		return nil, fmt.Errorf("Gate API не вернул поддерживаемых активных рынков с котировкой USDT")
	}
	a.logger.Info(
		"Gate markets fetched",
		"spot_usdt", spotCount,
		"linear_usdt_perpetual", perpetualCount,
		"total", len(markets),
	)
	return markets, nil
}

// convertSpotPair проверяет двустороннюю торговлю, обычный тип и статус делистинга.
func convertSpotPair(pair spotCurrencyPair) (market.Market, bool, error) {
	id := strings.TrimSpace(pair.ID)
	base := strings.ToUpper(strings.TrimSpace(pair.Base))
	quote := strings.ToUpper(strings.TrimSpace(pair.Quote))
	if quote != usdtAsset {
		return market.Market{}, false, nil
	}
	if id == "" || base == "" {
		return market.Market{}, false, fmt.Errorf("отсутствует id или base")
	}
	switch strings.ToLower(strings.TrimSpace(pair.TradeStatus)) {
	case "tradable":
	case "buyable", "sellable", "untradable":
		return market.Market{}, false, nil
	default:
		return market.Market{}, false, fmt.Errorf("неизвестный или отсутствующий trade_status=%q", pair.TradeStatus)
	}
	switch strings.ToLower(strings.TrimSpace(pair.Type)) {
	case "normal":
	case "premarket":
		return market.Market{}, false, nil
	default:
		return market.Market{}, false, fmt.Errorf("неизвестный или отсутствующий type=%q", pair.Type)
	}
	if strings.TrimSpace(pair.Quote) == "" {
		return market.Market{}, false, fmt.Errorf("отсутствует quote")
	}
	delistingTime, present, err := parseInt64Raw(pair.DelistingTime)
	if err != nil {
		return market.Market{}, false, fmt.Errorf("некорректный delisting_time: %w", err)
	}
	if present && delistingTime != 0 {
		return market.Market{}, false, nil
	}

	// Идентификатор проверяется на согласованность, активы берутся из отдельных полей API.
	parts := strings.Split(id, "_")
	if len(parts) != 2 || !strings.EqualFold(strings.TrimSpace(parts[0]), base) ||
		!strings.EqualFold(strings.TrimSpace(parts[1]), quote) {
		return market.Market{}, false, fmt.Errorf("id %q не согласован с base=%q и quote=%q", id, base, quote)
	}

	symbol := market.NewSymbol(base, quote)
	normalized, err := market.NormalizeMarket(market.Market{
		Exchange: "gate", Symbol: id,
		BaseAsset: symbol.BaseAsset, QuoteAsset: symbol.QuoteAsset,
		MarketType: market.MarketTypeSpot, ContractType: market.ContractTypeSpot, Active: true,
	})
	if err != nil {
		return market.Market{}, false, fmt.Errorf("нормализовать Spot-пару %q: %w", id, err)
	}
	return normalized, true, nil
}

// convertFuturesContract принимает только активный прямой USDT Perpetual-контракт.
func convertFuturesContract(contract futuresContract) (market.Market, bool, error) {
	name := strings.TrimSpace(contract.Name)
	parts := strings.Split(name, "_")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" ||
		!strings.EqualFold(strings.TrimSpace(parts[1]), usdtAsset) {
		return market.Market{}, false, nil
	}
	switch strings.ToLower(strings.TrimSpace(contract.Type)) {
	case "direct":
	case "inverse":
		return market.Market{}, false, nil
	default:
		return market.Market{}, false, fmt.Errorf("неизвестный или отсутствующий type=%q", contract.Type)
	}
	switch strings.ToLower(strings.TrimSpace(contract.Status)) {
	case "trading":
	case "prelaunch", "delisting", "delisted", "circuit_breaker":
		return market.Market{}, false, nil
	default:
		return market.Market{}, false, fmt.Errorf("неизвестный или отсутствующий status=%q", contract.Status)
	}
	if contract.InDelisting == nil {
		return market.Market{}, false, fmt.Errorf("поле in_delisting отсутствует")
	}
	if *contract.InDelisting {
		return market.Market{}, false, nil
	}
	multiplierText, present, err := parseDecimal(contract.QuantoMultiplier)
	if err != nil || !present {
		return market.Market{}, false, fmt.Errorf("quanto_multiplier отсутствует или некорректен")
	}
	multiplier, err := decimal.NewFromString(multiplierText)
	if err != nil || !multiplier.GreaterThan(decimal.Zero) {
		return market.Market{}, false, fmt.Errorf("quanto_multiplier должен быть положительным decimal")
	}

	base := strings.ToUpper(strings.TrimSpace(parts[0]))
	symbol := market.NewSymbol(base, usdtAsset)
	normalized, err := market.NormalizeMarket(market.Market{
		Exchange: "gate", Symbol: name,
		BaseAsset: symbol.BaseAsset, QuoteAsset: symbol.QuoteAsset,
		MarketType: market.MarketTypeFutures, ContractType: market.ContractTypePerpetual, Active: true,
	})
	if err != nil {
		return market.Market{}, false, fmt.Errorf("нормализовать Futures-контракт %q: %w", name, err)
	}
	return normalized, true, nil
}

func internalMarketKey(m market.Market) string {
	return strings.ToUpper(strings.TrimSpace(m.Exchange)) + "|" +
		strings.ToUpper(strings.TrimSpace(m.Symbol)) + "|" +
		string(m.MarketType) + "|" +
		string(m.ContractType)
}
