package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/example/crypto-arbitrage-scanner/internal/config"
	"github.com/example/crypto-arbitrage-scanner/internal/exchange"
	"github.com/example/crypto-arbitrage-scanner/internal/scanner"
	"github.com/example/crypto-arbitrage-scanner/internal/storage"
	"github.com/shopspring/decimal"
)

// doctorCheck описывает одну локальную проверку без секретов и сетевых данных.
type doctorCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// doctorReport содержит результаты локальной диагностики приложения.
type doctorReport struct {
	GeneratedAt        time.Time        `json:"generated_at"`
	OK                 bool             `json:"ok"`
	Checks             []doctorCheck    `json:"checks"`
	Warnings           []string         `json:"warnings,omitempty"`
	SupportedExchanges []string         `json:"supported_exchanges"`
	EnabledAdapters    []string         `json:"enabled_adapters"`
	Database           map[string]int64 `json:"database_table_counts,omitempty"`
}

func runDoctorCommand(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", defaultConfigPath, "путь YAML-конфигурации")
	databasePath := flags.String("database", "", "переопределить database.path")
	migrationsPath := flags.String("migrations", defaultMigrationsPath, "каталог SQL-миграций")
	format := flags.String("format", "text", "формат результата: text или json")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("неожиданные аргументы: %s", strings.Join(flags.Args(), " "))
	}
	if *format != "text" && *format != "json" {
		return fmt.Errorf("неизвестный формат %q; доступны text и json", *format)
	}

	report := doctorReport{
		GeneratedAt: time.Now().UTC(),
		Checks:      []doctorCheck{},
		Warnings:    []string{},
		SupportedExchanges: []string{
			string(exchange.ExchangeBinance), string(exchange.ExchangeBybit), string(exchange.ExchangeMEXC),
			string(exchange.ExchangeOKX), string(exchange.ExchangeGate), string(exchange.ExchangeBitget),
		},
		EnabledAdapters: []string{},
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		report.Checks = append(report.Checks, doctorCheck{Name: "configuration", Status: "failed", Message: err.Error()})
		if renderErr := renderDoctorReport(stdout, report, *format); renderErr != nil {
			return errors.Join(err, renderErr)
		}
		return err
	}
	report.Checks = append(report.Checks, doctorCheck{Name: "configuration", Status: "ok", Message: "YAML прочитан; обязательные параметры и значения проверены"})
	for _, warning := range cfg.Warnings {
		report.Warnings = append(report.Warnings, "неизвестное поле конфигурации: "+warning)
	}
	if *databasePath != "" {
		cfg.Database.Path = *databasePath
	}
	unknownStatus := "ok"
	unknownMessage := "неизвестных YAML-полей не обнаружено"
	if len(cfg.Warnings) > 0 {
		unknownStatus = "warning"
		unknownMessage = "неизвестные YAML-поля игнорируются для совместимости: " + strings.Join(cfg.Warnings, ", ")
	}
	report.Checks = append(report.Checks, doctorCheck{Name: "unknown_config_fields", Status: unknownStatus, Message: unknownMessage})

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	adapters := configuredAdapters(cfg.Exchanges, logger)
	registered := make(map[string]struct{}, len(adapters))
	for _, adapter := range adapters {
		if adapter == nil {
			continue
		}
		name := strings.ToLower(strings.TrimSpace(adapter.Name()))
		registered[name] = struct{}{}
		report.EnabledAdapters = append(report.EnabledAdapters, name)
	}
	missing := make([]string, 0)
	for _, name := range enabledExchangeNames(cfg.Exchanges) {
		if _, ok := registered[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		message := fmt.Sprintf("все включённые адаптеры зарегистрированы; включено: %d", len(adapters))
		if len(adapters) == 0 {
			message += "; все биржи выключены, сбор не запустится"
			report.Warnings = append(report.Warnings, "все адаптеры бирж выключены")
		}
		report.Checks = append(report.Checks, doctorCheck{Name: "exchange_registry", Status: statusWhen(len(adapters) > 0), Message: message})
	} else {
		report.Checks = append(report.Checks, doctorCheck{Name: "exchange_registry", Status: "failed", Message: "нет регистрации для включённых бирж: " + strings.Join(missing, ", ")})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	dbReport, dbErr := storage.CheckDatabase(ctx, cfg.Database.Path, *migrationsPath)
	if dbErr != nil {
		report.Checks = append(report.Checks, doctorCheck{Name: "sqlite", Status: "failed", Message: dbErr.Error()})
	} else {
		report.Database = dbReport.TableCounts
		report.Checks = append(report.Checks, doctorCheck{Name: "sqlite", Status: "ok", Message: fmt.Sprintf("целостность, схема версии %d/%d, миграции и чтение таблиц проверены", dbReport.SchemaVersion, dbReport.ExpectedSchemaVersion)})
		db, openErr := storage.OpenReadOnly(ctx, cfg.Database.Path)
		if openErr != nil {
			report.Checks = append(report.Checks, doctorCheck{Name: "runner_initialization", Status: "failed", Message: "не удалось открыть SQLite только для чтения: " + openErr.Error()})
		} else {
			_, runnerErr := scanner.NewRunner(adapters, db, scanner.Config{
				MinSpreadPercent:       decimal.NewFromFloat(cfg.Scanner.MinSpreadPercent),
				MaxDataAge:             cfg.Scanner.MaxDataAge,
				MaxConcurrentExchanges: 6,
			}, logger)
			closeErr := db.Close()
			if runnerErr != nil {
				report.Checks = append(report.Checks, doctorCheck{Name: "runner_initialization", Status: "failed", Message: runnerErr.Error()})
			} else if closeErr != nil {
				report.Checks = append(report.Checks, doctorCheck{Name: "runner_initialization", Status: "failed", Message: "runner создан, но закрытие диагностического SQLite завершилось ошибкой: " + closeErr.Error()})
			} else {
				report.Checks = append(report.Checks, doctorCheck{Name: "runner_initialization", Status: "ok", Message: "адаптеры и зависимости runner инициализированы; цикл сканирования не запускался"})
			}
		}
	}
	report.OK = true
	for _, check := range report.Checks {
		if check.Status == "failed" {
			report.OK = false
			break
		}
	}
	if err := renderDoctorReport(stdout, report, *format); err != nil {
		return err
	}
	if !report.OK {
		return errors.New("локальная диагностика не пройдена")
	}
	return nil
}

func enabledExchangeNames(cfg config.ExchangesConfig) []string {
	names := make([]string, 0, 6)
	if cfg.Binance.Enabled {
		names = append(names, string(exchange.ExchangeBinance))
	}
	if cfg.Bybit.Enabled {
		names = append(names, string(exchange.ExchangeBybit))
	}
	if cfg.MEXC.Enabled {
		names = append(names, string(exchange.ExchangeMEXC))
	}
	if cfg.OKX.Enabled {
		names = append(names, string(exchange.ExchangeOKX))
	}
	if cfg.Gate.Enabled {
		names = append(names, string(exchange.ExchangeGate))
	}
	if cfg.Bitget.Enabled {
		names = append(names, string(exchange.ExchangeBitget))
	}
	return names
}

func statusWhen(ok bool) string {
	if ok {
		return "ok"
	}
	return "warning"
}

func renderDoctorReport(w io.Writer, report doctorReport, format string) error {
	if format == "json" {
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		return encoder.Encode(report)
	}
	state := "FAILED"
	if report.OK {
		state = "OK"
	}
	fmt.Fprintf(w, "Диагностика сканера: %s\nВремя: %s\n", state, report.GeneratedAt.Format(time.RFC3339))
	for _, check := range report.Checks {
		fmt.Fprintf(w, "[%s] %s: %s\n", strings.ToUpper(check.Status), check.Name, check.Message)
	}
	for _, warning := range report.Warnings {
		fmt.Fprintf(w, "Предупреждение: %s\n", warning)
	}
	fmt.Fprintf(w, "Поддерживаемые адаптеры: %s\n", strings.Join(report.SupportedExchanges, ", "))
	if len(report.EnabledAdapters) > 0 {
		fmt.Fprintf(w, "Включённые адаптеры: %s\n", strings.Join(report.EnabledAdapters, ", "))
	}
	return nil
}
