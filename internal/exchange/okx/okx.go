// Package okx реализует публичный REST-адаптер рыночных данных OKX.
package okx

import (
	"context"
	"log/slog"

	"github.com/example/crypto-arbitrage-scanner/internal/exchange"
	"github.com/example/crypto-arbitrage-scanner/internal/market"
	"github.com/example/crypto-arbitrage-scanner/internal/price"
)

// Adapter получает публичные Spot и линейные USDT SWAP-данные OKX.
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

// NewDefault создаёт адаптер с официальным публичным REST API OKX.
func NewDefault(logger *slog.Logger) *Adapter {
	return New(NewDefaultClient(), logger)
}

// Name возвращает стабильный внутренний идентификатор биржи.
func (a *Adapter) Name() string {
	return string(exchange.ExchangeOKX)
}

// DiscoverMarkets загружает поддерживаемые Spot USDT и линейные USDT SWAP-рынки.
func (a *Adapter) DiscoverMarkets(ctx context.Context) ([]market.Market, error) {
	a.logger.Info("starting OKX adapter")
	return a.discoverMarkets(ctx)
}

// CollectTickers получает пакетные bid/ask для переданных рынков.
func (a *Adapter) CollectTickers(ctx context.Context, markets []market.Market) ([]price.Ticker, error) {
	tickers, _, err := a.collectTickersWithStats(ctx, markets)
	return tickers, err
}

// CollectTickersWithStats дополнительно возвращает статистику проверки котировок.
func (a *Adapter) CollectTickersWithStats(
	ctx context.Context,
	markets []market.Market,
) ([]price.Ticker, TickerStats, error) {
	return a.collectTickersWithStats(ctx, markets)
}
