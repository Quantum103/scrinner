// Package mexc реализует публичный REST-адаптер рыночных данных MEXC.
package mexc

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// Пути публичного REST API MEXC Spot и Futures.
const (
	spotExchangeInfoEndpoint    = "/api/v3/exchangeInfo"
	spotBookTickerEndpoint      = "/api/v3/ticker/bookTicker"
	spot24hrTickerEndpoint      = "/api/v3/ticker/24hr"
	futuresContractInfoEndpoint = "/api/v1/contract/detail/country"
	futuresTickerEndpoint       = "/api/v1/contract/ticker"

	usdtAsset = "USDT"

	spotStatusOnline = 1
	futuresPerpetual = 1
	futuresEnabled   = 0
	futuresTypeNormal = 1
)

// TickerStats содержит сводную статистику сбора котировок MEXC.
type TickerStats struct {
	Received int
	Valid    int
	Skipped  int
}

// spotExchangeInfoResponse содержит метаданные торговых пар Spot.
type spotExchangeInfoResponse struct {
	Symbols []spotSymbol `json:"symbols"`
}

// spotSymbol содержит поля, необходимые для фильтрации Spot-рынка.
type spotSymbol struct {
	Symbol               string          `json:"symbol"`
	Status               json.RawMessage `json:"status"`
	BaseAsset            string          `json:"baseAsset"`
	QuoteAsset           string          `json:"quoteAsset"`
	IsSpotTradingAllowed *bool           `json:"isSpotTradingAllowed"`
	Permissions           []string        `json:"permissions"`
	TradeSideType         json.RawMessage `json:"tradeSideType"`
}

// futuresContract содержит структурированные метаданные бессрочного контракта.
type futuresContract struct {
	Symbol      string          `json:"symbol"`
	BaseCoin    string          `json:"baseCoin"`
	QuoteCoin   string          `json:"quoteCoin"`
	SettleCoin  string          `json:"settleCoin"`
	FutureType  json.RawMessage `json:"futureType"`
	State       json.RawMessage `json:"state"`
	Type        json.RawMessage `json:"type"`
	ContractSize json.RawMessage `json:"contractSize"`
	APIAllowed  *bool           `json:"apiAllowed"`
	IsHidden    *bool           `json:"isHidden"`
}

// spotBookTicker описывает лучшие цены Spot из bookTicker.
type spotBookTicker struct {
	Symbol   string          `json:"symbol"`
	BidPrice json.RawMessage `json:"bidPrice"`
	AskPrice json.RawMessage `json:"askPrice"`
	BidQty   json.RawMessage `json:"bidQty"`
	AskQty   json.RawMessage `json:"askQty"`
}

// spot24hrTicker содержит статистику Spot за последние 24 часа.
type spot24hrTicker struct {
	Symbol      string          `json:"symbol"`
	Volume      json.RawMessage `json:"volume"`
	QuoteVolume json.RawMessage `json:"quoteVolume"`
	CloseTime   json.RawMessage `json:"closeTime"`
}

// futuresTicker содержит лучшие цены и объёмы бессрочного контракта.
type futuresTicker struct {
	Symbol    string          `json:"symbol"`
	Bid1      json.RawMessage `json:"bid1"`
	Ask1      json.RawMessage `json:"ask1"`
	Volume24  json.RawMessage `json:"volume24"`
	Amount24  json.RawMessage `json:"amount24"`
	Timestamp json.RawMessage `json:"timestamp"`
}

// futuresEnvelope описывает общую оболочку ответов публичного Futures API.
type futuresEnvelope struct {
	Success *bool           `json:"success"`
	Code    json.RawMessage `json:"code"`
	Message string          `json:"message"`
	Msg     string          `json:"msg"`
	Data    json.RawMessage `json:"data"`
}

// spotAPIError описывает возможную JSON-ошибку Spot API при HTTP 200.
type spotAPIError struct {
	Success *bool           `json:"success"`
	Code    json.RawMessage `json:"code"`
	Message string          `json:"message"`
	Msg     string          `json:"msg"`
}

// spotTickerList принимает как массив котировок, так и объект одной котировки.
type spotTickerList []spotBookTicker

// UnmarshalJSON разбирает массив, одиночный объект либо отклоняет null.
func (l *spotTickerList) UnmarshalJSON(data []byte) error {
	return unmarshalArrayOrObject(data, (*[]spotBookTicker)(l), func() any {
		return &spotBookTicker{}
	})
}

// spotStatsList принимает массив статистики или объект одной торговой пары.
type spotStatsList []spot24hrTicker

// UnmarshalJSON разбирает массив, одиночный объект либо отклоняет null.
func (l *spotStatsList) UnmarshalJSON(data []byte) error {
	return unmarshalArrayOrObject(data, (*[]spot24hrTicker)(l), func() any {
		return &spot24hrTicker{}
	})
}

// futuresContractList принимает массив контрактов или объект одного контракта.
type futuresContractList []futuresContract

// UnmarshalJSON разбирает массив, одиночный объект либо отклоняет null.
func (l *futuresContractList) UnmarshalJSON(data []byte) error {
	return unmarshalArrayOrObject(data, (*[]futuresContract)(l), func() any {
		return &futuresContract{}
	})
}

// futuresTickerList принимает массив котировок или объект одного контракта.
type futuresTickerList []futuresTicker

// UnmarshalJSON разбирает массив, одиночный объект либо отклоняет null.
func (l *futuresTickerList) UnmarshalJSON(data []byte) error {
	return unmarshalArrayOrObject(data, (*[]futuresTicker)(l), func() any {
		return &futuresTicker{}
	})
}

func unmarshalArrayOrObject[T any](data []byte, target *[]T, newItem func() any) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		*target = nil
		return nil
	}
	if trimmed[0] == '[' {
		return json.Unmarshal(trimmed, target)
	}
	if trimmed[0] != '{' {
		return fmt.Errorf("ожидался JSON-массив или объект, получено %q", string(trimmed))
	}

	item := newItem()
	if err := json.Unmarshal(trimmed, item); err != nil {
		return err
	}
	encoded, err := json.Marshal(item)
	if err != nil {
		return err
	}
	var value T
	if err := json.Unmarshal(encoded, &value); err != nil {
		return err
	}
	*target = []T{value}
	return nil
}

func containsPermission(permissions []string, expected string) bool {
	for _, permission := range permissions {
		if strings.EqualFold(strings.TrimSpace(permission), expected) {
			return true
		}
	}
	return false
}
