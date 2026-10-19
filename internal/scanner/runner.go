// Package scanner объединяет сбор данных, проверку свежести, детекцию и сохранение наблюдений.
package scanner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/example/crypto-arbitrage-scanner/internal/arbitrage"
	"github.com/example/crypto-arbitrage-scanner/internal/exchange"
	"github.com/example/crypto-arbitrage-scanner/internal/market"
	"github.com/example/crypto-arbitrage-scanner/internal/price"
	"github.com/example/crypto-arbitrage-scanner/internal/storage"
	"github.com/shopspring/decimal"
)

const defaultMaxConcurrentExchanges = 6

var ErrCycleAlreadyRunning = errors.New("предыдущий цикл сканирования ещё выполняется")

// Config содержит настройки одного интеграционного цикла сканирования.
type Config struct {
	MinSpreadPercent       decimal.Decimal
	MaxDataAge             time.Duration
	MaxConcurrentExchanges int
}

// ExchangeStatus описывает результат опроса одной биржи в текущем цикле.
type ExchangeStatus struct {
	Name            string
	Markets         int
	TickersReceived int
	SnapshotsSaved  int
	Error           string
}

// CycleResult описывает частичный или полный результат цикла.
type CycleResult struct {
	StartedAt             time.Time
	FinishedAt            time.Time
	Exchanges             []ExchangeStatus
	OpportunitiesDetected int
}

// Runner выполняет один непрерывный процесс сбора и сопоставления данных.
type Runner struct {
	adapters []exchange.Exchange
	db       *storage.DB
	logger   *slog.Logger
	config   Config
	detector *arbitrage.Detector
	running  atomic.Bool
}

// NewRunner проверяет настройки и сохраняет включённые адаптеры.
func NewRunner(
	adapters []exchange.Exchange,
	db *storage.DB,
	cfg Config,
	logger *slog.Logger,
) (*Runner, error) {
	if db == nil {
		return nil, fmt.Errorf("SQLite-хранилище не задано")
	}
	if cfg.MaxDataAge <= 0 {
		return nil, fmt.Errorf("max_data_age должен быть положительным")
	}
	if cfg.MinSpreadPercent.IsNegative() {
		return nil, fmt.Errorf("min_spread_percent не может быть отрицательным")
	}
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.MaxConcurrentExchanges <= 0 {
		cfg.MaxConcurrentExchanges = defaultMaxConcurrentExchanges
	}
	if cfg.MaxConcurrentExchanges > defaultMaxConcurrentExchanges {
		cfg.MaxConcurrentExchanges = defaultMaxConcurrentExchanges
	}
	detector, err := arbitrage.NewDetector(cfg.MinSpreadPercent, cfg.MaxDataAge)
	if err != nil {
		return nil, fmt.Errorf("создать детектор возможностей: %w", err)
	}
	copiedAdapters := append([]exchange.Exchange(nil), adapters...)
	return &Runner{
		adapters: copiedAdapters,
		db:       db,
		logger:   logger,
		config:   cfg,
		detector: detector,
	}, nil
}

type exchangeBatch struct {
	name    string
	markets []market.Market
	tickers []price.Ticker
	err     error
}

// RunCycle параллельно получает котировки, затем последовательно и атомарно сохраняет их по биржам.
func (r *Runner) RunCycle(ctx context.Context) (CycleResult, error) {
	if r == nil {
		return CycleResult{}, fmt.Errorf("scanner runner не задан")
	}
	if !r.running.CompareAndSwap(false, true) {
		return CycleResult{}, ErrCycleAlreadyRunning
	}
	defer r.running.Store(false)

	result := CycleResult{StartedAt: time.Now().UTC()}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if len(r.adapters) == 0 {
		result.FinishedAt = time.Now().UTC()
		r.logger.Warn("цикл пропущен: нет включённых адаптеров")
		return result, nil
	}

	batches := r.collectInParallel(ctx)
	if err := ctx.Err(); err != nil {
		result.FinishedAt = time.Now().UTC()
		return result, err
	}

	statusByIndex := make([]ExchangeStatus, len(batches))
	quotes := make([]arbitrage.Quote, 0)
	for i, batch := range batches {
		status := ExchangeStatus{Name: batch.name}
		if batch.err != nil {
			status.Error = batch.err.Error()
			statusByIndex[i] = status
			r.logger.Error("ошибка сбора биржи", "exchange", batch.name, "error", batch.err)
			continue
		}

		now := time.Now().UTC()
		marketByKey := make(map[string]market.Market, len(batch.markets))
		for _, item := range batch.markets {
			marketByKey[marketKey(item)] = item
		}
		inputs := make([]storage.MarketSnapshotInput, 0, len(batch.tickers))
		for _, ticker := range batch.tickers {
			if !ticker.IsFresh(now, r.config.MaxDataAge) {
				r.logger.Warn("пропущена устаревшая или некорректная котировка",
					"exchange", batch.name, "symbol", ticker.Symbol,
					"timestamp", ticker.Timestamp, "received_at", ticker.ReceivedAt,
					"exchange_timestamp", ticker.ExchangeTimestamp)
				continue
			}
			item, ok := marketByKey[tickerKey(ticker)]
			if !ok {
				r.logger.Warn("котировка не связана с рынком текущего ответа",
					"exchange", batch.name, "symbol", ticker.Symbol,
					"market_type", ticker.MarketType, "contract_type", ticker.ContractType)
				continue
			}
			inputs = append(inputs, storage.MarketSnapshotInput{
				Market: item,
				Snapshot: storage.PriceSnapshot{
					Timestamp: ticker.Timestamp, ReceivedAt: ticker.ReceivedAt,
					ExchangeTimestamp: ticker.ExchangeTimestamp,
					Bid:               ticker.Bid, Ask: ticker.Ask, Volume24h: ticker.Volume24h,
				},
			})
		}

		saved, err := r.db.PersistMarketSnapshots(ctx, batch.markets, inputs)
		if err != nil {
			status.Error = err.Error()
			statusByIndex[i] = status
			r.logger.Error("ошибка транзакционного сохранения данных биржи", "exchange", batch.name, "error", err)
			continue
		}
		status.Markets = len(batch.markets)
		status.TickersReceived = len(batch.tickers)
		status.SnapshotsSaved = len(saved)
		statusByIndex[i] = status
		for _, item := range saved {
			ticker := price.Ticker{
				Exchange: item.Market.Exchange, Symbol: item.Market.Symbol,
				MarketType: item.Market.MarketType, ContractType: item.Market.ContractType,
				Bid: item.Snapshot.Bid, Ask: item.Snapshot.Ask,
				Volume24h: item.Snapshot.Volume24h,
				Timestamp: item.Snapshot.Timestamp, ReceivedAt: item.Snapshot.ReceivedAt,
				ExchangeTimestamp: item.Snapshot.ExchangeTimestamp,
			}
			quotes = append(quotes, arbitrage.Quote{
				Market: item.Market, MarketID: item.MarketID,
				SnapshotID: item.SnapshotID, Ticker: ticker,
			})
		}
		r.logger.Info("биржа сохранена",
			"exchange", batch.name, "markets", status.Markets,
			"tickers", status.TickersReceived, "snapshots", status.SnapshotsSaved)
	}

	result.Exchanges = statusByIndex
	detectionTime := time.Now().UTC()
	opportunities := r.detector.Detect(quotes, detectionTime)
	if len(opportunities) > 0 {
		if err := storage.NewOpportunityRepository(r.db).InsertOpportunities(ctx, opportunities); err != nil {
			result.FinishedAt = time.Now().UTC()
			return result, fmt.Errorf("сохранить найденные возможности: %w", err)
		}
	}
	result.OpportunitiesDetected = len(opportunities)
	result.FinishedAt = time.Now().UTC()

	failed := 0
	for _, status := range result.Exchanges {
		if status.Error != "" {
			failed++
		}
	}
	r.logger.Info("цикл сканирования завершён",
		"exchanges_total", len(result.Exchanges),
		"exchanges_failed", failed,
		"markets_saved", sumMarkets(result.Exchanges),
		"snapshots_saved", sumSnapshots(result.Exchanges),
		"opportunities_detected", result.OpportunitiesDetected,
		"duration", result.FinishedAt.Sub(result.StartedAt))
	return result, nil
}

// Run повторяет циклы без перекрытия и ожидает интервал после завершения каждого цикла.
func (r *Runner) Run(ctx context.Context, interval time.Duration) error {
	if interval <= 0 {
		return fmt.Errorf("scan_interval должен быть положительным")
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := r.RunCycle(ctx); err != nil && !errors.Is(err, context.Canceled) {
			r.logger.Error("цикл сканирования завершился ошибкой", "error", err)
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (r *Runner) collectInParallel(ctx context.Context) []exchangeBatch {
	results := make([]exchangeBatch, len(r.adapters))
	if len(r.adapters) == 0 {
		return results
	}
	workerLimit := r.config.MaxConcurrentExchanges
	if workerLimit > len(r.adapters) {
		workerLimit = len(r.adapters)
	}
	semaphore := make(chan struct{}, workerLimit)
	var wg sync.WaitGroup
	for i, adapter := range r.adapters {
		i, adapter := i, adapter
		wg.Add(1)
		go func() {
			defer wg.Done()
			name := ""
			if adapter != nil {
				name = strings.TrimSpace(adapter.Name())
			}
			results[i] = exchangeBatch{name: name}
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				results[i].err = ctx.Err()
				return
			}
			results[i] = r.collectOne(ctx, adapter)
		}()
	}
	wg.Wait()
	return results
}

func (r *Runner) collectOne(ctx context.Context, adapter exchange.Exchange) exchangeBatch {
	if adapter == nil {
		return exchangeBatch{err: fmt.Errorf("адаптер не задан")}
	}
	name := strings.TrimSpace(adapter.Name())
	if name == "" {
		return exchangeBatch{err: fmt.Errorf("идентификатор биржи не задан")}
	}
	markets, err := adapter.DiscoverMarkets(ctx)
	if err != nil {
		return exchangeBatch{name: name, err: fmt.Errorf("получить список рынков: %w", err)}
	}

	validMarkets := make([]market.Market, 0, len(markets))
	knownMarkets := make(map[string]market.Market, len(markets))
	for _, item := range markets {
		normalized, err := market.NormalizeMarket(item)
		if err != nil {
			r.logger.Warn("пропущены некорректные метаданные рынка",
				"exchange", name, "symbol", item.Symbol, "error", err)
			continue
		}
		if !strings.EqualFold(normalized.Exchange, name) || !market.IsSupportedMarket(normalized) {
			r.logger.Warn("пропущен неподдерживаемый рынок",
				"exchange", name, "market_exchange", normalized.Exchange,
				"symbol", normalized.Symbol, "active", normalized.Active,
				"market_type", normalized.MarketType, "contract_type", normalized.ContractType)
			continue
		}
		key := marketKey(normalized)
		if _, exists := knownMarkets[key]; exists {
			r.logger.Warn("дублирующиеся метаданные рынка пропущены",
				"exchange", name, "symbol", normalized.Symbol,
				"market_type", normalized.MarketType, "contract_type", normalized.ContractType)
			continue
		}
		knownMarkets[key] = normalized
		validMarkets = append(validMarkets, normalized)
	}
	if len(validMarkets) == 0 {
		return exchangeBatch{name: name, err: fmt.Errorf("адаптер не вернул активных поддерживаемых Spot USDT или Perpetual рынков")}
	}

	tickers, err := adapter.CollectTickers(ctx, validMarkets)
	if err != nil {
		return exchangeBatch{name: name, err: fmt.Errorf("получить пакет котировок: %w", err)}
	}
	validTickers := make([]price.Ticker, 0, len(tickers))
	seen := make(map[string]struct{}, len(tickers))
	for _, ticker := range tickers {
		key := tickerKey(ticker)
		item, exists := knownMarkets[key]
		if !exists || !ticker.IsValid() {
			r.logger.Warn("котировка не относится к поддерживаемому рынку ответа",
				"exchange", name, "symbol", ticker.Symbol,
				"market_type", ticker.MarketType, "contract_type", ticker.ContractType)
			continue
		}
		if _, duplicate := seen[key]; duplicate {
			r.logger.Warn("дублирующаяся котировка пропущена", "exchange", name, "symbol", ticker.Symbol)
			continue
		}
		// Используем исходный символ из метаданных рынка.
		ticker.Symbol = item.Symbol
		ticker.Exchange = item.Exchange
		seen[key] = struct{}{}
		validTickers = append(validTickers, ticker)
	}
	sort.Slice(validMarkets, func(i, j int) bool { return marketKey(validMarkets[i]) < marketKey(validMarkets[j]) })
	sort.Slice(validTickers, func(i, j int) bool { return tickerKey(validTickers[i]) < tickerKey(validTickers[j]) })
	return exchangeBatch{name: name, markets: validMarkets, tickers: validTickers}
}

func marketKey(item market.Market) string {
	return strings.ToLower(strings.TrimSpace(item.Exchange)) + "|" +
		strings.ToUpper(strings.TrimSpace(item.Symbol)) + "|" +
		string(item.MarketType) + "|" + string(item.ContractType)
}

func tickerKey(item price.Ticker) string {
	return strings.ToLower(strings.TrimSpace(item.Exchange)) + "|" +
		strings.ToUpper(strings.TrimSpace(item.Symbol)) + "|" +
		string(item.MarketType) + "|" + string(item.ContractType)
}

func sumMarkets(statuses []ExchangeStatus) int {
	total := 0
	for _, item := range statuses {
		total += item.Markets
	}
	return total
}

func sumSnapshots(statuses []ExchangeStatus) int {
	total := 0
	for _, item := range statuses {
		total += item.SnapshotsSaved
	}
	return total
}
