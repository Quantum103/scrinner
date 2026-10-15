package market

import (
	"fmt"
	"strings"
)

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
