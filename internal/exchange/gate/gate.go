// Package gate реализует публичный REST-адаптер рыночных данных Gate API v4.
package gate

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/example/crypto-arbitrage-scanner/internal/exchange"
	"github.com/example/crypto-arbitrage-scanner/internal/market"
	"github.com/example/crypto-arbitrage-scanner/internal/price"
)

// Adapter загружает публичные Spot- и USDT Perpetual-рынки Gate.
type Adapter struct {
	client *Client
	logger *slog.Logger

	mu                   sync.Mutex
	spotTickerCache      []spotTicker
	spotTickerReceivedAt time.Time
	hasSpotTickerCache   bool
}

var _ exchange.Exchange = (*Adapter)(nil)

// New создаёт адаптер на основе HTTP-клиента Gate.
func New(client *Client, logger *slog.Logger) *Adapter {
	if logger == nil {
		logger = slog.Default()
	}
	if client == nil {
		client = NewDefaultClient()
	}
	return &Adapter{client: client, logger: logger}
}

// NewDefault создаёт адаптер публичного Gate API с настройками по умолчанию.
func NewDefault(logger *slog.Logger) *Adapter {
	return New(NewDefaultClient(), logger)
}

// Name возвращает стабильный внутренний идентификатор биржи.
func (a *Adapter) Name() string {
	return string(exchange.ExchangeGate)
}

// DiscoverMarkets загружает активные Spot USDT и линейные USDT Perpetual-рынки.
func (a *Adapter) DiscoverMarkets(ctx context.Context) ([]market.Market, error) {
	a.logger.Info("загрузка рынков Gate")
	return a.discoverMarkets(ctx)
}

// CollectTickers загружает лучшие bid/ask для переданных рынков.
func (a *Adapter) CollectTickers(ctx context.Context, markets []market.Market) ([]price.Ticker, error) {
	tickers, _, err := a.collectTickersWithStats(ctx, markets)
	return tickers, err
}

// CollectTickersWithStats дополнительно возвращает статистику полученных котировок.
func (a *Adapter) CollectTickersWithStats(
	ctx context.Context,
	markets []market.Market,
) ([]price.Ticker, TickerStats, error) {
	return a.collectTickersWithStats(ctx, markets)
}
