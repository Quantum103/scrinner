// Package bybit реализует публичный REST-адаптер рыночных данных Bybit V5.
package bybit

import (
	"context"
	"log/slog"

	"github.com/example/crypto-arbitrage-scanner/internal/exchange"
	"github.com/example/crypto-arbitrage-scanner/internal/market"
	"github.com/example/crypto-arbitrage-scanner/internal/price"
)

// Adapter реализует публичный рыночный адаптер Bybit V5.
type Adapter struct {
	client *Client
	logger *slog.Logger
}

var _ exchange.Exchange = (*Adapter)(nil)

// New создаёт адаптер на основе уже созданного HTTP-клиента.
func New(client *Client, logger *slog.Logger) *Adapter {
	if logger == nil {
		logger = slog.Default()
	}
	return &Adapter{client: client, logger: logger}
}

// NewDefault создаёт адаптер с публичным production endpoint Bybit.
func NewDefault(logger *slog.Logger) *Adapter {
	return New(NewDefaultClient(), logger)
}

// Name возвращает стабильный идентификатор биржи.
func (a *Adapter) Name() string {
	return "bybit"
}

// DiscoverMarkets получает только Spot USDT и Linear USDT Perpetual рынки.
func (a *Adapter) DiscoverMarkets(ctx context.Context) ([]market.Market, error) {
	a.logger.Info("starting Bybit adapter")
	return a.discoverMarkets(ctx)
}

// CollectTickers получает bid/ask и 24h volume для переданных Bybit рынков.
func (a *Adapter) CollectTickers(ctx context.Context, markets []market.Market) ([]price.Ticker, error) {
	return a.collectTickers(ctx, markets)
}

// CollectTickersWithStats получает тикеры и дополнительно возвращает статистику фильтрации.
func (a *Adapter) CollectTickersWithStats(ctx context.Context, markets []market.Market) ([]price.Ticker, TickerStats, error) {
	return a.collectTickersWithStats(ctx, markets)
}
