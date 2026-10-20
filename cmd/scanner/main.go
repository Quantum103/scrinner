package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/example/crypto-arbitrage-scanner/internal/config"
	"github.com/example/crypto-arbitrage-scanner/internal/exchange"
	"github.com/example/crypto-arbitrage-scanner/internal/exchange/binance"
	"github.com/example/crypto-arbitrage-scanner/internal/exchange/bitget"
	"github.com/example/crypto-arbitrage-scanner/internal/exchange/bybit"
	"github.com/example/crypto-arbitrage-scanner/internal/exchange/gate"
	"github.com/example/crypto-arbitrage-scanner/internal/exchange/mexc"
	"github.com/example/crypto-arbitrage-scanner/internal/exchange/okx"
	"github.com/example/crypto-arbitrage-scanner/internal/market"
	"github.com/example/crypto-arbitrage-scanner/internal/price"
	scanengine "github.com/example/crypto-arbitrage-scanner/internal/scanner"
	"github.com/example/crypto-arbitrage-scanner/internal/storage"
	"github.com/shopspring/decimal"
)

const defaultConfigPath = "configs/config.yaml"
const defaultMigrationsPath = "migrations"

func main() {
	if len(os.Args) > 1 {
		var err error
		switch os.Args[1] {
		case "stats":
			err = runStatsCommand(os.Args[2:], os.Stdout, os.Stderr)
		case "backup":
			err = runBackupCommand(os.Args[2:], os.Stdout, os.Stderr)
		case "db-check":
			err = runDBCheckCommand(os.Args[2:], os.Stdout, os.Stderr)
		case "backup-check":
			err = runBackupCheckCommand(os.Args[2:], os.Stdout, os.Stderr)
		case "doctor":
			err = runDoctorCommand(os.Args[2:], os.Stdout, os.Stderr)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", os.Args[1], err)
			os.Exit(2)
		}
		if os.Args[1] == "stats" || os.Args[1] == "backup" || os.Args[1] == "db-check" || os.Args[1] == "backup-check" || os.Args[1] == "doctor" {
			return
		}
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	logger.Info("starting crypto arbitrage scanner")

	cfg, err := config.Load(defaultConfigPath)
	if err != nil {
		logger.Error("failed to load configuration", "error", err, "path", defaultConfigPath)
		os.Exit(1)
	}
	for _, warning := range cfg.Warnings {
		logger.Warn("неизвестное поле конфигурации; значение не применено", "field", warning)
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

	adapters := configuredAdapters(cfg.Exchanges, logger)
	if len(adapters) == 0 {
		logger.Warn("все адаптеры бирж отключены; сканирование не запускается")
		return
	}

	runner, err := scanengine.NewRunner(adapters, db, scanengine.Config{
		MinSpreadPercent:       decimal.NewFromFloat(cfg.Scanner.MinSpreadPercent),
		MaxDataAge:             cfg.Scanner.MaxDataAge,
		MaxConcurrentExchanges: 6,
	}, logger)
	if err != nil {
		logger.Error("не удалось подготовить scanner", "error", err)
		os.Exit(1)
	}

	logger.Info("scanner started", "exchanges_enabled", len(adapters),
		"scan_interval", cfg.Scanner.Interval, "min_spread_percent", cfg.Scanner.MinSpreadPercent,
		"max_data_age", cfg.Scanner.MaxDataAge)
	if err := runner.Run(ctx, cfg.Scanner.Interval); err != nil && !errors.Is(err, context.Canceled) {
		logger.Error("scanner stopped with an error", "error", err)
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

// runGatePass выполняет один исследовательский проход Gate через публичный REST API.
func runGatePass(ctx context.Context, logger *slog.Logger, db *storage.DB, baseURL string) error {
	clientConfig := gate.DefaultClientConfig()
	if strings.TrimSpace(baseURL) != "" {
		clientConfig.BaseURL = strings.TrimSpace(baseURL)
	}
	client, err := gate.NewClient(clientConfig)
	if err != nil {
		return fmt.Errorf("создать HTTP-клиент Gate: %w", err)
	}
	return runGatePassWithAdapter(ctx, logger, db, gate.New(client, logger))
}

// runGatePassWithAdapter позволяет тестировать полный поток Gate на mock HTTP API.
func runGatePassWithAdapter(ctx context.Context, logger *slog.Logger, db *storage.DB, adapter *gate.Adapter) error {
	if logger == nil {
		logger = slog.Default()
	}
	if adapter == nil {
		return fmt.Errorf("Gate adapter не задан")
	}

	markets, err := adapter.DiscoverMarkets(ctx)
	if err != nil {
		return err
	}
	spotCount, perpetualCount := 0, 0
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
			return fmt.Errorf("сохранить Gate рынок %s: %w", m.Symbol, err)
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
			logger.Warn("Gate ticker не сопоставлен с сохранённым рынком",
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
		return fmt.Errorf("сохранить Gate price snapshots: %w", err)
	}
	logger.Info("Gate pass completed",
		"spot_usdt", spotCount, "linear_usdt_perpetual", perpetualCount,
		"tickers_received", stats.Received, "tickers_valid", stats.Valid,
		"tickers_skipped", stats.Skipped, "markets_stored", len(marketIDs),
		"snapshots_stored", len(snapshots))
	return nil
}

// runGatePassIfEnabled не создаёт клиент и не выполняет запросов, если Gate выключен.
func runGatePassIfEnabled(ctx context.Context, enabled bool, baseURL string, logger *slog.Logger, db *storage.DB) error {
	if !enabled {
		if logger == nil {
			logger = slog.Default()
		}
		logger.Info("Gate adapter disabled by configuration")
		return nil
	}
	return runGatePass(ctx, logger, db, baseURL)
}

// runBitgetPass выполняет один исследовательский проход Bitget через публичный REST API.
func runBitgetPass(ctx context.Context, logger *slog.Logger, db *storage.DB) error {
	return runBitgetPassWithAdapter(ctx, logger, db, bitget.NewDefault(logger))
}

// runBitgetPassWithAdapter сохраняет рынки и котировки Bitget через существующие SQLite-репозитории.
func runBitgetPassWithAdapter(ctx context.Context, logger *slog.Logger, db *storage.DB, adapter *bitget.Adapter) error {
	if logger == nil {
		logger = slog.Default()
	}
	if adapter == nil {
		return fmt.Errorf("Bitget adapter не задан")
	}

	markets, err := adapter.DiscoverMarkets(ctx)
	if err != nil {
		return err
	}
	spotCount, perpetualCount := 0, 0
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
			return fmt.Errorf("сохранить Bitget рынок %s: %w", m.Symbol, err)
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
			logger.Warn("Bitget ticker не сопоставлен с сохранённым рынком",
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
		return fmt.Errorf("сохранить Bitget price snapshots: %w", err)
	}
	logger.Info("Bitget pass completed",
		"spot_usdt", spotCount, "usdt_perpetual", perpetualCount,
		"tickers_received", stats.Received, "tickers_valid", stats.Valid,
		"tickers_skipped", stats.Skipped, "markets_stored", len(marketIDs),
		"snapshots_stored", len(snapshots))
	return nil
}

// runBitgetPassIfEnabled не создаёт клиент и не обращается к сети, когда Bitget выключен.
func runBitgetPassIfEnabled(ctx context.Context, enabled bool, logger *slog.Logger, db *storage.DB) error {
	if !enabled {
		if logger == nil {
			logger = slog.Default()
		}
		logger.Info("Bitget adapter disabled by configuration")
		return nil
	}
	return runBitgetPass(ctx, logger, db)
}

type unavailableExchange struct {
	name string
	err  error
}

func (e unavailableExchange) Name() string { return e.name }

func (e unavailableExchange) DiscoverMarkets(context.Context) ([]market.Market, error) {
	return nil, e.err
}

func (e unavailableExchange) CollectTickers(context.Context, []market.Market) ([]price.Ticker, error) {
	return nil, e.err
}

// configuredAdapters создаёт адаптеры по YAML и сохраняет ошибки создания как отказ отдельной биржи.
func configuredAdapters(cfg config.ExchangesConfig, logger *slog.Logger) []exchange.Exchange {
	adapters := make([]exchange.Exchange, 0, 6)
	if cfg.Bybit.Enabled {
		adapters = append(adapters, bybit.NewDefault(logger))
	}
	if cfg.Binance.Enabled {
		adapters = append(adapters, binance.NewDefault(logger))
	}
	if cfg.MEXC.Enabled {
		adapters = append(adapters, mexc.NewDefault(logger))
	}
	if cfg.OKX.Enabled {
		clientConfig := okx.DefaultClientConfig()
		if strings.TrimSpace(cfg.OKX.BaseURL) != "" {
			clientConfig.BaseURL = strings.TrimSpace(cfg.OKX.BaseURL)
		}
		client, err := okx.NewClient(clientConfig)
		if err != nil {
			adapters = append(adapters, unavailableExchange{name: string(exchange.ExchangeOKX), err: fmt.Errorf("создать HTTP-клиент OKX: %w", err)})
		} else {
			adapters = append(adapters, okx.New(client, logger))
		}
	}
	if cfg.Gate.Enabled {
		clientConfig := gate.DefaultClientConfig()
		if strings.TrimSpace(cfg.Gate.BaseURL) != "" {
			clientConfig.BaseURL = strings.TrimSpace(cfg.Gate.BaseURL)
		}
		client, err := gate.NewClient(clientConfig)
		if err != nil {
			adapters = append(adapters, unavailableExchange{name: string(exchange.ExchangeGate), err: fmt.Errorf("создать HTTP-клиент Gate: %w", err)})
		} else {
			adapters = append(adapters, gate.New(client, logger))
		}
	}
	if cfg.Bitget.Enabled {
		adapters = append(adapters, bitget.NewDefault(logger))
	}
	return adapters
}
