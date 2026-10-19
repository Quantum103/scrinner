package scanner

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/example/crypto-arbitrage-scanner/internal/exchange"
	"github.com/example/crypto-arbitrage-scanner/internal/market"
	"github.com/example/crypto-arbitrage-scanner/internal/price"
	"github.com/example/crypto-arbitrage-scanner/internal/storage"
	"github.com/shopspring/decimal"
)

type fakeExchange struct {
	name        string
	markets     []market.Market
	tickers     []price.Ticker
	discoverErr error
	tickerErr   error
	barrier     *sync.WaitGroup
	active      *atomic.Int32
	maxActive   *atomic.Int32
	started     chan struct{}
}

func (f *fakeExchange) Name() string { return f.name }

func (f *fakeExchange) DiscoverMarkets(ctx context.Context) ([]market.Market, error) {
	if f.started != nil {
		select {
		case f.started <- struct{}{}:
		default:
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if f.discoverErr != nil {
		return nil, f.discoverErr
	}
	return append([]market.Market(nil), f.markets...), nil
}

func (f *fakeExchange) CollectTickers(ctx context.Context, markets []market.Market) ([]price.Ticker, error) {
	if f.active != nil {
		active := f.active.Add(1)
		defer f.active.Add(-1)
		if f.maxActive != nil {
			for {
				maximum := f.maxActive.Load()
				if active <= maximum || f.maxActive.CompareAndSwap(maximum, active) {
					break
				}
			}
		}
	}
	if f.barrier != nil {
		f.barrier.Done()
		waited := make(chan struct{})
		go func() {
			f.barrier.Wait()
			close(waited)
		}()
		select {
		case <-waited:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if f.tickerErr != nil {
		return nil, f.tickerErr
	}
	return append([]price.Ticker(nil), f.tickers...), nil
}

var _ exchange.Exchange = (*fakeExchange)(nil)

func TestRunnerPersistsQuotesAndDetectsOpportunityDespiteOneExchangeFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	db := openTestDB(t)
	now := time.Now().UTC()
	barrier := &sync.WaitGroup{}
	barrier.Add(2)

	binanceMarket := market.Market{
		Exchange: "binance", Symbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT",
		MarketType: market.MarketTypeSpot, ContractType: market.ContractTypeSpot, Active: true,
	}
	bybitMarket := market.Market{
		Exchange: "bybit", Symbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT",
		MarketType: market.MarketTypeFutures, ContractType: market.ContractTypePerpetual, Active: true,
	}
	binanceTicker := fakeTicker(binanceMarket, "99", "100", now)
	bybitTicker := fakeTicker(bybitMarket, "102", "103", now)

	adapters := []exchange.Exchange{
		&fakeExchange{name: "binance", markets: []market.Market{binanceMarket}, tickers: []price.Ticker{binanceTicker}, barrier: barrier},
		&fakeExchange{name: "bybit", markets: []market.Market{bybitMarket}, tickers: []price.Ticker{bybitTicker}, barrier: barrier},
		&fakeExchange{name: "mexc", discoverErr: errors.New("fixture: API unavailable")},
	}
	runner, err := NewRunner(adapters, db, Config{
		MinSpreadPercent: decimal.NewFromInt(1), MaxDataAge: 10 * time.Second,
		MaxConcurrentExchanges: 3,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}

	result, err := runner.RunCycle(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Exchanges) != 3 {
		t.Fatalf("ожидался результат по трём адаптерам: %#v", result.Exchanges)
	}
	if result.Exchanges[0].Error != "" || result.Exchanges[1].Error != "" || result.Exchanges[2].Error == "" {
		t.Fatalf("частичный отказ должен быть отмечен, а остальные биржи — успешно обработаны: %#v", result.Exchanges)
	}
	if result.Exchanges[0].SnapshotsSaved != 1 || result.Exchanges[1].SnapshotsSaved != 1 {
		t.Fatalf("снимки успешных бирж не сохранены: %#v", result.Exchanges)
	}
	if result.OpportunitiesDetected != 1 {
		t.Fatalf("ожидалась одна возможность, найдено %d", result.OpportunitiesDetected)
	}

	opportunities, err := storage.NewOpportunityRepository(db).GetRecentOpportunities(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(opportunities) != 1 {
		t.Fatalf("ожидалась одна запись opportunities, получено %d", len(opportunities))
	}
	got := opportunities[0]
	if got.BuyExchange != "binance" || got.BuySymbol != "BTCUSDT" ||
		got.SellExchange != "bybit" || got.SellSymbol != "BTCUSDT" {
		t.Fatalf("неверное направление или исходные символы в возможности: %#v", got)
	}
	if got.BuySnapshotID <= 0 || got.SellSnapshotID <= 0 {
		t.Fatalf("возможность должна ссылаться на снимки, использованные для расчёта: %#v", got)
	}
	if !got.SpreadPercent.Equal(decimal.NewFromInt(2)) {
		t.Fatalf("валовой спред = %s, ожидалось 2", got.SpreadPercent)
	}

	var snapshotCount int
	if err := db.SQLDB().QueryRowContext(ctx, `SELECT COUNT(*) FROM price_snapshots`).Scan(&snapshotCount); err != nil {
		t.Fatal(err)
	}
	if snapshotCount != 2 {
		t.Fatalf("должны сохраниться два снимка, получено %d", snapshotCount)
	}
}

func TestRunnerBoundsConcurrentExchangeRequests(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	db := openTestDB(t)
	var active, maximum atomic.Int32
	now := time.Now().UTC()
	adapters := make([]exchange.Exchange, 3)
	for i, name := range []string{"binance", "bybit", "gate"} {
		m := market.Market{
			Exchange: name, Symbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT",
			MarketType: market.MarketTypeSpot, ContractType: market.ContractTypeSpot, Active: true,
		}
		adapters[i] = &delayedExchange{
			fakeExchange: fakeExchange{
				name: name, markets: []market.Market{m}, tickers: []price.Ticker{fakeTicker(m, "99", "100", now)},
				active: &active, maxActive: &maximum,
			},
		}
	}
	runner, err := NewRunner(adapters, db, Config{
		MinSpreadPercent: decimal.NewFromInt(1), MaxDataAge: 10 * time.Second,
		MaxConcurrentExchanges: 2,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.RunCycle(ctx); err != nil {
		t.Fatal(err)
	}
	if maximum.Load() < 2 || maximum.Load() > 2 {
		t.Fatalf("ожидалось ограничение в две параллельные загрузки, максимум=%d", maximum.Load())
	}
}

type delayedExchange struct{ fakeExchange }

func (f *delayedExchange) CollectTickers(ctx context.Context, markets []market.Market) ([]price.Ticker, error) {
	active := f.active.Add(1)
	defer f.active.Add(-1)
	for {
		maximum := f.maxActive.Load()
		if active <= maximum || f.maxActive.CompareAndSwap(maximum, active) {
			break
		}
	}
	select {
	case <-time.After(25 * time.Millisecond):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if f.tickerErr != nil {
		return nil, f.tickerErr
	}
	return append([]price.Ticker(nil), f.tickers...), nil
}

func openTestDB(t *testing.T) *storage.DB {
	t.Helper()
	db, err := storage.Open(context.Background(), filepath.Join(t.TempDir(), "scanner-test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background(), "../../migrations"); err != nil {
		t.Fatal(err)
	}
	return db
}

func fakeTicker(m market.Market, bid, ask string, now time.Time) price.Ticker {
	bidValue, _ := decimal.NewFromString(bid)
	askValue, _ := decimal.NewFromString(ask)
	receivedAt := now.Add(-time.Second)
	return price.Ticker{
		Exchange: m.Exchange, Symbol: m.Symbol,
		MarketType: m.MarketType, ContractType: m.ContractType,
		Bid: bidValue, Ask: askValue, Timestamp: receivedAt, ReceivedAt: receivedAt,
	}
}


// TestRunnerClassifiesEmptyResponsesAndCompleteExchangeFailure проверяет, что пустые ответы не маскируются под успешный сбор.
func TestRunnerClassifiesEmptyResponsesAndCompleteExchangeFailure(t *testing.T) {
	t.Run("empty tickers", func(t *testing.T) {
		ctx := context.Background()
		db := openTestDB(t)
		m := market.Market{
			Exchange: "binance", Symbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT",
			MarketType: market.MarketTypeSpot, ContractType: market.ContractTypeSpot, Active: true,
		}
		runner, err := NewRunner([]exchange.Exchange{
			&fakeExchange{name: "binance", markets: []market.Market{m}},
		}, db, Config{
			MinSpreadPercent: decimal.NewFromInt(1), MaxDataAge: 10 * time.Second,
		}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err != nil {
			t.Fatal(err)
		}

		result, err := runner.RunCycle(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != CycleStatusCollectionFailed {
			t.Fatalf("пустой пакет должен быть помечен как отсутствие собранных данных: %#v", result)
		}
		if result.ExchangesFailed != 1 || result.Exchanges[0].FailureStage != "collect_tickers" {
			t.Fatalf("ошибка пустого ответа не отражена в результате биржи: %#v", result.Exchanges)
		}
		if result.SnapshotsSaved != 0 || result.OpportunitiesDetected != 0 {
			t.Fatalf("пустой ответ не должен порождать снимки или возможности: %#v", result)
		}
	})

	t.Run("all exchanges unavailable", func(t *testing.T) {
		ctx := context.Background()
		db := openTestDB(t)
		runner, err := NewRunner([]exchange.Exchange{
			&fakeExchange{name: "binance", discoverErr: errors.New("fixture: Binance недоступна")},
			&fakeExchange{name: "bybit", discoverErr: errors.New("fixture: Bybit недоступна")},
		}, db, Config{
			MinSpreadPercent: decimal.NewFromInt(1), MaxDataAge: 10 * time.Second,
		}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err != nil {
			t.Fatal(err)
		}

		result, err := runner.RunCycle(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if result.Status != CycleStatusCollectionFailed || result.ExchangesFailed != 2 {
			t.Fatalf("полный отказ бирж должен быть явно отражён: %#v", result)
		}
		if result.OpportunitiesDetected != 0 || result.SnapshotsSaved != 0 {
			t.Fatalf("при отсутствии данных нельзя создавать результаты: %#v", result)
		}
	})
}

// TestRunnerCachesMarketMetadataBetweenQuoteCycles гарантирует, что метаданные не запрашиваются на каждом цикле.
func TestRunnerCachesMarketMetadataBetweenQuoteCycles(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	now := time.Now().UTC()
	m := market.Market{
		Exchange: "binance", Symbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT",
		MarketType: market.MarketTypeSpot, ContractType: market.ContractTypeSpot, Active: true,
	}
	adapter := &fakeExchange{
		name: "binance", markets: []market.Market{m},
		tickers: []price.Ticker{fakeTicker(m, "99", "100", now)},
	}
	runner, err := NewRunner([]exchange.Exchange{adapter}, db, Config{
		MinSpreadPercent: decimal.NewFromInt(1), MaxDataAge: 10 * time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	first, err := runner.RunCycle(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first.MetadataRefreshes != 1 || first.Status != CycleStatusSuccess {
		t.Fatalf("первый цикл должен обновить метаданные и успешно сохранить данные: %#v", first)
	}

	adapter.discoverErr = errors.New("fixture: API метаданных временно недоступен")
	second, err := runner.RunCycle(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if second.MetadataRefreshes != 0 || second.Status != CycleStatusSuccess {
		t.Fatalf("в пределах срока кэша второй цикл должен переиспользовать рынки и получать новые котировки: %#v", second)
	}
	if second.SnapshotsSaved != 1 {
		t.Fatalf("второй цикл должен сохранить новое историческое наблюдение: %#v", second)
	}
}

// TestRunnerDoesNotUseExpiredMetadataWhenRefreshFails запрещает использование устаревшего кэша после ошибки обновления.
func TestRunnerDoesNotUseExpiredMetadataWhenRefreshFails(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	m := market.Market{
		Exchange: "binance", Symbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT",
		MarketType: market.MarketTypeSpot, ContractType: market.ContractTypeSpot, Active: true,
	}
	adapter := &fakeExchange{
		name: "binance", markets: []market.Market{m},
		tickers: []price.Ticker{fakeTicker(m, "99", "100", time.Now().UTC())},
	}
	runner, err := NewRunner([]exchange.Exchange{adapter}, db, Config{
		MinSpreadPercent: decimal.NewFromInt(1), MaxDataAge: 10 * time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.RunCycle(ctx); err != nil {
		t.Fatal(err)
	}

	runner.marketRefreshInterval = time.Nanosecond
	time.Sleep(time.Millisecond)
	adapter.discoverErr = errors.New("fixture: обновление рынков не удалось")
	second, err := runner.RunCycle(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if second.Status != CycleStatusCollectionFailed || second.SnapshotsSaved != 0 {
		t.Fatalf("после истечения срока кэша нельзя использовать рынки, которые не удалось обновить: %#v", second)
	}
	if second.Exchanges[0].FailureStage != "discover_markets" {
		t.Fatalf("ошибка обновления метаданных не отражена: %#v", second.Exchanges[0])
	}
}

// TestRunnerRunStartsImmediatelyAndStopsOnCancellation проверяет немедленный запуск и остановку планировщика.
func TestRunnerRunStartsImmediatelyAndStopsOnCancellation(t *testing.T) {
	db := openTestDB(t)
	now := time.Now().UTC()
	m := market.Market{
		Exchange: "binance", Symbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT",
		MarketType: market.MarketTypeSpot, ContractType: market.ContractTypeSpot, Active: true,
	}
	started := make(chan struct{}, 4)
	adapter := &fakeExchange{
		name: "binance", markets: []market.Market{m},
		tickers: []price.Ticker{fakeTicker(m, "99", "100", now)},
		started: started,
	}
	runner, err := NewRunner([]exchange.Exchange{adapter}, db, Config{
		MinSpreadPercent: decimal.NewFromInt(1), MaxDataAge: 10 * time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runner.Run(ctx, time.Hour)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		cancel()
		t.Fatal("первый цикл должен начаться сразу, не ожидая интервал")
	}

	deadline := time.Now().Add(time.Second)
	for {
		var count int
		if err := db.SQLDB().QueryRowContext(context.Background(), "SELECT COUNT(*) FROM price_snapshots").Scan(&count); err != nil {
			cancel()
			t.Fatal(err)
		}
		if count == 1 {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("первый цикл не сохранил снимок до таймаута")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("планировщик должен завершиться по отмене контекста, получено %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("планировщик не завершился после отмены контекста")
	}
	if len(started) != 0 {
		t.Fatalf("после завершения не должен стартовать новый цикл")
	}
}


// TestRunnerReportsOpportunityPersistenceFailureAfterSavingQuotes проверяет ошибку сохранения результата цикла.
func TestRunnerReportsOpportunityPersistenceFailureAfterSavingQuotes(t *testing.T) {
	ctx := context.Background()
	db := openTestDB(t)
	_, err := db.SQLDB().ExecContext(ctx, `CREATE TRIGGER reject_opportunity_insert
		BEFORE INSERT ON opportunities
		BEGIN
			SELECT RAISE(ABORT, 'fixture: запись возможности запрещена');
		END`)
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC()
	buyMarket := market.Market{
		Exchange: "binance", Symbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT",
		MarketType: market.MarketTypeSpot, ContractType: market.ContractTypeSpot, Active: true,
	}
	sellMarket := market.Market{
		Exchange: "bybit", Symbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT",
		MarketType: market.MarketTypeFutures, ContractType: market.ContractTypePerpetual, Active: true,
	}
	runner, err := NewRunner([]exchange.Exchange{
		&fakeExchange{name: "binance", markets: []market.Market{buyMarket},
			tickers: []price.Ticker{fakeTicker(buyMarket, "99", "100", now)}},
		&fakeExchange{name: "bybit", markets: []market.Market{sellMarket},
			tickers: []price.Ticker{fakeTicker(sellMarket, "102", "103", now)}},
	}, db, Config{
		MinSpreadPercent: decimal.NewFromInt(1), MaxDataAge: 10 * time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}

	result, err := runner.RunCycle(ctx)
	if err == nil {
		t.Fatal("ожидалась ошибка при сохранении возможности")
	}
	if result.Status != CycleStatusSaveFailed || result.OpportunitiesDetected != 1 ||
		result.OpportunitiesSaved != 0 || result.PersistenceErrors != 1 {
		t.Fatalf("ошибка сохранения возможности должна быть видна в результате цикла: %#v", result)
	}
	if result.SnapshotsSaved != 2 {
		t.Fatalf("успешно сохранённые снимки должны учитываться отдельно: %#v", result)
	}
	var count int
	if err := db.SQLDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM opportunities").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("транзакция возможностей должна откатиться целиком, записей=%d", count)
	}
}
