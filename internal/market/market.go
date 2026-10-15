package market

import (
	"errors"
	"fmt"
	"strings"
)

// MarketType описывает общий тип торгового рынка.
type MarketType string

const (
	// MarketTypeSpot обозначает спотовый рынок.
	MarketTypeSpot MarketType = "spot"

	// MarketTypeFutures обозначает фьючерсный рынок.
	MarketTypeFutures MarketType = "futures"
)

// ContractType описывает вид контракта.
type ContractType string

const (
	// ContractTypeSpot обозначает спотовый инструмент без фьючерсного контракта.
	ContractTypeSpot ContractType = "spot"

	// ContractTypePerpetual обозначает бессрочный фьючерсный контракт.
	ContractTypePerpetual ContractType = "perpetual"

	// ContractTypeDelivery обозначает срочный фьючерсный контракт.
	// Он сохраняется для совместимости с моделью Stage 1/2,
	// но не поддерживается в текущем scanner.
	ContractTypeDelivery ContractType = "delivery"
)

// Market описывает торговый рынок биржи во внутреннем формате приложения.
type Market struct {
	Exchange string

	// Symbol содержит оригинальное обозначение инструмента,
	// полученное от конкретной биржи.
	Symbol string

	BaseAsset  string
	QuoteAsset string

	MarketType   MarketType
	ContractType ContractType

	Active bool
}

// IsSpot сообщает, является ли рынок поддерживаемым спотовым рынком.
func (m Market) IsSpot() bool {
	return m.MarketType == MarketTypeSpot &&
		m.ContractType == ContractTypeSpot
}

// IsPerpetual сообщает, является ли рынок бессрочным USDT-фьючерсом.
func (m Market) IsPerpetual() bool {
	return m.MarketType == MarketTypeFutures &&
		m.ContractType == ContractTypePerpetual &&
		strings.EqualFold(strings.TrimSpace(m.QuoteAsset), "USDT")
}

// IsSupported сообщает, относится ли рынок к текущей области Stage 3.
func (m Market) IsSupported() bool {
	if !m.Active {
		return false
	}

	return m.IsSpot() || m.IsPerpetual()
}

// IsValidMarketType проверяет, известен ли тип рынка приложению.
func IsValidMarketType(marketType MarketType) bool {
	switch marketType {
	case MarketTypeSpot, MarketTypeFutures:
		return true
	default:
		return false
	}
}

// IsValidContractType проверяет, известен ли тип контракта приложению.
func IsValidContractType(contractType ContractType) bool {
	switch contractType {
	case ContractTypeSpot, ContractTypePerpetual, ContractTypeDelivery:
		return true
	default:
		return false
	}
}

// ValidateMarket проверяет структурную корректность рынка.
//
// Проверка активности здесь намеренно не выполняется.
// Неактивный рынок может быть корректной записью,
// которая должна сохраняться в базе для истории.
func ValidateMarket(m Market) error {
	if strings.TrimSpace(m.Exchange) == "" {
		return errors.New("exchange is required")
	}

	if strings.TrimSpace(m.Symbol) == "" {
		return errors.New("symbol is required")
	}

	if strings.TrimSpace(m.BaseAsset) == "" {
		return errors.New("base asset is required")
	}

	if strings.TrimSpace(m.QuoteAsset) == "" {
		return errors.New("quote asset is required")
	}

	if !IsValidMarketType(m.MarketType) {
		return fmt.Errorf("unsupported market type: %q", m.MarketType)
	}

	if !IsValidContractType(m.ContractType) {
		return fmt.Errorf("unsupported contract type: %q", m.ContractType)
	}

	baseAsset := strings.ToUpper(strings.TrimSpace(m.BaseAsset))
	quoteAsset := strings.ToUpper(strings.TrimSpace(m.QuoteAsset))

	if baseAsset == quoteAsset {
		return fmt.Errorf(
			"base asset and quote asset must be different: %q",
			baseAsset,
		)
	}

	if quoteAsset != "USDT" {
		return fmt.Errorf(
			"unsupported quote asset: %q",
			quoteAsset,
		)
	}

	switch m.MarketType {
	case MarketTypeSpot:
		if m.ContractType != ContractTypeSpot {
			return fmt.Errorf(
				"spot market must use contract type %q, got %q",
				ContractTypeSpot,
				m.ContractType,
			)
		}

	case MarketTypeFutures:
		if m.ContractType != ContractTypePerpetual {
			return fmt.Errorf(
				"futures market must use contract type %q for the current scanner, got %q",
				ContractTypePerpetual,
				m.ContractType,
			)
		}
	}

	return nil
}

// IsSupportedMarket проверяет, может ли рынок участвовать в сопоставлении.
//
// В отличие от ValidateMarket эта функция также требует,
// чтобы рынок был активным.
func IsSupportedMarket(m Market) bool {
	if err := ValidateMarket(m); err != nil {
		return false
	}

	return m.Active
}