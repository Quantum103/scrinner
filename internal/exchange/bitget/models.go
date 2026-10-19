package bitget

import "encoding/json"

// apiEnvelope описывает стандартную оболочку ответа публичного Bitget API V3.
type apiEnvelope struct {
	Code string          `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

// instrument содержит только публичные метаданные инструмента, необходимые адаптеру.
type instrument struct {
	Category       string `json:"category"`
	Symbol         string `json:"symbol"`
	BaseCoin       string `json:"baseCoin"`
	QuoteCoin      string `json:"quoteCoin"`
	Status         string `json:"status"`
	Type           string `json:"type"`
	SymbolType     string `json:"symbolType"`
	InstrumentType string `json:"instrumentType"`
	IsRWA          string `json:"isRwa"`
	IsReality      string `json:"isReality"`
	AreaSymbol     string `json:"areaSymbol"`
	FullName       string `json:"fullName"`
	ISIN           string `json:"isin"`
}

// ticker содержит пакетную котировку V3 без преобразования финансовых чисел в float64.
type ticker struct {
	Category       string          `json:"category"`
	Symbol         string          `json:"symbol"`
	Ask1Price      json.RawMessage `json:"ask1Price"`
	Bid1Price      json.RawMessage `json:"bid1Price"`
	Ask1Size       json.RawMessage `json:"ask1Size"`
	Bid1Size       json.RawMessage `json:"bid1Size"`
	Volume24h      json.RawMessage `json:"volume24h"`
	Turnover24h    json.RawMessage `json:"turnover24h"`
	TimestampMilli json.RawMessage `json:"ts"`
	LastPrice      json.RawMessage `json:"lastPrice"`
	MarkPrice      json.RawMessage `json:"markPrice"`
	IndexPrice     json.RawMessage `json:"indexPrice"`
}
