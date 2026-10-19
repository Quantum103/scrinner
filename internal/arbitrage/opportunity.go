package arbitrage

import (
	"time"

	"github.com/example/crypto-arbitrage-scanner/internal/market"
	"github.com/shopspring/decimal"
)

// Opportunity описывает одно историческое наблюдение валового ценового спреда.
type Opportunity struct {
	// Timestamp — локальное время обнаружения возможности.
	Timestamp time.Time

	// Symbol — нормализованная пара, например BTC/USDT.
	Symbol     string
	BaseAsset  string
	QuoteAsset string

	BuyMarketID     int64
	BuySymbol       string
	BuyExchange     string
	BuyMarketType   market.MarketType
	BuyContractType market.ContractType
	BuyPrice        decimal.Decimal
	BuySnapshotID   int64

	SellMarketID     int64
	SellSymbol       string
	SellExchange     string
	SellMarketType   market.MarketType
	SellContractType market.ContractType
	SellPrice        decimal.Decimal
	SellSnapshotID   int64

	SpreadPercent decimal.Decimal
}
