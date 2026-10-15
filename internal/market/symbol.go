package market

import (
	"fmt"
	"strings"
)

// Symbol содержит каноническое представление пары базового и котируемого активов.
type Symbol struct {
	BaseAsset  string
	QuoteAsset string
}

// NewSymbol создаёт канонический символ из базового и котируемого активов.
func NewSymbol(baseAsset, quoteAsset string) Symbol {
	return Symbol{
		BaseAsset:  strings.ToUpper(strings.TrimSpace(baseAsset)),
		QuoteAsset: strings.ToUpper(strings.TrimSpace(quoteAsset)),
	}
}

// IsValid проверяет, что оба актива указаны.
func (s Symbol) IsValid() bool {
	return s.BaseAsset != "" && s.QuoteAsset != ""
}

// String возвращает каноническое представление пары.
func (s Symbol) String() string {
	if !s.IsValid() {
		return ""
	}

	return fmt.Sprintf("%s/%s", s.BaseAsset, s.QuoteAsset)
}
