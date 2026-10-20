package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/example/crypto-arbitrage-scanner/internal/storage"
)

func TestMaintenanceCommandsDoNotStartScannerOrModifyHistory(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	databasePath := filepath.Join(root, "scanner.db")
	db, err := storage.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, "../../migrations"); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	configPath := filepath.Join(root, "config.yaml")
	configText := fmt.Sprintf(`scanner:
  interval: 30s
  min_spread_percent: 1
  max_data_age: 10s
exchanges:
  binance:
    enabled: true
  bybit:
    enabled: true
  mexc:
    enabled: true
  okx:
    enabled: true
  gate:
    enabled: true
  bitget:
    enabled: true
database:
  path: %q
stats:
  timezone: UTC
  max_observation_gap: 60s
`, databasePath)
	if err := os.WriteFile(configPath, []byte(configText), 0600); err != nil {
		t.Fatal(err)
	}

	backupPath := filepath.Join(root, "backup.db")
	var stdout, stderr bytes.Buffer
	if err := runBackupCommand([]string{
		"--config", configPath, "--migrations", "../../migrations", "--output", backupPath,
	}, &stdout, &stderr); err != nil {
		t.Fatalf("backup завершился ошибкой: %v; stderr=%s", err, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Целостность SQLite: OK") {
		t.Fatalf("backup не сообщил о проверенной копии: %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if err := runDBCheckCommand([]string{
		"--config", configPath, "--migrations", "../../migrations",
	}, &stdout, &stderr); err != nil {
		t.Fatalf("db-check завершился ошибкой: %v; output=%s", err, stdout.String())
	}
	if !strings.Contains(stdout.String(), "Проверка SQLite: OK") {
		t.Fatalf("db-check не подтвердил целостность: %s", stdout.String())
	}

	stdout.Reset()
	if err := runBackupCheckCommand([]string{
		"--input", backupPath, "--migrations", "../../migrations",
	}, &stdout, &stderr); err != nil {
		t.Fatalf("backup-check завершился ошибкой: %v; output=%s", err, stdout.String())
	}

	verified, err := storage.OpenReadOnly(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer verified.Close()
	var cycles int64
	if err := verified.SQLDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM scan_cycles").Scan(&cycles); err != nil {
		t.Fatal(err)
	}
	if cycles != 0 {
		t.Fatalf("служебные команды не должны запускать сканирование; циклов записано: %d", cycles)
	}
	var rows int
	if err := verified.SQLDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 4 {
		t.Fatalf("команды диагностики изменили историю миграций: %d", rows)
	}
}
