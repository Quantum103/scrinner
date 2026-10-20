package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/example/crypto-arbitrage-scanner/internal/config"
	"github.com/example/crypto-arbitrage-scanner/internal/storage"
)

// runBackupCommand создаёт и проверяет резервную копию, не запуская сборщик котировок.
func runBackupCommand(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("backup", flag.ContinueOnError)
	flags.SetOutput(stderr)
	output := flags.String("output", "", "путь итогового файла резервной копии")
	force := flags.Bool("force", false, "разрешить атомарную замену существующего обычного файла после проверки копии")
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
	if strings.TrimSpace(*output) == "" {
		return errors.New("--output обязателен")
	}
	if *format != "text" && *format != "json" {
		return fmt.Errorf("неизвестный формат %q; доступны text и json", *format)
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if *databasePath != "" {
		cfg.Database.Path = *databasePath
	}
	info, err := os.Stat(cfg.Database.Path)
	if err != nil {
		return fmt.Errorf("исходная база должна существовать до запуска backup: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("исходная база %q не является обычным файлом", cfg.Database.Path)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	db, err := storage.Open(ctx, cfg.Database.Path)
	if err != nil {
		return fmt.Errorf("открыть SQLite для резервного копирования: %w", err)
	}
	defer func() { _ = db.Close() }()

	report, err := db.BackupTo(ctx, *output, *force, *migrationsPath)
	if err != nil {
		return err
	}
	return renderBackupReport(stdout, report, *format)
}

// runDBCheckCommand выполняет диагностику существующей базы в режиме только для чтения.
func runDBCheckCommand(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("db-check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", defaultConfigPath, "путь YAML-конфигурации")
	databasePath := flags.String("database", "", "проверить указанный файл вместо database.path")
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
	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if *databasePath != "" {
		cfg.Database.Path = *databasePath
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	report, checkErr := storage.CheckDatabase(ctx, cfg.Database.Path, *migrationsPath)
	if err := renderIntegrityReport(stdout, report, *format); err != nil {
		return err
	}
	return checkErr
}

// runBackupCheckCommand проверяет резервную копию перед ручным восстановлением.
func runBackupCheckCommand(args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("backup-check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	input := flags.String("input", "", "путь к файлу резервной копии")
	migrationsPath := flags.String("migrations", defaultMigrationsPath, "каталог SQL-миграций")
	format := flags.String("format", "text", "формат результата: text или json")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("неожиданные аргументы: %s", strings.Join(flags.Args(), " "))
	}
	if strings.TrimSpace(*input) == "" {
		return errors.New("--input обязателен")
	}
	if *format != "text" && *format != "json" {
		return fmt.Errorf("неизвестный формат %q; доступны text и json", *format)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	report, checkErr := storage.CheckDatabase(ctx, *input, *migrationsPath)
	if err := renderIntegrityReport(stdout, report, *format); err != nil {
		return err
	}
	return checkErr
}

func renderBackupReport(w io.Writer, report storage.BackupReport, format string) error {
	if format == "json" {
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		return encoder.Encode(report)
	}
	fmt.Fprintf(w, "РЕЗЕРВНАЯ КОПИЯ ПРОВЕРЕНА\nФайл: %s\nСформирована: %s\n", report.Output, report.GeneratedAt.Format("2006-01-02T15:04:05Z07:00"))
	fmt.Fprintln(w, "Целостность SQLite: OK")
	writeTableCounts(w, report.TableCounts)
	for _, warning := range report.Warnings {
		fmt.Fprintf(w, "Предупреждение: %s\n", warning)
	}
	return nil
}

func renderIntegrityReport(w io.Writer, report storage.IntegrityReport, format string) error {
	if format == "json" {
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		return encoder.Encode(report)
	}
	state := "FAILED"
	if report.OK {
		state = "OK"
	}
	fmt.Fprintf(w, "Проверка SQLite: %s\nФайл: %s\nСформирована: %s\nВерсия схемы: %d (ожидается %d)\n",
		state, report.Path, report.GeneratedAt.Format("2006-01-02T15:04:05Z07:00"),
		report.SchemaVersion, report.ExpectedSchemaVersion)
	if len(report.IntegrityResults) > 0 {
		fmt.Fprintf(w, "PRAGMA integrity_check: %s\n", strings.Join(report.IntegrityResults, "; "))
	} else {
		fmt.Fprintln(w, "PRAGMA integrity_check: результат недоступен")
	}
	if len(report.ForeignKeyViolations) == 0 {
		fmt.Fprintln(w, "PRAGMA foreign_key_check: нарушений не найдено")
	} else {
		for _, violation := range report.ForeignKeyViolations {
			fmt.Fprintf(w, "Нарушение внешнего ключа: %s\n", violation)
		}
	}
	if len(report.TableCounts) > 0 {
		writeTableCounts(w, report.TableCounts)
	}
	for _, missing := range report.MissingTables {
		fmt.Fprintf(w, "Отсутствует таблица: %s\n", missing)
	}
	for _, issue := range report.Issues {
		fmt.Fprintf(w, "Ошибка: %s\n", issue)
	}
	for _, warning := range report.Warnings {
		fmt.Fprintf(w, "Предупреждение: %s\n", warning)
	}
	return nil
}

func writeTableCounts(w io.Writer, counts map[string]int64) {
	for _, table := range []string{"schema_migrations", "markets", "price_snapshots", "opportunities", "scan_cycles"} {
		if count, ok := counts[table]; ok {
			fmt.Fprintf(w, "Записей в %s: %d\n", table, count)
		}
	}
}
