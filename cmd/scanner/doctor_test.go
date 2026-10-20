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

func TestDoctorRunsOfflineWithoutMutatingDatabaseOrStartingScan(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	dbPath := filepath.Join(root, "doctor.db")
	db, err := storage.Open(ctx, dbPath)
	if err != nil { t.Fatal(err) }
	if err := db.Migrate(ctx, "../../migrations"); err != nil { _ = db.Close(); t.Fatal(err) }
	if err := db.Close(); err != nil { t.Fatal(err) }

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
`, dbPath)
	if err := os.WriteFile(configPath, []byte(configText), 0600); err != nil { t.Fatal(err) }

	var stdout, stderr bytes.Buffer
	err = runDoctorCommand([]string{"--config",configPath,"--migrations","../../migrations","--format","json"}, &stdout,&stderr)
	if err != nil {
		t.Fatalf("doctor завершился ошибкой: %v; stderr=%s; output=%s",err,stderr.String(),stdout.String())
	}
	for _, expected := range []string{"\"ok\": true","binance","bybit","mexc","okx","gate","bitget","runner_initialization","sqlite"} {
		if !strings.Contains(stdout.String(), expected) { t.Fatalf("отчёт doctor не содержит %q: %s", expected,stdout.String()) }
	}
	verified, err := storage.OpenReadOnly(ctx, dbPath)
	if err != nil { t.Fatal(err) }
	defer verified.Close()
	var cycles, migrations int64
	if err := verified.SQLDB().QueryRowContext(ctx,"SELECT COUNT(*) FROM scan_cycles").Scan(&cycles); err != nil { t.Fatal(err) }
	if err := verified.SQLDB().QueryRowContext(ctx,"SELECT COUNT(*) FROM schema_migrations").Scan(&migrations); err != nil { t.Fatal(err) }
	if cycles != 0 || migrations != 4 { t.Fatalf("doctor изменил историю: cycles=%d migrations=%d",cycles,migrations) }
}

func TestDoctorReportsMissingDatabase(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root,"config.yaml")
	configText := fmt.Sprintf(`scanner:
  interval: 30s
  min_spread_percent: 1
  max_data_age: 10s
exchanges: {}
database:
  path: %q
`, filepath.Join(root,"missing.db"))
	if err:=os.WriteFile(configPath,[]byte(configText),0600); err!=nil { t.Fatal(err) }
	var stdout,stderr bytes.Buffer
	err:=runDoctorCommand([]string{"--config",configPath,"--migrations","../../migrations"},&stdout,&stderr)
	if err==nil || !strings.Contains(stdout.String(),"sqlite") || !strings.Contains(stdout.String(),"FAILED") {
		t.Fatalf("ожидалась диагностика отсутствующей базы: err=%v output=%s",err,stdout.String())
	}
}
