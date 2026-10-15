package exchange

import (
	"context"

	"github.com/example/crypto-arbitrage-scanner/internal/market"
	"github.com/example/crypto-arbitrage-scanner/internal/price"
)

// Exchange представляет адаптер публичных рыночных данных криптовалютной биржи.
type Exchange interface {
	// Name возвращает стабильный внутренний идентификатор биржи.
	Name() string

	// DiscoverMarkets получает доступные торговые инструменты биржи.
	DiscoverMarkets(
		ctx context.Context,
	) ([]market.Market, error)

	// CollectTickers получает bid/ask для переданных рынков.
	CollectTickers(
		ctx context.Context,
		markets []market.Market,
	) ([]price.Ticker, error)
}

// ExchangeName является типизированным идентификатором поддерживаемой биржи.
type ExchangeName string

const (
	ExchangeBinance ExchangeName = "binance"
	ExchangeBybit   ExchangeName = "bybit"
	ExchangeMEXC    ExchangeName = "mexc"
	ExchangeOKX     ExchangeName = "okx"
	ExchangeGate    ExchangeName = "gate"
	ExchangeBitget  ExchangeName = "bitget"
)
