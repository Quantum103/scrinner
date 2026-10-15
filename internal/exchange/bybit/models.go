package bybit

import "encoding/json"

// Инструменты Bybit V5 и их внутреннее JSON-представление.

const (
	instrumentEndpoint = "/v5/market/instruments-info"
	tickersEndpoint    = "/v5/market/tickers"
	spotCategory       = "spot"
	linearCategory     = "linear"
	tradingStatus      = "Trading"
	linearPerpetual    = "LinearPerpetual"
	usdtAsset          = "USDT"
	maxInstrumentPages = 100
)

// apiEnvelope описывает общую оболочку ответа публичного V5 API Bybit.
type apiEnvelope struct {
	RetCode *int            `json:"retCode"`
	RetMsg  string          `json:"retMsg"`
	Result  json.RawMessage `json:"result"`
	Time    int64           `json:"time"`
}

// instrumentsResult описывает результат запроса списка инструментов.
type instrumentsResult struct {
	Category       string            `json:"category"`
	NextPageCursor string            `json:"nextPageCursor"`
	List           []bybitInstrument `json:"list"`
}

// tickerResult описывает результат запроса тикеров.
type tickerResult struct {
	Category string        `json:"category"`
	List     []bybitTicker `json:"list"`
}

// bybitInstrument содержит только поля, необходимые Stage 4.
type bybitInstrument struct {
	Symbol       string `json:"symbol"`
	ContractType string `json:"contractType"`
	Status       string `json:"status"`
	BaseCoin     string `json:"baseCoin"`
	QuoteCoin    string `json:"quoteCoin"`
	SettleCoin   string `json:"settleCoin"`
}

// bybitTicker содержит bid/ask и объём из ответа Get Tickers.
type bybitTicker struct {
	Symbol    string `json:"symbol"`
	Bid1Price string `json:"bid1Price"`
	Ask1Price string `json:"ask1Price"`
	Volume24h string `json:"volume24h"`
}
