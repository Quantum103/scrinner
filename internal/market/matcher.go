package market

import (
	"fmt"
	"strings"
)

// SymbolMatchesAssets проверяет, что биржевой символ выражает именно те
// baseAsset/quoteAsset, которые вернуло API. Это защищает от контрактов с
// масштабированным обозначением (например, 1000TOKENUSDT) под базовым активом
// TOKEN: цена такого контракта не сопоставима с ценой одного TOKEN.
func SymbolMatchesAssets(m Market) bool {
	base := strings.ToUpper(strings.TrimSpace(m.BaseAsset))
	quote := strings.ToUpper(strings.TrimSpace(m.QuoteAsset))
	symbol := strings.ToUpper(strings.TrimSpace(m.Symbol))
	if base == "" || quote == "" || symbol == "" {
		return false
	}

	// OKX perpetual symbols use BASE-USDT-SWAP; SWAP is a market-type suffix,
	// not part of the base asset.
	if strings.HasSuffix(symbol, "-SWAP") {
		symbol = strings.TrimSuffix(symbol, "-SWAP")
	}
	symbol = strings.NewReplacer("_", "", "-", "", "/", "", ".", "").Replace(symbol)
	return symbol == base+quote
}

// AreMarketsComparable определяет, могут ли два рынка участвовать
// в дальнейшем сравнении цен.
//
// На этом этапе не анализируются bid, ask или spread.
// Проверяется только структура рынков и возможность их сопоставления.
func AreMarketsComparable(a, b Market) bool {
	normalizedA, err := NormalizeMarket(a)
	if err != nil || !normalizedA.IsSupported() {
		return false
	}

	normalizedB, err := NormalizeMarket(b)
	if err != nil || !normalizedB.IsSupported() {
		return false
	}
	if !SymbolMatchesAssets(normalizedA) || !SymbolMatchesAssets(normalizedB) {
		return false
	}

	// Один и тот же рынок на одной бирже нельзя использовать
	// как межбиржевую пару.
	if strings.EqualFold(
		normalizedA.Exchange,
		normalizedB.Exchange,
	) {
		return false
	}

	if normalizedA.BaseAsset != normalizedB.BaseAsset {
		return false
	}

	if normalizedA.QuoteAsset != normalizedB.QuoteAsset {
		return false
	}

	return true
}

// ValidateComparableMarkets возвращает подробную ошибку,
// если два рынка нельзя сопоставить.
func ValidateComparableMarkets(a, b Market) error {
	normalizedA, err := NormalizeMarket(a)
	if err != nil {
		return fmt.Errorf("invalid first market: %w", err)
	}

	normalizedB, err := NormalizeMarket(b)
	if err != nil {
		return fmt.Errorf("invalid second market: %w", err)
	}

	if !normalizedA.IsSupported() {
		return fmt.Errorf(
			"first market is not supported: %s",
			normalizedA.Symbol,
		)
	}

	if !normalizedB.IsSupported() {
		return fmt.Errorf(
			"second market is not supported: %s",
			normalizedB.Symbol,
		)
	}
	if !SymbolMatchesAssets(normalizedA) {
		return fmt.Errorf("first market symbol %q does not match base/quote %s/%s", normalizedA.Symbol, normalizedA.BaseAsset, normalizedA.QuoteAsset)
	}
	if !SymbolMatchesAssets(normalizedB) {
		return fmt.Errorf("second market symbol %q does not match base/quote %s/%s", normalizedB.Symbol, normalizedB.BaseAsset, normalizedB.QuoteAsset)
	}

	if strings.EqualFold(normalizedA.Exchange, normalizedB.Exchange) {
		return fmt.Errorf(
			"markets belong to the same exchange: %s",
			normalizedA.Exchange,
		)
	}

	if normalizedA.BaseAsset != normalizedB.BaseAsset {
		return fmt.Errorf(
			"base assets differ: %s != %s",
			normalizedA.BaseAsset,
			normalizedB.BaseAsset,
		)
	}

	if normalizedA.QuoteAsset != normalizedB.QuoteAsset {
		return fmt.Errorf(
			"quote assets differ: %s != %s",
			normalizedA.QuoteAsset,
			normalizedB.QuoteAsset,
		)
	}

	return nil
}

// MarketIdentityEqual проверяет, описывают ли два объекта один и тот же
// физический рынок одной биржи.
//
// Оригинальный symbol учитывается, потому что именно он идентифицирует
// инструмент в контексте конкретной биржи.
func MarketIdentityEqual(a, b Market) bool {
	return strings.EqualFold(strings.TrimSpace(a.Exchange), strings.TrimSpace(b.Exchange)) &&
		strings.EqualFold(strings.TrimSpace(a.Symbol), strings.TrimSpace(b.Symbol)) &&
		a.MarketType == b.MarketType &&
		a.ContractType == b.ContractType
}
