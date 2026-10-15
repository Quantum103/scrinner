package price

import (
	"context"

	"github.com/example/crypto-arbitrage-scanner/internal/market"
)

// Collector отвечает за получение данных о ценах известных рынков.
type Collector interface {
	// CollectTickers получает тикеры для переданного набора рынков.
	CollectTickers(
		ctx context.Context,
		markets []market.Market,
	) ([]Ticker, error)
}
