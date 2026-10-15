// Package okx реализует публичный REST-адаптер рыночных данных OKX.
package okx

import "encoding/json"

// Пути публичного REST API OKX.
const (
	publicInstrumentsEndpoint = "/api/v5/public/instruments"
	marketTickersEndpoint     = "/api/v5/market/tickers"
	usdtAsset                 = "USDT"
)

// TickerStats содержит статистику обработки пакетных котировок.
type TickerStats struct {
	Received int
	Valid    int
	Skipped  int
}

// apiResponse описывает общую оболочку публичных ответов OKX.
type apiResponse struct {
	Code json.RawMessage `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

// spotInstrument содержит метаданные, необходимые для выбора Spot-инструмента.
type spotInstrument struct {
	InstType string `json:"instType"`
	InstID   string `json:"instId"`
	BaseCcy  string `json:"baseCcy"`
	QuoteCcy string `json:"quoteCcy"`
	State    string `json:"state"`
}

// swapInstrument содержит метаданные, необходимые для проверки линейного SWAP-контракта.
type swapInstrument struct {
	InstType   string `json:"instType"`
	InstID     string `json:"instId"`
	BaseCcy    string `json:"baseCcy"`
	QuoteCcy   string `json:"quoteCcy"`
	SettleCcy  string `json:"settleCcy"`
	CtType     string `json:"ctType"`
	CtValCcy   string `json:"ctValCcy"`
	Uly        string `json:"uly"`
	InstFamily string `json:"instFamily"`
	State      string `json:"state"`
}

// marketTicker содержит только поля пакетной котировки, используемые сканером.
type marketTicker struct {
	InstType  string          `json:"instType"`
	InstID    string          `json:"instId"`
	BidPx     json.RawMessage `json:"bidPx"`
	AskPx     json.RawMessage `json:"askPx"`
	Timestamp json.RawMessage `json:"ts"`
	Vol24h    json.RawMessage `json:"vol24h"`
	VolCcy24h json.RawMessage `json:"volCcy24h"`
}
