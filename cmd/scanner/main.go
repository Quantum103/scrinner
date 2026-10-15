package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/example/crypto-arbitrage-scanner/internal/config"
	"github.com/example/crypto-arbitrage-scanner/internal/exchange/binance"
	"github.com/example/crypto-arbitrage-scanner/internal/exchange/bybit"
	"github.com/example/crypto-arbitrage-scanner/internal/exchange/mexc"
	"github.com/example/crypto-arbitrage-scanner/internal/exchange/okx"
	"github.com/example/crypto-arbitrage-scanner/internal/market"
	"github.com/example/crypto-arbitrage-scanner/internal/price"
	"github.com/example/crypto-arbitrage-scanner/internal/storage"
)

const defaultConfigPath = "configs/config.yaml"
const defaultMigrationsPath = "migrations"

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	logger.Info("starting crypto arbitrage scanner")

	cfg, err := config.Load(defaultConfigPath)
	if err != nil {
		logger.Error("failed to load configuration", "error", err, "path", defaultConfigPath)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := storage.Open(ctx, cfg.Database.Path)
	if err != nil {
		logger.Error("failed to open SQLite", "error", err, "path", cfg.Database.Path)
		os.Exit(1)
	}
	defer db.Close()

	if err := db.Migrate(ctx, defaultMigrationsPath); err != nil {
		logger.Error("failed to apply migrations", "error", err, "path", defaultMigrationsPath)
		os.Exit(1)
	}

	logger.Info("configuration loaded", "scan_interval", cfg.Scanner.Interval, "min_spread_percent", cfg.Scanner.MinSpreadPercent, "max_data_age", cfg.Scanner.MaxDataAge, "database", cfg.Database.Path)
	logger.Info("SQLite storage initialized successfully")

	enabledAdapters := 0
	passFailed := false

	if cfg.Exchanges.Bybit.Enabled {
		enabledAdapters++
		if err := runBybitPass(ctx, logger, db); err != nil {
			logger.Error("Bybit pass failed", "error", err)
			passFailed = true
		}
	} else {
		logger.Info("Bybit adapter disabled by configuration")
	}

	if cfg.Exchanges.Binance.Enabled {
		enabledAdapters++
		if err := runBinancePass(ctx, logger, db); err != nil {
			logger.Error("Binance pass failed", "error", err)
			passFailed = true
		}
	} else {
		logger.Info("Binance adapter disabled by configuration")
	}

	if cfg.Exchanges.MEXC.Enabled {
		enabledAdapters++
	}
	if err := runMEXCPassIfEnabled(ctx, cfg.Exchanges.MEXC.Enabled, logger, db); err != nil {
		logger.Error("MEXC pass failed", "error", err)
		passFailed = true
	}

	if cfg.Exchanges.OKX.Enabled {
		enabledAdapters++
	}
	if err := runOKXPassIfEnabled(ctx, cfg.Exchanges.OKX.Enabled, cfg.Exchanges.OKX.BaseURL, logger, db); err != nil {
		logger.Error("OKX pass failed", "error", err)
		passFailed = true
	}

	if enabledAdapters == 0 {
		logger.Info("all exchange adapters disabled by configuration")
	}
	if passFailed {
		os.Exit(1)
	}
}

// runBybitPass выполняет один исследовательский проход без scheduler и detector.
func runBybitPass(ctx context.Context, logger *slog.Logger, db *storage.DB) error {
	adapter := bybit.NewDefault(logger)

	markets, err := adapter.DiscoverMarkets(ctx)
	if err != nil {
		return err
	}

	spotCount := 0
	perpetualCount := 0
	marketIDs := make(map[string]int64, len(markets))
	marketRepo := storage.NewMarketRepository(db)

	for _, m := range markets {
		switch {
		case m.IsSpot():
			spotCount++
		case m.IsPerpetual():
			perpetualCount++
		default:
			continue
		}

		id, err := marketRepo.UpsertMarket(ctx, m)
		if err != nil {
			return err
		}
		marketIDs[marketStorageKey(m)] = id
	}

	logger.Info("Bybit markets stored", "spot_usdt", spotCount, "perpetual_usdt", perpetualCount, "stored", len(marketIDs))

	tickers, stats, err := adapter.CollectTickersWithStats(ctx, markets)
	if err != nil {
		return err
	}

	snapshots := make([]storage.PriceSnapshot, 0, len(tickers))
	for _, ticker := range tickers {
		if !ticker.IsValid() {
			continue
		}

		key := tickerStorageKey(ticker)
		marketID, ok := marketIDs[key]
		if !ok {
			logger.Warn("Bybit ticker has no matching stored market", "symbol", ticker.Symbol, "market_type", ticker.MarketType, "contract_type", ticker.ContractType)
			continue
		}

		snapshot := storage.PriceSnapshot{
			MarketID:          marketID,
			Timestamp:         ticker.Timestamp,
			ReceivedAt:        ticker.ReceivedAt,
			ExchangeTimestamp: ticker.ExchangeTimestamp,
			Bid:               ticker.Bid,
			Ask:               ticker.Ask,
			Volume24h:         ticker.Volume24h,
		}
		snapshots = append(snapshots, snapshot)
	}

	priceRepo := storage.NewPriceRepository(db)
	if err := priceRepo.InsertPriceSnapshots(ctx, snapshots); err != nil {
		return err
	}

	logger.Info("Bybit price snapshots stored", "snapshots", len(snapshots))
	logger.Info("Bybit pass completed",
		"spot_usdt", spotCount,
		"perpetual_usdt", perpetualCount,
		"tickers_received", stats.Received,
		"tickers_valid", stats.Valid,
		"tickers_skipped", stats.Skipped,
		"markets_stored", len(marketIDs),
		"snapshots_stored", len(snapshots),
	)

	return nil
}

// runBinancePass выполняет один исследовательский проход Binance без scheduler и detector.
func runBinancePass(ctx context.Context, logger *slog.Logger, db *storage.DB) error {
	adapter := binance.NewDefault(logger)

	markets, err := adapter.DiscoverMarkets(ctx)
	if err != nil {
		return err
	}

	spotCount := 0
	perpetualCount := 0
	marketIDs := make(map[string]int64, len(markets))
	marketRepo := storage.NewMarketRepository(db)

	for _, m := range markets {
		switch {
		case m.IsSpot():
			spotCount++
		case m.IsPerpetual():
			perpetualCount++
		default:
			continue
		}

		id, err := marketRepo.UpsertMarket(ctx, m)
		if err != nil {
			return err
		}
		marketIDs[marketStorageKey(m)] = id
	}

	tickers, stats, err := adapter.CollectTickersWithStats(ctx, markets)
	if err != nil {
		return err
	}

	snapshots := make([]storage.PriceSnapshot, 0, len(tickers))
	for _, ticker := range tickers {
		if !ticker.IsValid() {
			continue
		}

		key := tickerStorageKey(ticker)
		marketID, ok := marketIDs[key]
		if !ok {
			logger.Warn("Binance ticker has no matching stored market", "symbol", ticker.Symbol, "market_type", ticker.MarketType, "contract_type", ticker.ContractType)
			continue
		}

		snapshot := storage.PriceSnapshot{
			MarketID:          marketID,
			Timestamp:         ticker.Timestamp,
			ReceivedAt:        ticker.ReceivedAt,
			ExchangeTimestamp: ticker.ExchangeTimestamp,
			Bid:               ticker.Bid,
			Ask:               ticker.Ask,
			Volume24h:         ticker.Volume24h,
		}
		snapshots = append(snapshots, snapshot)
	}

	priceRepo := storage.NewPriceRepository(db)
	if err := priceRepo.InsertPriceSnapshots(ctx, snapshots); err != nil {
		return err
	}

	logger.Info("Binance pass completed",
		"spot_usdt", spotCount,
		"perpetual_usdt", perpetualCount,
		"tickers_received", stats.Received,
		"tickers_valid", stats.Valid,
		"tickers_skipped", stats.Skipped,
		"markets_stored", len(marketIDs),
		"snapshots_stored", len(snapshots),
	)
	return nil
}

// marketStorageKey различает Spot и Perpetual при сохранении связки Market↔Ticker.
func marketStorageKey(m market.Market) string {
	return strings.ToUpper(strings.TrimSpace(m.Exchange)) + "|" +
		strings.ToUpper(strings.TrimSpace(m.Symbol)) + "|" +
		string(m.MarketType) + "|" +
		string(m.ContractType)
}

// tickerStorageKey строит тот же ключ, что используется для Market.
func tickerStorageKey(t price.Ticker) string {
	return strings.ToUpper(strings.TrimSpace(t.Exchange)) + "|" +
		strings.ToUpper(strings.TrimSpace(t.Symbol)) + "|" +
		string(t.MarketType) + "|" +
		string(t.ContractType)
}

// runMEXCPass выполняет один исследовательский проход MEXC без scheduler и detector.
func runMEXCPass(ctx context.Context, logger *slog.Logger, db *storage.DB) error {
	return runMEXCPassWithAdapter(ctx, logger, db, mexc.NewDefault(logger))
}

// runMEXCPassWithAdapter позволяет проверять весь поток MEXC через mock HTTP API.
func runMEXCPassWithAdapter(ctx context.Context, logger *slog.Logger, db *storage.DB, adapter *mexc.Adapter) error {
	if logger == nil {
		logger = slog.Default()
	}
	if adapter == nil {
		return fmt.Errorf("MEXC adapter не задан")
	}

	markets, err := adapter.DiscoverMarkets(ctx)
	if err != nil {
		return err
	}

	spotCount := 0
	perpetualCount := 0
	marketIDs := make(map[string]int64, len(markets))
	marketRepo := storage.NewMarketRepository(db)

	for _, m := range markets {
		switch {
		case m.IsSpot():
			spotCount++
		case m.IsPerpetual():
			perpetualCount++
		default:
			continue
		}

		id, err := marketRepo.UpsertMarket(ctx, m)
		if err != nil {
			return fmt.Errorf("сохранить MEXC рынок %s: %w", m.Symbol, err)
		}
		marketIDs[marketStorageKey(m)] = id
	}

	tickers, stats, err := adapter.CollectTickersWithStats(ctx, markets)
	if err != nil {
		return err
	}

	snapshots := make([]storage.PriceSnapshot, 0, len(tickers))
	for _, ticker := range tickers {
		if !ticker.IsValid() {
			continue
		}

		key := tickerStorageKey(ticker)
		marketID, ok := marketIDs[key]
		if !ok {
			logger.Warn("MEXC ticker has no matching stored market", "symbol", ticker.Symbol, "market_type", ticker.MarketType, "contract_type", ticker.ContractType)
			continue
		}

		snapshots = append(snapshots, storage.PriceSnapshot{
			MarketID:          marketID,
			Timestamp:         ticker.Timestamp,
			ReceivedAt:        ticker.ReceivedAt,
			ExchangeTimestamp: ticker.ExchangeTimestamp,
			Bid:               ticker.Bid,
			Ask:               ticker.Ask,
			Volume24h:         ticker.Volume24h,
		})
	}

	if err := storage.NewPriceRepository(db).InsertPriceSnapshots(ctx, snapshots); err != nil {
		return fmt.Errorf("сохранить MEXC price snapshots: %w", err)
	}

	logger.Info("MEXC pass completed",
		"spot_usdt", spotCount,
		"perpetual_usdt", perpetualCount,
		"tickers_received", stats.Received,
		"tickers_valid", stats.Valid,
		"tickers_skipped", stats.Skipped,
		"markets_stored", len(marketIDs),
		"snapshots_stored", len(snapshots),
	)
	return nil
}

// runMEXCPassIfEnabled не создаёт клиент и не обращается к сети, если MEXC выключен.
func runMEXCPassIfEnabled(ctx context.Context, enabled bool, logger *slog.Logger, db *storage.DB) error {
	if !enabled {
		if logger == nil {
			logger = slog.Default()
		}
		logger.Info("MEXC adapter disabled by configuration")
		return nil
	}
	return runMEXCPass(ctx, logger, db)
}

// runOKXPass выполняет один исследовательский проход OKX через публичный REST API.
func runOKXPass(ctx context.Context, logger *slog.Logger, db *storage.DB, baseURL string) error {
	clientConfig := okx.DefaultClientConfig()
	if strings.TrimSpace(baseURL) != "" {
		clientConfig.BaseURL = strings.TrimSpace(baseURL)
	}
	client, err := okx.NewClient(clientConfig)
	if err != nil {
		return fmt.Errorf("создать HTTP-клиент OKX: %w", err)
	}
	return runOKXPassWithAdapter(ctx, logger, db, okx.New(client, logger))
}

// runOKXPassWithAdapter позволяет тестировать весь поток OKX на mock HTTP API.
func runOKXPassWithAdapter(ctx context.Context, logger *slog.Logger, db *storage.DB, adapter *okx.Adapter) error {
	if logger == nil {
		logger = slog.Default()
	}
	if adapter == nil {
		return fmt.Errorf("OKX adapter не задан")
	}

	markets, err := adapter.DiscoverMarkets(ctx)
	if err != nil {
		return err
	}

	spotCount, swapCount := 0, 0
	marketIDs := make(map[string]int64, len(markets))
	marketRepo := storage.NewMarketRepository(db)
	for _, m := range markets {
		switch {
		case m.IsSpot():
			spotCount++
		case m.IsPerpetual():
			swapCount++
		default:
			continue
		}
		id, err := marketRepo.UpsertMarket(ctx, m)
		if err != nil {
			return fmt.Errorf("сохранить OKX рынок %s: %w", m.Symbol, err)
		}
		marketIDs[marketStorageKey(m)] = id
	}

	tickers, stats, err := adapter.CollectTickersWithStats(ctx, markets)
	if err != nil {
		return err
	}
	snapshots := make([]storage.PriceSnapshot, 0, len(tickers))
	for _, ticker := range tickers {
		if !ticker.IsValid() {
			continue
		}
		marketID, ok := marketIDs[tickerStorageKey(ticker)]
		if !ok {
			logger.Warn("OKX ticker не сопоставлен с сохранённым рынком",
				"symbol", ticker.Symbol, "market_type", ticker.MarketType, "contract_type", ticker.ContractType)
			continue
		}
		snapshots = append(snapshots, storage.PriceSnapshot{
			MarketID: marketID, Timestamp: ticker.Timestamp, ReceivedAt: ticker.ReceivedAt,
			ExchangeTimestamp: ticker.ExchangeTimestamp, Bid: ticker.Bid, Ask: ticker.Ask,
			Volume24h: ticker.Volume24h,
		})
	}
	if err := storage.NewPriceRepository(db).InsertPriceSnapshots(ctx, snapshots); err != nil {
		return fmt.Errorf("сохранить OKX price snapshots: %w", err)
	}
	logger.Info("OKX pass completed",
		"spot_usdt", spotCount, "linear_usdt_swap", swapCount,
		"tickers_received", stats.Received, "tickers_valid", stats.Valid,
		"tickers_skipped", stats.Skipped, "markets_stored", len(marketIDs),
		"snapshots_stored", len(snapshots))
	return nil
}

// runOKXPassIfEnabled не создаёт клиент и не вызывает API, когда адаптер выключен.
func runOKXPassIfEnabled(ctx context.Context, enabled bool, baseURL string, logger *slog.Logger, db *storage.DB) error {
	if !enabled {
		if logger == nil {
			logger = slog.Default()
		}
		logger.Info("OKX adapter disabled by configuration")
		return nil
	}
	return runOKXPass(ctx, logger, db, baseURL)
}
