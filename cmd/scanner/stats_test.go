package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/example/crypto-arbitrage-scanner/internal/storage"
)

func TestStatsCommandDoesNotStartScannerAndReturnsEmptyReport(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	databasePath := filepath.Join(t.TempDir(), "stats.db")
	const yamlConfig = `scanner:
  interval: 30s
  min_spread_percent: 1.0
  max_data_age: 10s
exchanges:
  binance:
    enabled: true
database:
  path: PLACEHOLDER
stats:
  timezone: UTC
  max_observation_gap: 60s
`
	configText := bytes.ReplaceAll([]byte(yamlConfig), []byte("PLACEHOLDER"), []byte(databasePath))
	if err := os.WriteFile(configPath, configText, 0600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	err := runStatsCommand([]string{
		"--period=all", "--config", configPath, "--migrations", "../../migrations", "--format=json",
	}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("команда статистики завершилась ошибкой: %v; stderr=%s", err, stderr.String())
	}
	var report struct {
		Observations int64 `json:"observations"`
		Cycles       struct {
			Total int64 `json:"total"`
		} `json:"cycles"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("не удалось разобрать JSON: %v; output=%s", err, stdout.String())
	}
	if report.Observations != 0 || report.Cycles.Total != 0 {
		t.Fatalf("пустая база должна дать пустой отчёт: %#v", report)
	}
	db, err := storage.Open(context.Background(), databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var cycles int
	if err := db.SQLDB().QueryRowContext(context.Background(), "SELECT COUNT(*) FROM scan_cycles").Scan(&cycles); err != nil {
		t.Fatal(err)
	}
	if cycles != 0 {
		t.Fatalf("stats-команда не должна запускать цикл сканирования; записано циклов %d", cycles)
	}
}

func TestParseStatsPeriodAndValidation(t *testing.T) {
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	since, until, err := parseStatsPeriod("7d", "", "", false, now)
	if err != nil {
		t.Fatal(err)
	}
	if !until.Equal(now) || !since.Equal(now.Add(-7*24*time.Hour)) {
		t.Fatalf("7d должен означать последние 168 часов: %s — %s", since, until)
	}
	since, until, err = parseStatsPeriod("", "2026-10-01T00:00:00+02:00", "2026-10-02T00:00:00+02:00", false, now)
	if err != nil {
		t.Fatal(err)
	}
	if since.Format(time.RFC3339) != "2026-09-30T22:00:00Z" || until.Format(time.RFC3339) != "2026-10-01T22:00:00Z" {
		t.Fatalf("границы периода должны переводиться в UTC: %s — %s", since, until)
	}
	if _, _, err := parseStatsPeriod("24h", "2026-10-01T00:00:00Z", "", false, now); err == nil {
		t.Fatal("неполный произвольный диапазон должен быть отклонён")
	}
	if _, _, err := parseStatsPeriod("24h", "2026-10-02T00:00:00Z", "2026-10-01T00:00:00Z", false, now); err == nil {
		t.Fatal("конец периода раньше начала должен быть отклонён")
	}
}
