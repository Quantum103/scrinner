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
	Name              string
	Duration          time.Duration
	Markets           int
	MetadataRefreshed bool
	TickersReceived   int
	TickersAccepted   int
	TickersRejected   int
	SnapshotsSaved    int
	FailureStage      string
	Error             string
}

// CycleResult описывает частичный или полный результат цикла.
type CycleResult struct {
	ID                    string
	Status                string
	StartedAt             time.Time
	FinishedAt            time.Time
	CollectionDuration    time.Duration
	ExchangesEnabled      int
	ExchangesSucceeded    int
	ExchangesFailed       int
	MarketsDiscovered     int
	MetadataRefreshes     int
	TickersReceived       int
	TickersAccepted       int
	TickersRejected       int
	SnapshotsSaved        int
	OpportunitiesDetected int
	OpportunitiesSaved    int
	PersistenceErrors     int
	Error                 string
	Exchanges             []ExchangeStatus
}

const (
	CycleStatusRunning          = "running"
	CycleStatusSuccess          = "success"
	CycleStatusPartial          = "partial"
	CycleStatusNoData           = "no_data"
	CycleStatusCollectionFailed = "collection_failed"
	CycleStatusSaveFailed       = "save_failed"
	CycleStatusCancelled        = "cancelled"
)

const defaultMarketRefreshInterval = 15 * time.Minute

type marketCacheEntry struct {
	markets     []market.Market
	refreshedAt time.Time
}

// Runner выполняет один непрерывный процесс сбора и сопоставления данных.
type Runner struct {
	adapters              []exchange.Exchange
	db                    *storage.DB
	logger                *slog.Logger
	config                Config
	detector              *arbitrage.Detector
	running               atomic.Bool
	marketCacheMu         sync.Mutex
	marketCache           map[string]marketCacheEntry
	marketRefreshInterval time.Duration
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
		adapters:              copiedAdapters,
		db:                    db,
		logger:                logger,
		config:                cfg,
		detector:              detector,
		marketCache:           make(map[string]marketCacheEntry),
		marketRefreshInterval: defaultMarketRefreshInterval,
	}, nil
}

type exchangeBatch struct {
	name              string
	duration          time.Duration
	markets           []market.Market
	tickers           []price.Ticker
	tickersReceived   int
	tickersRejected   int
	metadataRefreshed bool
	failureStage      string
	err               error
}

type completedExchangeBatch struct {
	index       int
	batch       exchangeBatch
	completedAt time.Time
}

// RunCycle параллельно получает котировки, сохраняет только свежие снимки и записывает историю возможностей.
func (r *Runner) RunCycle(ctx context.Context) (CycleResult, error) {
	if r == nil {
		return CycleResult{}, fmt.Errorf("scanner runner не задан")
	}
	if ctx == nil {
		return CycleResult{}, fmt.Errorf("контекст сканирования не задан")
	}
	if !r.running.CompareAndSwap(false, true) {
		return CycleResult{}, ErrCycleAlreadyRunning
	}
	defer r.running.Store(false)

	startedAt := time.Now().UTC()
	result := CycleResult{
		ID:               startedAt.Format("20060102T150405.000000000Z"),
		Status:           CycleStatusRunning,
		StartedAt:        startedAt,
		ExchangesEnabled: len(r.adapters),
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := r.db.StartScanCycle(ctx, storage.ScanCycle{
		ID: result.ID, StartedAt: result.StartedAt, EnabledExchanges: result.ExchangesEnabled,
		MinSpreadPercent: r.config.MinSpreadPercent,
	}); err != nil {
		result.Status = CycleStatusSaveFailed
		result.FinishedAt = time.Now().UTC()
		result.Error = err.Error()
		r.logCycle(result)
		return result, fmt.Errorf("зарегистрировать начало цикла сканирования: %w", err)
	}
	var cycleFinalizeErr error
	finish := func(status string) {
		result.Status = status
		result.FinishedAt = time.Now().UTC()
		result.ExchangesFailed = 0
		for _, exchangeStatus := range result.Exchanges {
			if exchangeStatus.Error != "" {
				result.ExchangesFailed++
			}
		}
		result.ExchangesSucceeded = len(result.Exchanges) - result.ExchangesFailed
		saveCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cycle := storage.ScanCycle{
			ID: result.ID, StartedAt: result.StartedAt, FinishedAt: result.FinishedAt,
			Status: result.Status, EnabledExchanges: result.ExchangesEnabled,
			SucceededExchanges: result.ExchangesSucceeded, FailedExchanges: result.ExchangesFailed,
			MarketsDiscovered: result.MarketsDiscovered, TickersReceived: result.TickersReceived,
			TickersAccepted: result.TickersAccepted, TickersRejected: result.TickersRejected,
			SnapshotsSaved: result.SnapshotsSaved, OpportunitiesDetected: result.OpportunitiesDetected,
			OpportunitiesSaved: result.OpportunitiesSaved, PersistenceErrors: result.PersistenceErrors,
			MinSpreadPercent: r.config.MinSpreadPercent, Error: result.Error,
		}
		if err := r.db.FinishScanCycle(saveCtx, cycle); err != nil {
			cycleFinalizeErr = fmt.Errorf("сохранить итог цикла %s: %w", result.ID, err)
			result.Status = CycleStatusSaveFailed
			result.PersistenceErrors++
			if result.Error == "" {
				result.Error = cycleFinalizeErr.Error()
			} else {
				result.Error += "; " + cycleFinalizeErr.Error()
			}
		}
		r.logCycle(result)
	}
	if len(r.adapters) == 0 {
		result.Error = "нет включённых адаптеров бирж"
		finish(CycleStatusNoData)
		return result, cycleFinalizeErr
	}

	collectionStartedAt := time.Now()
	completedBatches := make(chan completedExchangeBatch, len(r.adapters))
	workerLimit := r.config.MaxConcurrentExchanges
	if workerLimit > len(r.adapters) {
		workerLimit = len(r.adapters)
	}
	semaphore := make(chan struct{}, workerLimit)
	for index, adapter := range r.adapters {
		index, adapter := index, adapter
		go func() {
			started := time.Now()
			name := ""
			if adapter != nil {
				name = strings.TrimSpace(adapter.Name())
			}
			select {
			case semaphore <- struct{}{}:
				batch := r.collectOne(ctx, adapter, result.ID)
				batch.duration = time.Since(started)
				completedBatches <- completedExchangeBatch{index: index, batch: batch, completedAt: time.Now()}
			case <-ctx.Done():
				completedBatches <- completedExchangeBatch{
					index: index,
					batch: exchangeBatch{
						name: name, duration: time.Since(started),
						failureStage: "cancelled", err: ctx.Err(),
					},
					completedAt: time.Now(),
				}
			}
		}()
	}

	statuses := make([]ExchangeStatus, len(r.adapters))
	latestQuotes := make(map[string]arbitrage.Quote)
	detectedOpportunityKeys := make(map[string]struct{})
	collectionFinishedAt := collectionStartedAt
	opportunityRepo := storage.NewOpportunityRepository(r.db)

	// Обрабатываем каждую биржу сразу после её ответа, не дожидаясь самой медленной.
	// Иначе ранние REST-ответы стареют, пока собираются остальные биржи.
	for received := 0; received < len(r.adapters); received++ {
		completed := <-completedBatches
		batch := completed.batch
		i := completed.index
		if completed.completedAt.After(collectionFinishedAt) {
			collectionFinishedAt = completed.completedAt
		}
		if i < 0 || i >= len(statuses) {
			result.PersistenceErrors++
			if result.Error == "" {
				result.Error = "получен результат неизвестного адаптера"
			}
			continue
		}

		status := ExchangeStatus{
			Name:              batch.name,
			Duration:          batch.duration,
			Markets:           len(batch.markets),
			MetadataRefreshed: batch.metadataRefreshed,
			TickersReceived:   batch.tickersReceived,
			TickersRejected:   batch.tickersRejected,
			FailureStage:      batch.failureStage,
		}
		if batch.err != nil {
			status.Error = batch.err.Error()
		}
		statuses[i] = status

		result.MarketsDiscovered += len(batch.markets)
		result.TickersReceived += batch.tickersReceived
		result.TickersRejected += batch.tickersRejected
		if batch.metadataRefreshed {
			result.MetadataRefreshes++
		}

		if ctx.Err() != nil {
			if status.Error == "" {
				status.FailureStage = "cancelled"
				status.Error = ctx.Err().Error()
			}
			statuses[i] = status
			continue
		}
		if batch.err != nil {
			r.logger.Error("ошибка сбора биржи",
				"cycle_id", result.ID, "exchange", batch.name,
				"stage", batch.failureStage, "error", batch.err)
			continue
		}

		now := time.Now().UTC()
		marketByKey := make(map[string]market.Market, len(batch.markets))
		for _, item := range batch.markets {
			marketByKey[marketKey(item)] = item
		}
		inputs := make([]storage.MarketSnapshotInput, 0, len(batch.tickers))
		freshnessRejected := 0
		staleExamples := make([]string, 0, 3)
		for _, ticker := range batch.tickers {
			if !ticker.IsFresh(now, r.config.MaxDataAge) {
				status.TickersRejected++
				result.TickersRejected++
				freshnessRejected++
				if len(staleExamples) < 3 {
					staleExamples = append(staleExamples, ticker.Symbol)
				}
				continue
			}
			item, ok := marketByKey[tickerKey(ticker)]
			if !ok {
				status.TickersRejected++
				result.TickersRejected++
				r.logger.Warn("котировка не связана с рынком текущего ответа",
					"cycle_id", result.ID, "exchange", batch.name,
					"symbol", ticker.Symbol, "market_type", ticker.MarketType,
					"contract_type", ticker.ContractType)
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
		status.TickersAccepted = len(inputs)
		result.TickersAccepted += len(inputs)
		if freshnessRejected > 0 {
			r.logger.Warn("котировки пропущены по проверке свежести",
				"cycle_id", result.ID, "exchange", batch.name,
				"rejected", freshnessRejected, "accepted", len(inputs),
				"max_data_age", r.config.MaxDataAge, "example_symbols", staleExamples)
		}

		saved, err := r.db.PersistMarketSnapshots(ctx, batch.markets, inputs)
		if err != nil {
			status.FailureStage = "persist_snapshots"
			status.Error = err.Error()
			statuses[i] = status
			result.PersistenceErrors++
			r.logger.Error("ошибка транзакционного сохранения данных биржи",
				"cycle_id", result.ID, "exchange", batch.name, "error", err)
			continue
		}
		status.SnapshotsSaved = len(saved)
		result.SnapshotsSaved += len(saved)

		if len(inputs) == 0 {
			status.FailureStage = "freshness"
			status.Error = "биржа не предоставила свежих корректных котировок"
			statuses[i] = status
			r.logger.Warn("биржа не предоставила свежих корректных котировок",
				"cycle_id", result.ID, "exchange", batch.name,
				"markets", status.Markets, "tickers_received", status.TickersReceived,
				"tickers_rejected", status.TickersRejected)
			continue
		}

		for _, item := range saved {
			ticker := price.Ticker{
				Exchange: item.Market.Exchange, Symbol: item.Market.Symbol,
				MarketType: item.Market.MarketType, ContractType: item.Market.ContractType,
				Bid: item.Snapshot.Bid, Ask: item.Snapshot.Ask,
				Volume24h: item.Snapshot.Volume24h,
				Timestamp: item.Snapshot.Timestamp, ReceivedAt: item.Snapshot.ReceivedAt,
				ExchangeTimestamp: item.Snapshot.ExchangeTimestamp,
			}
			latestQuotes[marketKey(item.Market)] = arbitrage.Quote{
				Market: item.Market, MarketID: item.MarketID,
				SnapshotID: item.SnapshotID, Ticker: ticker,
			}
		}
		statuses[i] = status
		r.logger.Info("биржа обработана",
			"cycle_id", result.ID, "exchange", batch.name,
			"markets", status.Markets, "metadata_refreshed", status.MetadataRefreshed,
			"tickers_received", status.TickersReceived,
			"tickers_accepted", status.TickersAccepted,
			"tickers_rejected", status.TickersRejected,
			"snapshots_saved", status.SnapshotsSaved, "duration", status.Duration)

		detectAt := time.Now().UTC()
		freshQuotes := make([]arbitrage.Quote, 0, len(latestQuotes))
		for _, quote := range latestQuotes {
			if quote.Ticker.IsFresh(detectAt, r.config.MaxDataAge) {
				freshQuotes = append(freshQuotes, quote)
			}
		}
		opportunities := r.detector.Detect(freshQuotes, detectAt)
		newOpportunities := make([]arbitrage.Opportunity, 0, len(opportunities))
		for _, opportunity := range opportunities {
			key := fmt.Sprintf("%d:%d", opportunity.BuySnapshotID, opportunity.SellSnapshotID)
			if _, exists := detectedOpportunityKeys[key]; exists {
				continue
			}
			detectedOpportunityKeys[key] = struct{}{}
			opportunity.CycleID = result.ID
			newOpportunities = append(newOpportunities, opportunity)
		}
		if len(newOpportunities) > 0 {
			result.OpportunitiesDetected += len(newOpportunities)
			if err := opportunityRepo.InsertOpportunities(ctx, newOpportunities); err != nil {
				result.PersistenceErrors++
				message := fmt.Sprintf("сохранить найденные возможности: %v", err)
				if result.Error == "" {
					result.Error = message
				} else {
					result.Error += "; " + message
				}
				r.logger.Error("ошибка сохранения возможностей",
					"cycle_id", result.ID, "count", len(newOpportunities), "error", err)
			} else {
				result.OpportunitiesSaved += len(newOpportunities)
			}
		}
	}
	result.CollectionDuration = collectionFinishedAt.Sub(collectionStartedAt)
	result.Exchanges = statuses

	if err := ctx.Err(); err != nil {
		result.Error = err.Error()
		finish(CycleStatusCancelled)
		return result, errors.Join(err, cycleFinalizeErr)
	}

	failed := 0
	for _, status := range result.Exchanges {
		if status.Error != "" {
			failed++
		}
	}
	result.ExchangesFailed = failed
	result.ExchangesSucceeded = len(result.Exchanges) - failed

	switch {
	case result.PersistenceErrors > 0:
		finish(CycleStatusSaveFailed)
	case result.SnapshotsSaved == 0 && failed > 0:
		finish(CycleStatusCollectionFailed)
	case result.SnapshotsSaved == 0:
		finish(CycleStatusNoData)
	case failed > 0:
		finish(CycleStatusPartial)
	default:
		finish(CycleStatusSuccess)
	}
	return result, cycleFinalizeErr
}

// logCycle выводит сводку цикла без перечисления всех тикеров.
func (r *Runner) logCycle(result CycleResult) {
	attrs := []any{
		"cycle_id", result.ID,
		"status", result.Status,
		"started_at", result.StartedAt,
		"finished_at", result.FinishedAt,
		"duration", result.FinishedAt.Sub(result.StartedAt),
		"collection_duration", result.CollectionDuration,
		"exchanges_enabled", result.ExchangesEnabled,
		"exchanges_succeeded", result.ExchangesSucceeded,
		"exchanges_failed", result.ExchangesFailed,
		"markets_discovered", result.MarketsDiscovered,
		"metadata_refreshes", result.MetadataRefreshes,
		"tickers_received", result.TickersReceived,
		"tickers_accepted", result.TickersAccepted,
		"tickers_rejected", result.TickersRejected,
		"snapshots_saved", result.SnapshotsSaved,
		"opportunities_detected", result.OpportunitiesDetected,
		"opportunities_saved", result.OpportunitiesSaved,
		"persistence_errors", result.PersistenceErrors,
	}
	if result.Error != "" {
		attrs = append(attrs, "error", result.Error)
	}
	switch result.Status {
	case CycleStatusSuccess:
		r.logger.Info("цикл сканирования завершён", attrs...)
	case CycleStatusCancelled:
		r.logger.Info("цикл сканирования остановлен", attrs...)
	case CycleStatusPartial, CycleStatusNoData, CycleStatusCollectionFailed:
		r.logger.Warn("цикл сканирования завершён не полностью", attrs...)
	default:
		r.logger.Error("цикл сканирования завершён с ошибкой", attrs...)
	}
}

// Run повторяет циклы без перекрытия и ожидает интервал после завершения каждого цикла.
func (r *Runner) Run(ctx context.Context, interval time.Duration) error {
	if interval < time.Second {
		return fmt.Errorf("scan_interval должен быть не меньше 1s")
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		cycleResult, err := r.RunCycle(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			r.logger.Error("цикл сканирования завершился ошибкой", "error", err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if cycleResult.ID != "" {
			r.logger.Info("ожидание до следующего цикла", "cycle_id", cycleResult.ID, "wait_duration", interval)
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

func (r *Runner) collectInParallel(ctx context.Context, cycleID string) []exchangeBatch {
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
			startedAt := time.Now()
			name := ""
			if adapter != nil {
				name = strings.TrimSpace(adapter.Name())
			}
			select {
			case semaphore <- struct{}{}:
				defer func() { <-semaphore }()
			case <-ctx.Done():
				results[i] = exchangeBatch{name: name, duration: time.Since(startedAt), err: ctx.Err()}
				return
			}
			batch := r.collectOne(ctx, adapter, cycleID)
			batch.duration = time.Since(startedAt)
			results[i] = batch
		}()
	}
	wg.Wait()
	return results
}

func (r *Runner) collectOne(ctx context.Context, adapter exchange.Exchange, cycleID string) exchangeBatch {
	if adapter == nil {
		return exchangeBatch{failureStage: "discover_markets", err: fmt.Errorf("адаптер не задан")}
	}
	name := strings.TrimSpace(adapter.Name())
	if name == "" {
		return exchangeBatch{failureStage: "discover_markets", err: fmt.Errorf("идентификатор биржи не задан")}
	}

	validMarkets, refreshed, err := r.marketsFor(ctx, adapter, name, cycleID)
	if err != nil {
		return exchangeBatch{
			name: name, markets: validMarkets, metadataRefreshed: refreshed,
			failureStage: "discover_markets", err: err,
		}
	}
	if len(validMarkets) == 0 {
		return exchangeBatch{
			name: name, metadataRefreshed: refreshed, failureStage: "discover_markets",
			err: fmt.Errorf("не найдено активных поддерживаемых Spot USDT или Perpetual рынков"),
		}
	}

	tickers, err := adapter.CollectTickers(ctx, validMarkets)
	if err != nil {
		return exchangeBatch{
			name: name, markets: validMarkets, metadataRefreshed: refreshed,
			failureStage: "collect_tickers", err: fmt.Errorf("получить пакет котировок: %w", err),
		}
	}
	if len(tickers) == 0 {
		return exchangeBatch{
			name: name, markets: validMarkets, metadataRefreshed: refreshed,
			failureStage: "collect_tickers", err: fmt.Errorf("адаптер вернул пустой пакет котировок"),
		}
	}

	marketIndex := make(map[string]market.Market, len(validMarkets))
	for _, item := range validMarkets {
		marketIndex[marketKey(item)] = item
	}
	validTickers := make([]price.Ticker, 0, len(tickers))
	rejected := 0
	seen := make(map[string]struct{}, len(tickers))
	for _, ticker := range tickers {
		key := tickerKey(ticker)
		item, exists := marketIndex[key]
		if !exists || !ticker.IsValid() {
			rejected++
			r.logger.Warn("котировка не относится к поддерживаемому рынку ответа",
				"cycle_id", cycleID, "exchange", name, "symbol", ticker.Symbol,
				"market_type", ticker.MarketType, "contract_type", ticker.ContractType)
			continue
		}
		if _, duplicate := seen[key]; duplicate {
			rejected++
			r.logger.Warn("дублирующаяся котировка пропущена", "cycle_id", cycleID, "exchange", name, "symbol", ticker.Symbol)
			continue
		}
		ticker.Symbol = item.Symbol
		ticker.Exchange = item.Exchange
		seen[key] = struct{}{}
		validTickers = append(validTickers, ticker)
	}
	if len(validTickers) == 0 {
		return exchangeBatch{
			name: name, markets: validMarkets, tickersReceived: len(tickers),
			tickersRejected: rejected, metadataRefreshed: refreshed,
			failureStage: "collect_tickers", err: fmt.Errorf("пакет не содержит корректных котировок"),
		}
	}
	sort.Slice(validMarkets, func(i, j int) bool { return marketKey(validMarkets[i]) < marketKey(validMarkets[j]) })
	sort.Slice(validTickers, func(i, j int) bool { return tickerKey(validTickers[i]) < tickerKey(validTickers[j]) })
	return exchangeBatch{
		name: name, markets: validMarkets, tickers: validTickers,
		tickersReceived: len(tickers), tickersRejected: rejected,
		metadataRefreshed: refreshed,
	}
}

// marketsFor использует кэш метаданных с ограниченным сроком действия.
// После истечения срока неудачное обновление не приводит к использованию старого списка рынков.
func (r *Runner) marketsFor(ctx context.Context, adapter exchange.Exchange, name, cycleID string) ([]market.Market, bool, error) {
	now := time.Now().UTC()
	r.marketCacheMu.Lock()
	entry, exists := r.marketCache[strings.ToLower(name)]
	r.marketCacheMu.Unlock()
	if exists {
		age := now.Sub(entry.refreshedAt)
		if age >= 0 && age < r.marketRefreshInterval {
			return append([]market.Market(nil), entry.markets...), false, nil
		}
	}

	discovered, err := adapter.DiscoverMarkets(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("получить список рынков: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}

	validMarkets := make([]market.Market, 0, len(discovered))
	seen := make(map[string]struct{}, len(discovered))
	for _, item := range discovered {
		normalized, err := market.NormalizeMarket(item)
		if err != nil {
			r.logger.Warn("пропущены некорректные метаданные рынка",
				"cycle_id", cycleID, "exchange", name, "symbol", item.Symbol, "error", err)
			continue
		}
		if !strings.EqualFold(normalized.Exchange, name) || !market.IsSupportedMarket(normalized) {
			r.logger.Warn("пропущен неподдерживаемый рынок",
				"cycle_id", cycleID, "exchange", name, "market_exchange", normalized.Exchange,
				"symbol", normalized.Symbol, "active", normalized.Active,
				"market_type", normalized.MarketType, "contract_type", normalized.ContractType)
			continue
		}
		if !market.SymbolMatchesAssets(normalized) {
			r.logger.Warn("рынок пропущен: символ не соответствует base/quote; возможен контракт с другой единицей цены",
				"cycle_id", cycleID, "exchange", name, "symbol", normalized.Symbol,
				"base_asset", normalized.BaseAsset, "quote_asset", normalized.QuoteAsset,
				"market_type", normalized.MarketType, "contract_type", normalized.ContractType)
			continue
		}
		key := marketKey(normalized)
		if _, duplicate := seen[key]; duplicate {
			r.logger.Warn("дублирующиеся метаданные рынка пропущены",
				"cycle_id", cycleID, "exchange", name, "symbol", normalized.Symbol,
				"market_type", normalized.MarketType, "contract_type", normalized.ContractType)
			continue
		}
		seen[key] = struct{}{}
		validMarkets = append(validMarkets, normalized)
	}
	if len(validMarkets) == 0 {
		return nil, false, fmt.Errorf("биржа не вернула активных поддерживаемых Spot USDT или Perpetual рынков")
	}
	sort.Slice(validMarkets, func(i, j int) bool { return marketKey(validMarkets[i]) < marketKey(validMarkets[j]) })

	r.marketCacheMu.Lock()
	r.marketCache[strings.ToLower(name)] = marketCacheEntry{
		markets: append([]market.Market(nil), validMarkets...), refreshedAt: time.Now().UTC(),
	}
	r.marketCacheMu.Unlock()
	r.logger.Info("метаданные рынков обновлены",
		"cycle_id", cycleID, "exchange", name, "markets", len(validMarkets),
		"refresh_interval", r.marketRefreshInterval)
	return validMarkets, true, nil
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
