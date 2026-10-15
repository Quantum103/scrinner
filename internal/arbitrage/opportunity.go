package arbitrage

import (
	"time"

	"github.com/example/crypto-arbitrage-scanner/internal/market"
	"github.com/shopspring/decimal"
)

// Opportunity описывает конкретное ценовое расхождение между двумя рынками.
type Opportunity struct {
	Timestamp time.Time

	Symbol     string
	BaseAsset  string
	QuoteAsset string

	BuyMarketID     int64
	BuyExchange     string
	BuyMarketType   market.MarketType
	BuyContractType market.ContractType
	BuyPrice        decimal.Decimal

	SellMarketID     int64
	SellExchange     string
	SellMarketType   market.MarketType
	SellContractType market.ContractType
	SellPrice        decimal.Decimal

	SpreadPercent decimal.Decimal
}
