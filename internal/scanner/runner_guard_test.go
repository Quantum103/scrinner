package scanner

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/example/crypto-arbitrage-scanner/internal/exchange"
	"github.com/example/crypto-arbitrage-scanner/internal/market"
	"github.com/example/crypto-arbitrage-scanner/internal/price"
	"github.com/example/crypto-arbitrage-scanner/internal/storage"
	"github.com/shopspring/decimal"
)

func TestRunnerDoesNotReusePreviouslySavedPricesAfterAdapterFailure(t *testing.T) {
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "stale-price.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Migrate(ctx, "../../migrations"); err != nil {
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
	binanceAdapter := &fakeExchange{
		name: "binance", markets: []market.Market{buyMarket},
		tickers: []price.Ticker{fakeTicker(buyMarket, "99", "100", now)},
	}
	bybitAdapter := &fakeExchange{
		name: "bybit", markets: []market.Market{sellMarket},
		tickers: []price.Ticker{fakeTicker(sellMarket, "102", "103", now)},
	}
	runner, err := NewRunner([]exchange.Exchange{binanceAdapter, bybitAdapter}, db, Config{
		MinSpreadPercent: decimal.NewFromInt(1), MaxDataAge: 10 * time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}

	first, err := runner.RunCycle(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first.OpportunitiesDetected != 1 {
		t.Fatalf("первый цикл должен найти одну возможность: %#v", first)
	}

	// На втором проходе API Bybit завершился ошибкой; старый снимок остаётся в истории,
	// но не должен участвовать в новой детекции без свежего ответа этого адаптера.
	bybitAdapter.tickerErr = errors.New("fixture: биржа недоступна")
	second, err := runner.RunCycle(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if second.OpportunitiesDetected != 0 {
		t.Fatalf("старый сохранённый снимок не должен давать новую возможность: %#v", second)
	}
	if second.Exchanges[1].Error == "" {
		t.Fatalf("сбой Bybit должен быть отражён в результате цикла: %#v", second.Exchanges)
	}
	var count int
	if err := db.SQLDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM opportunities").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("в истории должна оставаться только возможность первого цикла, получено %d", count)
	}
}

func TestRunnerRejectsOverlappingCycleAndStopsOnContextCancellation(t *testing.T) {
	t.Run("overlap", func(t *testing.T) {
		db := openTestDB(t)
		started := make(chan struct{}, 1)
		release := make(chan struct{})
		m := market.Market{
			Exchange: "binance", Symbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT",
			MarketType: market.MarketTypeSpot, ContractType: market.ContractTypeSpot, Active: true,
		}
		adapter := &controlledExchange{name: "binance", markets: []market.Market{m}, started: started, release: release}
		runner, err := NewRunner([]exchange.Exchange{adapter}, db, Config{
			MinSpreadPercent: decimal.NewFromInt(1), MaxDataAge: 10 * time.Second,
		}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err != nil {
			t.Fatal(err)
		}

		finished := make(chan error, 1)
		go func() {
			_, err := runner.RunCycle(context.Background())
			finished <- err
		}()
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("первый цикл не начал сбор")
		}
		if _, err := runner.RunCycle(context.Background()); !errors.Is(err, ErrCycleAlreadyRunning) {
			t.Fatalf("второй цикл должен быть отклонён, получено %v", err)
		}
		close(release)
		select {
		case err := <-finished:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("первый цикл завис после снятия блокировки")
		}
	})

	t.Run("cancel", func(t *testing.T) {
		db := openTestDB(t)
		started := make(chan struct{}, 1)
		m := market.Market{
			Exchange: "binance", Symbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT",
			MarketType: market.MarketTypeSpot, ContractType: market.ContractTypeSpot, Active: true,
		}
		adapter := &controlledExchange{name: "binance", markets: []market.Market{m}, started: started}
		runner, err := NewRunner([]exchange.Exchange{adapter}, db, Config{
			MinSpreadPercent: decimal.NewFromInt(1), MaxDataAge: 10 * time.Second,
		}, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		finished := make(chan error, 1)
		go func() {
			_, err := runner.RunCycle(ctx)
			finished <- err
		}()
		select {
		case <-started:
		case <-time.After(time.Second):
			cancel()
			t.Fatal("сбор не начался")
		}
		cancel()
		select {
		case err := <-finished:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("цикл должен завершиться с отменой контекста, получено %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("цикл не завершился после отмены context")
		}
	})
}

type controlledExchange struct {
	name    string
	markets []market.Market
	started chan struct{}
	release <-chan struct{}
}

func (f *controlledExchange) Name() string { return f.name }

func (f *controlledExchange) DiscoverMarkets(ctx context.Context) ([]market.Market, error) {
	if f.started != nil {
		select {
		case f.started <- struct{}{}:
		default:
		}
	}
	if f.release != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-f.release:
		}
	} else {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return append([]market.Market(nil), f.markets...), nil
}

func (f *controlledExchange) CollectTickers(ctx context.Context, _ []market.Market) ([]price.Ticker, error) {
	return nil, ctx.Err()
}
