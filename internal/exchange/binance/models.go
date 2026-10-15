// Package binance реализует публичный REST-адаптер рыночных данных Binance.
package binance

// Пути и значения полей официального публичного REST API Binance.
const (
	spotExchangeInfoEndpoint    = "/api/v3/exchangeInfo"
	spotBookTickerEndpoint      = "/api/v3/ticker/bookTicker"
	spot24hrTickerEndpoint      = "/api/v3/ticker/24hr"
	futuresExchangeInfoEndpoint = "/fapi/v1/exchangeInfo"
	futuresBookTickerEndpoint   = "/fapi/v1/ticker/bookTicker"
	futures24hrTickerEndpoint   = "/fapi/v1/ticker/24hr"

	tradingStatus   = "TRADING"
	perpetualType   = "PERPETUAL"
	usdtAsset       = "USDT"
	maxSpotSymbols  = 100
)

// apiErrorResponse описывает стандартный ответ Binance об ошибке.
type apiErrorResponse struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
}

// spotExchangeInfoResponse содержит торговые параметры спотового рынка.
type spotExchangeInfoResponse struct {
	Symbols []spotSymbol `json:"symbols"`
}

// spotSymbol содержит необходимые поля спотового инструмента.
type spotSymbol struct {
	Symbol               string `json:"symbol"`
	Status               string `json:"status"`
	BaseAsset            string `json:"baseAsset"`
	QuoteAsset           string `json:"quoteAsset"`
	IsSpotTradingAllowed *bool  `json:"isSpotTradingAllowed"`
}

// futuresExchangeInfoResponse содержит торговые параметры USDⓈ-M Futures.
type futuresExchangeInfoResponse struct {
	Symbols []futuresSymbol `json:"symbols"`
}

// futuresSymbol описывает контракт USDⓈ-M из ответа exchangeInfo.
type futuresSymbol struct {
	Symbol       string `json:"symbol"`
	Pair         string `json:"pair"`
	ContractType string `json:"contractType"`
	Status       string `json:"status"`
	BaseAsset    string `json:"baseAsset"`
	QuoteAsset   string `json:"quoteAsset"`
	MarginAsset  string `json:"marginAsset"`
	DeliveryDate int64  `json:"deliveryDate"`
}

// bookTicker содержит лучшие BID/ASK; время доступно у USDⓈ-M Futures,
// но отсутствует в ответе Spot bookTicker.
type bookTicker struct {
	Symbol    string `json:"symbol"`
	BidPrice  string `json:"bidPrice"`
	BidQty    string `json:"bidQty"`
	AskPrice  string `json:"askPrice"`
	AskQty    string `json:"askQty"`
	Time      *int64 `json:"time"`
}

// spot24hrTicker содержит объём базового актива из статистики Spot за 24 часа.
type spot24hrTicker struct {
	Symbol string `json:"symbol"`
	Volume string `json:"volume"`
}

// futures24hrTicker содержит объём базового актива из статистики USDⓈ-M за 24 часа.
type futures24hrTicker struct {
	Symbol string `json:"symbol"`
	Volume string `json:"volume"`
}

// TickerStats описывает количество полученных, принятых и пропущенных строк тикеров.
type TickerStats struct {
	Received int
	Valid    int
	Skipped  int
}
