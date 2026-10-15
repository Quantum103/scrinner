// Package binance реализует публичный REST-адаптер рыночных данных Binance.
package binance

import (
	"context"
	"log/slog"

	"github.com/example/crypto-arbitrage-scanner/internal/exchange"
	"github.com/example/crypto-arbitrage-scanner/internal/market"
	"github.com/example/crypto-arbitrage-scanner/internal/price"
)

// Adapter реализует получение Spot и USDⓈ-M Perpetual данных Binance.
type Adapter struct {
	client *Client
	logger *slog.Logger
}

var _ exchange.Exchange = (*Adapter)(nil)

// New создаёт адаптер на основе переданного HTTP-клиента.
func New(client *Client, logger *slog.Logger) *Adapter {
	if logger == nil {
		logger = slog.Default()
	}
	if client == nil {
		client = NewDefaultClient()
	}
	return &Adapter{client: client, logger: logger}
}

// NewDefault создаёт адаптер с официальными публичными endpoints Binance.
func NewDefault(logger *slog.Logger) *Adapter {
	return New(NewDefaultClient(), logger)
}

// Name возвращает стабильный идентификатор биржи.
func (a *Adapter) Name() string {
	return "binance"
}

// DiscoverMarkets получает только активные Spot USDT и USDⓈ-M USDT Perpetual рынки.
func (a *Adapter) DiscoverMarkets(ctx context.Context) ([]market.Market, error) {
	a.logger.Info("starting Binance adapter")
	return a.discoverMarkets(ctx)
}

// CollectTickers получает лучшие bid/ask для ранее найденных рынков.
func (a *Adapter) CollectTickers(ctx context.Context, markets []market.Market) ([]price.Ticker, error) {
	return a.collectTickers(ctx, markets)
}

// CollectTickersWithStats дополнительно возвращает статистику проверки тикеров.
func (a *Adapter) CollectTickersWithStats(
	ctx context.Context,
	markets []market.Market,
) ([]price.Ticker, TickerStats, error) {
	return a.collectTickersWithStats(ctx, markets)
}
