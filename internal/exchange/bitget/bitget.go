// Package bitget реализует публичный REST-адаптер рыночных данных Bitget API V3.
package bitget

import (
	"context"
	"log/slog"
	"strings"

	"github.com/example/crypto-arbitrage-scanner/internal/exchange"
	"github.com/example/crypto-arbitrage-scanner/internal/market"
	"github.com/example/crypto-arbitrage-scanner/internal/price"
)

// Adapter загружает Spot USDT и бессрочные USDT-FUTURES рынки Bitget.
type Adapter struct {
	client *Client
	logger *slog.Logger
}

// TickerStats содержит количество обработанных котировок.
type TickerStats struct {
	Received int
	Valid    int
	Skipped  int
}

var _ exchange.Exchange = (*Adapter)(nil)

// New создаёт адаптер на основе переиспользуемого HTTP-клиента Bitget.
func New(client *Client, logger *slog.Logger) *Adapter {
	if logger == nil {
		logger = slog.Default()
	}
	if client == nil {
		client = NewDefaultClient()
	}
	return &Adapter{client: client, logger: logger}
}

// NewDefault создаёт адаптер Bitget с параметрами HTTP-клиента по умолчанию.
func NewDefault(logger *slog.Logger) *Adapter {
	return New(NewDefaultClient(), logger)
}

// Name возвращает стабильный внутренний идентификатор биржи.
func (a *Adapter) Name() string {
	return string(exchange.ExchangeBitget)
}

// DiscoverMarkets получает активные Spot USDT и USDT Perpetual рынки.
func (a *Adapter) DiscoverMarkets(ctx context.Context) ([]market.Market, error) {
	var spotInstruments []instrument
	_, err := a.client.getJSON(ctx, "получить Spot instruments", instrumentsEndpoint, spotCategory, &spotInstruments)
	if err != nil {
		return nil, err
	}
	if len(spotInstruments) == 0 {
		return nil, fmtEmptyResponse(instrumentsEndpoint, spotCategory)
	}

	var futuresInstruments []instrument
	if _, err := a.client.getJSON(ctx, "получить USDT Futures instruments", instrumentsEndpoint, futuresCategory, &futuresInstruments); err != nil {
		return nil, err
	}
	if len(futuresInstruments) == 0 {
		return nil, fmtEmptyResponse(instrumentsEndpoint, futuresCategory)
	}

	markets := make([]market.Market, 0, len(spotInstruments)+len(futuresInstruments))
	seen := make(map[string]struct{}, cap(markets))
	for _, item := range spotInstruments {
		converted, supported, err := convertInstrument(item, spotCategory)
		if err != nil {
			a.logger.Warn("инструмент Bitget пропущен", "category", spotCategory, "symbol", item.Symbol, "error", err)
			continue
		}
		if !supported {
			continue
		}
		key := marketKey(converted)
		if _, exists := seen[key]; exists {
			a.logger.Warn("повторный инструмент Bitget пропущен", "category", spotCategory, "symbol", converted.Symbol)
			continue
		}
		seen[key] = struct{}{}
		markets = append(markets, converted)
	}
	for _, item := range futuresInstruments {
		converted, supported, err := convertInstrument(item, futuresCategory)
		if err != nil {
			a.logger.Warn("инструмент Bitget пропущен", "category", futuresCategory, "symbol", item.Symbol, "error", err)
			continue
		}
		if !supported {
			continue
		}
		key := marketKey(converted)
		if _, exists := seen[key]; exists {
			a.logger.Warn("повторный инструмент Bitget пропущен", "category", futuresCategory, "symbol", converted.Symbol)
			continue
		}
		seen[key] = struct{}{}
		markets = append(markets, converted)
	}
	if len(markets) == 0 {
		return nil, fmtEmptyMarkets()
	}
	a.logger.Info("рынки Bitget загружены", "spot_usdt", countSpot(markets), "usdt_perpetual", countPerpetual(markets), "total", len(markets))
	return markets, nil
}

// CollectTickers возвращает лучшие bid/ask только для переданных активных рынков Bitget.
func (a *Adapter) CollectTickers(ctx context.Context, markets []market.Market) ([]price.Ticker, error) {
	tickers, _, err := a.CollectTickersWithStats(ctx, markets)
	return tickers, err
}

// CollectTickersWithStats собирает пакетные котировки и возвращает статистику фильтрации.
func (a *Adapter) CollectTickersWithStats(ctx context.Context, markets []market.Market) ([]price.Ticker, TickerStats, error) {
	spotMarkets := make(map[string]market.Market)
	futuresMarkets := make(map[string]market.Market)
	for _, item := range markets {
		normalized, err := market.NormalizeMarket(item)
		if err != nil || !strings.EqualFold(strings.TrimSpace(normalized.Exchange), string(exchange.ExchangeBitget)) || !normalized.IsSupported() {
			continue
		}
		symbol := strings.ToUpper(strings.TrimSpace(normalized.Symbol))
		if symbol == "" {
			continue
		}
		switch {
		case normalized.IsSpot():
			spotMarkets[symbol] = normalized
		case normalized.IsPerpetual():
			futuresMarkets[symbol] = normalized
		}
	}

	result := make([]price.Ticker, 0, len(spotMarkets)+len(futuresMarkets))
	stats := TickerStats{}
	if len(spotMarkets) == 0 && len(futuresMarkets) == 0 {
		return result, stats, nil
	}

	type categoryResult struct {
		tickers []price.Ticker
		stats   TickerStats
		err     error
		label   string
	}
	enabledCategories := 0
	results := make(chan categoryResult, 2)
	requestCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	if len(spotMarkets) > 0 {
		enabledCategories++
		go func() {
			tickers, tickStats, err := a.fetchTickers(requestCtx, spotCategory, spotMarkets, market.MarketTypeSpot, market.ContractTypeSpot)
			results <- categoryResult{tickers: tickers, stats: tickStats, err: err, label: "Spot"}
		}()
	}
	if len(futuresMarkets) > 0 {
		enabledCategories++
		go func() {
			tickers, tickStats, err := a.fetchTickers(requestCtx, futuresCategory, futuresMarkets, market.MarketTypeFutures, market.ContractTypePerpetual)
			results <- categoryResult{tickers: tickers, stats: tickStats, err: err, label: "USDT Perpetual"}
		}()
	}

	for i := 0; i < enabledCategories; i++ {
		category := <-results
		if category.err != nil {
			cancel()
			return nil, stats, fmt.Errorf("получить Bitget %s tickers: %w", category.label, category.err)
		}
		result = append(result, category.tickers...)
		stats.Received += category.stats.Received
		stats.Valid += category.stats.Valid
		stats.Skipped += category.stats.Skipped
	}
	a.logger.Info("котировки Bitget обработаны", "received", stats.Received, "valid", stats.Valid, "skipped", stats.Skipped)
	return result, stats, nil
}
