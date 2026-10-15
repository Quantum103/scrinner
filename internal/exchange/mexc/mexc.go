// Package mexc реализует публичный REST-адаптер рыночных данных MEXC.
package mexc

import (
	"context"
	"log/slog"
	"sync"

	"github.com/example/crypto-arbitrage-scanner/internal/exchange"
	"github.com/example/crypto-arbitrage-scanner/internal/market"
	"github.com/example/crypto-arbitrage-scanner/internal/price"
	"github.com/shopspring/decimal"
)

// Adapter получает Spot и бессрочные USDT-контракты через публичный REST API MEXC.
type Adapter struct {
	client *Client
	logger *slog.Logger

	mu            sync.RWMutex
	contractSizes map[string]decimal.Decimal
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
	return &Adapter{
		client:        client,
		logger:        logger,
		contractSizes: make(map[string]decimal.Decimal),
	}
}

// NewDefault создаёт адаптер с официальными публичными endpoints MEXC.
func NewDefault(logger *slog.Logger) *Adapter {
	return New(NewDefaultClient(), logger)
}

// Name возвращает стабильный внутренний идентификатор биржи.
func (a *Adapter) Name() string {
	return string(exchange.ExchangeMEXC)
}

// DiscoverMarkets возвращает поддерживаемые Spot USDT и USDT Perpetual рынки.
func (a *Adapter) DiscoverMarkets(ctx context.Context) ([]market.Market, error) {
	a.logger.Info("starting MEXC adapter")
	return a.discoverMarkets(ctx)
}

// CollectTickers получает лучшие bid/ask для ранее найденных рынков.
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
