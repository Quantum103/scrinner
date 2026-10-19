package bitget

import (
	"fmt"
	"strings"

	"github.com/example/crypto-arbitrage-scanner/internal/market"
)

// convertInstrument принимает только активный обычный Spot или USDT Perpetual-инструмент.
func convertInstrument(item instrument, expectedCategory string) (market.Market, bool, error) {
	category := strings.ToUpper(strings.TrimSpace(item.Category))
	if category != expectedCategory {
		return market.Market{}, false, fmt.Errorf("категория %q не совпадает с ожидаемой %q", item.Category, expectedCategory)
	}
	symbol := item.Symbol
	if strings.TrimSpace(symbol) == "" || strings.TrimSpace(symbol) != symbol {
		return market.Market{}, false, fmt.Errorf("символ отсутствует или содержит внешние пробелы: %q", symbol)
	}
	base := strings.ToUpper(strings.TrimSpace(item.BaseCoin))
	quote := strings.ToUpper(strings.TrimSpace(item.QuoteCoin))
	if base == "" || quote == "" {
		return market.Market{}, false, fmt.Errorf("не заданы структурированные baseCoin и quoteCoin")
	}
	if quote != usdtAsset {
		return market.Market{}, false, nil
	}
	if base == quote {
		return market.Market{}, false, fmt.Errorf("baseCoin и quoteCoin совпадают: %q", base)
	}
	status := strings.ToLower(strings.TrimSpace(item.Status))
	if status != "online" {
		switch status {
		case "listed", "limit_open", "limit_close", "offline", "restrictedapi":
			return market.Market{}, false, nil
		default:
			return market.Market{}, false, fmt.Errorf("неизвестный или пустой status=%q", item.Status)
		}
	}

	crypto, err := isCryptoInstrument(item)
	if err != nil {
		return market.Market{}, false, err
	}
	if !crypto {
		return market.Market{}, false, nil
	}
	if supported, err := checkFlag(item.IsRWA, "isRwa", "YES", "NO"); err != nil || !supported {
		return market.Market{}, false, err
	}
	if expectedCategory == spotCategory {
		if supported, err := checkFlag(item.IsReality, "isReality", "yes", "no"); err != nil || !supported {
			return market.Market{}, false, err
		}
	}

	var marketType market.MarketType
	var contractType market.ContractType
	switch expectedCategory {
	case spotCategory:
		marketType = market.MarketTypeSpot
		contractType = market.ContractTypeSpot
	case futuresCategory:
		typeName := strings.ToLower(strings.TrimSpace(item.Type))
		switch typeName {
		case "perpetual":
			marketType = market.MarketTypeFutures
			contractType = market.ContractTypePerpetual
		case "delivery":
			return market.Market{}, false, nil
		default:
			return market.Market{}, false, fmt.Errorf("неизвестный или пустой futures type=%q", item.Type)
		}
	default:
		return market.Market{}, false, fmt.Errorf("категория %q не поддерживается", expectedCategory)
	}

	normalized, err := market.NormalizeMarket(market.Market{
		Exchange:     stringExchangeBitget,
		Symbol:       symbol,
		BaseAsset:    base,
		QuoteAsset:   quote,
		MarketType:   marketType,
		ContractType: contractType,
		Active:       true,
	})
	if err != nil {
		return market.Market{}, false, fmt.Errorf("нормализовать инструмент %q: %w", symbol, err)
	}
	return normalized, true, nil
}

const stringExchangeBitget = "bitget"

// isCryptoInstrument проверяет документированные классификаторы; отсутствующее отдельное поле допустимо.
func isCryptoInstrument(item instrument) (bool, error) {
	values := []struct {
		name  string
		value string
		known []string
	}{
		{name: "symbolType", value: item.SymbolType, known: []string{"crypto", "metal", "stock", "commodity"}},
		{name: "instrumentType", value: item.InstrumentType, known: []string{"crypto", "metal", "energy", "stock", "etf"}},
	}
	known := false
	for _, value := range values {
		text := strings.ToLower(strings.TrimSpace(value.value))
		if text == "" {
			continue
		}
		known = true
		allowed := false
		for _, option := range value.known {
			if text == option {
				allowed = true
				break
			}
		}
		if !allowed {
			return false, fmt.Errorf("неизвестное значение %s=%q", value.name, value.value)
		}
		if text != "crypto" {
			return false, nil
		}
	}
	if !known {
		return false, fmt.Errorf("оба классификатора symbolType и instrumentType отсутствуют")
	}
	return true, nil
}

// checkFlag фильтрует документированные положительные и отрицательные значения классификационного флага.
func checkFlag(value, field, positiveValue, negativeValue string) (bool, error) {
	text := strings.TrimSpace(value)
	if text == "" {
		return true, nil
	}
	if strings.EqualFold(text, positiveValue) {
		return false, nil
	}
	if strings.EqualFold(text, negativeValue) {
		return true, nil
	}
	return false, fmt.Errorf("неизвестное значение %s=%q", field, value)
}

func marketKey(item market.Market) string {
	return strings.ToUpper(strings.TrimSpace(item.Exchange)) + "|" +
		strings.ToUpper(strings.TrimSpace(item.Symbol)) + "|" +
		string(item.MarketType) + "|" + string(item.ContractType)
}

func fmtEmptyResponse(endpoint, category string) error {
	return fmt.Errorf("Bitget %s вернул пустой список data для category=%s", endpoint, category)
}

func fmtEmptyMarkets() error {
	return fmt.Errorf("Bitget не вернул поддерживаемых активных Spot USDT или USDT Perpetual рынков")
}

func countSpot(markets []market.Market) int {
	count := 0
	for _, item := range markets {
		if item.IsSpot() {
			count++
		}
	}
	return count
}

func countPerpetual(markets []market.Market) int {
	count := 0
	for _, item := range markets {
		if item.IsPerpetual() {
			count++
		}
	}
	return count
}
