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
	name       string
	markets    []market.Market
	tickers    []price.Ticker
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
	return f.fakeExchange.CollectTickers(ctx, markets)
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
