// Package gate реализует публичный REST-адаптер рыночных данных Gate API v4.
package gate

import "encoding/json"

// Пути публичного REST API Gate Spot и USDT Perpetual.
const (
	spotCurrencyPairsEndpoint = "/api/v4/spot/currency_pairs"
	spotTickersEndpoint       = "/api/v4/spot/tickers"
	futuresContractsEndpoint  = "/api/v4/futures/usdt/contracts"
	futuresTickersEndpoint    = "/api/v4/futures/usdt/tickers"

	usdtAsset = "USDT"
)

// TickerStats содержит статистику фильтрации пакетных котировок Gate.
type TickerStats struct {
	Received int
	Valid    int
	Skipped  int
}

// apiErrorResponse описывает JSON-ошибку Gate API v4.
type apiErrorResponse struct {
	Label   string `json:"label"`
	Message string `json:"message"`
}

// spotCurrencyPair содержит структурированные метаданные Spot-пары.
type spotCurrencyPair struct {
	ID            string          `json:"id"`
	Base          string          `json:"base"`
	Quote         string          `json:"quote"`
	TradeStatus   string          `json:"trade_status"`
	Type          string          `json:"type"`
	DelistingTime json.RawMessage `json:"delisting_time"`
}

// futuresContract содержит метаданные контракта из /futures/usdt/contracts.
type futuresContract struct {
	Name             string          `json:"name"`
	Type             string          `json:"type"`
	QuantoMultiplier json.RawMessage `json:"quanto_multiplier"`
	Status           string          `json:"status"`
	InDelisting      *bool           `json:"in_delisting"`
}

// spotTicker содержит поля пакетной Spot-котировки.
type spotTicker struct {
	CurrencyPair  string          `json:"currency_pair"`
	HighestBid    json.RawMessage `json:"highest_bid"`
	LowestAsk     json.RawMessage `json:"lowest_ask"`
	BaseVolume    json.RawMessage `json:"base_volume"`
	QuoteVolume   json.RawMessage `json:"quote_volume"`
	ETFNetValue   json.RawMessage `json:"etf_net_value"`
	ETFPreNetValue json.RawMessage `json:"etf_pre_net_value"`
	ETFLeverage   json.RawMessage `json:"etf_leverage"`
}

// futuresTicker содержит лучшие цены и объёмы USDT Perpetual-контракта.
type futuresTicker struct {
	Contract        string          `json:"contract"`
	HighestBid      json.RawMessage `json:"highest_bid"`
	LowestAsk       json.RawMessage `json:"lowest_ask"`
	Volume24hBase   json.RawMessage `json:"volume_24h_base"`
	Volume24hQuote  json.RawMessage `json:"volume_24h_quote"`
	Volume24hSettle json.RawMessage `json:"volume_24h_settle"`
}
