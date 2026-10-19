package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/example/crypto-arbitrage-scanner/internal/config"
	"github.com/example/crypto-arbitrage-scanner/internal/statistics"
	"github.com/example/crypto-arbitrage-scanner/internal/storage"
)

// runStatsCommand формирует исторический отчёт и завершает работу, не запуская сканер.
func runStatsCommand(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("stats", flag.ContinueOnError)
	flags.SetOutput(stderr)
	period := flags.String("period", "24h", "период: 24h, 7d, 30d или all")
	sinceText := flags.String("since", "", "начало периода RFC3339 (включительно)")
	untilText := flags.String("until", "", "конец периода RFC3339 (исключительно)")
	format := flags.String("format", "text", "формат отчёта: text, json или csv")
	timezone := flags.String("timezone", "", "временная зона отчёта, например UTC или Europe/Amsterdam")
	limit := flags.Int("limit", 20, "максимум строк в текстовых рейтингах")
	sortBy := flags.String("sort", "observations", "сортировка: observations, episodes, avg-spread, max-spread, duration")
	configPath := flags.String("config", defaultConfigPath, "путь YAML-конфигурации")
	databasePath := flags.String("database", "", "переопределить database.path")
	migrationsPath := flags.String("migrations", defaultMigrationsPath, "каталог SQL-миграций")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("неожиданные аргументы: %s", strings.Join(flags.Args(), " "))
	}
	if *limit <= 0 {
		return fmt.Errorf("--limit должен быть положительным")
	}
	if err := statistics.ValidateSort(*sortBy); err != nil {
		return err
	}
	switch strings.ToLower(*format) {
	case "text", "json", "csv":
	default:
		return fmt.Errorf("неизвестный формат %q; доступны text, json и csv", *format)
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if *databasePath != "" {
		cfg.Database.Path = *databasePath
	}
	if *timezone != "" {
		cfg.Stats.Timezone = *timezone
		if _, err := time.LoadLocation(cfg.Stats.Timezone); err != nil {
			return fmt.Errorf("неизвестная временная зона %q: %w", cfg.Stats.Timezone, err)
		}
	}

	periodExplicit := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "period" {
			periodExplicit = true
		}
	})
	now := time.Now().UTC()
	since, until, err := parseStatsPeriod(*period, *sinceText, *untilText, periodExplicit, now)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	db, err := storage.Open(ctx, cfg.Database.Path)
	if err != nil {
		return fmt.Errorf("открыть SQLite для статистики: %w", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Migrate(ctx, *migrationsPath); err != nil {
		return fmt.Errorf("применить SQLite-миграции для статистики: %w", err)
	}

	report, err := statistics.Build(ctx, db, statistics.Options{
		Since: since, Until: until, Timezone: cfg.Stats.Timezone,
		MaxObservationGap: cfg.Stats.MaxObservationGap,
	})
	if err != nil {
		return err
	}
	statistics.SortCoins(report.Coins, *sortBy)
	statistics.SortRoutes(report.Routes, *sortBy)
	switch strings.ToLower(*format) {
	case "json":
		return statistics.RenderJSON(stdout, report)
	case "csv":
		return statistics.RenderCSV(stdout, report)
	default:
		return statistics.RenderText(stdout, report, *limit, *sortBy)
	}
}

func parseStatsPeriod(period, sinceText, untilText string, periodExplicit bool, now time.Time) (time.Time, time.Time, error) {
	if sinceText != "" || untilText != "" {
		if sinceText == "" || untilText == "" {
			return time.Time{}, time.Time{}, fmt.Errorf("--since и --until необходимо указывать вместе")
		}
		if periodExplicit {
			return time.Time{}, time.Time{}, fmt.Errorf("используй либо --period, либо пару --since/--until")
		}
		since, err := time.Parse(time.RFC3339Nano, sinceText)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("разобрать --since: %w", err)
		}
		until, err := time.Parse(time.RFC3339Nano, untilText)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("разобрать --until: %w", err)
		}
		if !since.Before(until) {
			return time.Time{}, time.Time{}, fmt.Errorf("--since должен быть раньше --until")
		}
		return since.UTC(), until.UTC(), nil
	}

	now = now.UTC()
	var duration time.Duration
	switch strings.ToLower(strings.TrimSpace(period)) {
	case "all":
		return time.Time{}, time.Time{}, nil
	case "24h":
		duration = 24 * time.Hour
	case "7d":
		duration = 7 * 24 * time.Hour
	case "30d":
		duration = 30 * 24 * time.Hour
	default:
		return time.Time{}, time.Time{}, fmt.Errorf("неизвестный период %q; доступны 24h, 7d, 30d, all или --since/--until", period)
	}
	return now.Add(-duration), now, nil
}
