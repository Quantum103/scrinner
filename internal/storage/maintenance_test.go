package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/example/crypto-arbitrage-scanner/internal/arbitrage"
	"github.com/shopspring/decimal"
)

func populateMaintenanceDB(t *testing.T, db *DB) {
	t.Helper()
	ctx := context.Background()
	markets := NewMarketRepository(db)
	buyMarket := testMarket()
	buyID, err := markets.CreateMarket(ctx, buyMarket)
	if err != nil {
		t.Fatal(err)
	}
	sellMarket := testMarket()
	sellMarket.Exchange = "bybit"
	sellMarket.MarketType = "futures"
	sellMarket.ContractType = "perpetual"
	sellID, err := markets.CreateMarket(ctx, sellMarket)
	if err != nil {
		t.Fatal(err)
	}

	priceRepo := NewPriceRepository(db)
	if _, err := priceRepo.InsertPriceSnapshot(ctx, PriceSnapshot{
		MarketID: buyID, Timestamp: time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC),
		Bid: decimal.RequireFromString("100.1"), Ask: decimal.RequireFromString("100.2"),
	}); err != nil {
		t.Fatal(err)
	}

	started := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	cycle := ScanCycle{
		ID: "cycle-for-backup", StartedAt: started, FinishedAt: started.Add(time.Second),
		Status: "success", EnabledExchanges: 2, SucceededExchanges: 2,
		MinSpreadPercent: decimal.RequireFromString("1"),
	}
	if err := db.StartScanCycle(ctx, cycle); err != nil {
		t.Fatal(err)
	}
	if err := db.FinishScanCycle(ctx, cycle); err != nil {
		t.Fatal(err)
	}

	opportunity := arbitrage.Opportunity{
		CycleID: cycle.ID, Timestamp: started, Symbol: "PEPE/USDT",
		BaseAsset: "PEPE", QuoteAsset: "USDT",
		BuyMarketID: buyID, BuySymbol: buyMarket.Symbol, BuyExchange: buyMarket.Exchange,
		BuyMarketType: buyMarket.MarketType, BuyContractType: buyMarket.ContractType,
		BuyPrice: decimal.RequireFromString("100"),
		SellMarketID: sellID, SellSymbol: sellMarket.Symbol, SellExchange: sellMarket.Exchange,
		SellMarketType: sellMarket.MarketType, SellContractType: sellMarket.ContractType,
		SellPrice: decimal.RequireFromString("101"), SpreadPercent: decimal.RequireFromString("1"),
	}
	if _, err := NewOpportunityRepository(db).InsertOpportunity(ctx, opportunity); err != nil {
		t.Fatal(err)
	}
}

func TestBackupToPreservesHistoricalDataSchemaAndIndexes(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	populateMaintenanceDB(t, db)

	output := filepath.Join(t.TempDir(), "backups", "historical.db")
	report, err := db.BackupTo(ctx, output, false, "../../migrations")
	if err != nil {
		t.Fatalf("резервное копирование: %v", err)
	}
	if !report.Integrity.OK {
		t.Fatalf("копия не прошла проверку целостности: %#v", report.Integrity)
	}
	if report.TableCounts["markets"] != 2 ||
		report.TableCounts["price_snapshots"] != 1 ||
		report.TableCounts["opportunities"] != 1 ||
		report.TableCounts["scan_cycles"] != 1 ||
		report.TableCounts["schema_migrations"] != 4 {
		t.Fatalf("неверные количества записей в резервной копии: %#v", report.TableCounts)
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatalf("файл резервной копии не опубликован: %v", err)
	}

	copyDB, err := OpenReadOnly(ctx, output)
	if err != nil {
		t.Fatal(err)
	}
	defer copyDB.Close()
	var spread, cycleID string
	if err := copyDB.SQLDB().QueryRowContext(ctx, "SELECT spread_percent FROM opportunities LIMIT 1").Scan(&spread); err != nil {
		t.Fatal(err)
	}
	if spread != "1" {
		t.Fatalf("исторический спред изменился: %s", spread)
	}
	if err := copyDB.SQLDB().QueryRowContext(ctx, "SELECT cycle_id FROM opportunities LIMIT 1").Scan(&cycleID); err != nil {
		t.Fatal(err)
	}
	if cycleID != "cycle-for-backup" {
		t.Fatalf("cycle_id не сохранён: %s", cycleID)
	}
	var indexCount int
	if err := copyDB.SQLDB().QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'index' AND name = 'idx_opportunities_history_identity_timestamp'`).Scan(&indexCount); err != nil {
		t.Fatal(err)
	}
	if indexCount != 1 {
		t.Fatal("индекс исторической статистики отсутствует в копии")
	}

	var sourceCount, copyCount int
	if err := db.SQLDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM opportunities").Scan(&sourceCount); err != nil {
		t.Fatal(err)
	}
	if err := copyDB.SQLDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM opportunities").Scan(&copyCount); err != nil {
		t.Fatal(err)
	}
	if sourceCount != copyCount || sourceCount != 1 {
		t.Fatalf("история не совпадает: исходная база=%d, копия=%d", sourceCount, copyCount)
	}
}

func TestBackupToRefusesOverwriteAndSameSourcePath(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	populateMaintenanceDB(t, db)

	if _, err := db.BackupTo(ctx, db.path, true, "../../migrations"); err == nil {
		t.Fatal("нельзя заменять активную базу её резервной копией")
	}
	output := filepath.Join(t.TempDir(), "existing.db")
	sentinel := []byte("existing backup must remain unchanged")
	if err := os.WriteFile(output, sentinel, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := db.BackupTo(ctx, output, false, "../../migrations"); err == nil {
		t.Fatal("ожидалась ошибка при существующем файле назначения")
	}
	got, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(sentinel) {
		t.Fatal("существующий файл назначения был изменён")
	}
}

func TestBackupToRejectsUnwritableDestinationDirectory(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	parent := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(parent, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(parent, "copy.db")
	if _, err := db.BackupTo(ctx, output, false, "../../migrations"); err == nil {
		t.Fatal("ожидалась ошибка каталога назначения")
	}
}

func TestCheckDatabaseDetectsForeignKeyViolation(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	db.SQLDB().SetMaxOpenConns(1)
	if _, err := db.SQLDB().ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatal(err)
	}
	_, err := db.SQLDB().ExecContext(ctx, `INSERT INTO price_snapshots(market_id, timestamp, bid, ask)
		VALUES (987654, '2026-10-19T00:00:00Z', '1', '2')`)
	if err != nil {
		t.Fatalf("не удалось создать контролируемую ошибку внешнего ключа: %v", err)
	}
	report, err := CheckDatabase(ctx, db.path, "../../migrations")
	if err == nil || report.OK || len(report.ForeignKeyViolations) == 0 {
		t.Fatalf("нарушение внешнего ключа не обнаружено: report=%#v err=%v", report, err)
	}
}

func TestCheckDatabaseRejectsCorruptAndFutureSchema(t *testing.T) {
	ctx := context.Background()
	corruptPath := filepath.Join(t.TempDir(), "corrupt.db")
	if err := os.WriteFile(corruptPath, []byte("not a sqlite database"), 0600); err != nil {
		t.Fatal(err)
	}
	if report, err := CheckDatabase(ctx, corruptPath, "../../migrations"); err == nil || report.OK {
		t.Fatalf("повреждённая база не была отклонена: report=%#v err=%v", report, err)
	}

	db := newTestDB(t)
	if _, err := db.SQLDB().ExecContext(ctx,
		"INSERT INTO schema_migrations(version, applied_at) VALUES (99, ?)",
		time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	report, err := CheckDatabase(ctx, db.path, "../../migrations")
	if err == nil || report.OK || report.SchemaVersion != 99 {
		t.Fatalf("несовместимая будущая схема не обнаружена: report=%#v err=%v", report, err)
	}
}

func TestCheckDatabaseDoesNotApplyMigrations(t *testing.T) {
	ctx := context.Background()
	db := newTestDB(t)
	var before int
	if err := db.SQLDB().QueryRowContext(ctx, "SELECT MAX(version) FROM schema_migrations").Scan(&before); err != nil {
		t.Fatal(err)
	}
	report, err := CheckDatabase(ctx, db.path, "../../migrations")
	if err != nil {
		t.Fatal(err)
	}
	var after int
	if err := db.SQLDB().QueryRowContext(ctx, "SELECT MAX(version) FROM schema_migrations").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after || report.SchemaVersion != int64(before) {
		t.Fatalf("проверка изменила схему: до=%d после=%d отчёт=%d", before, after, report.SchemaVersion)
	}
}

func TestBackupToWhileWriterIsActive(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	db := newTestDB(t)

	stop := make(chan struct{})
	writerDone := make(chan error, 1)
	go func() {
		for i := int64(0); ; i++ {
			select {
			case <-stop:
				writerDone <- nil
				return
			default:
			}
			id := "writer-" + strconv.FormatInt(i, 10)
			_, err := db.SQLDB().ExecContext(ctx, `INSERT INTO scan_cycles(id, started_at, status)
				VALUES (?, ?, 'success')`, id, time.Now().UTC().Format(time.RFC3339Nano))
			if err != nil {
				writerDone <- err
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	time.Sleep(10 * time.Millisecond)
	output := filepath.Join(t.TempDir(), "online.db")
	_, backupErr := db.BackupTo(ctx, output, false, "../../migrations")
	close(stop)
	writerErr := <-writerDone
	if writerErr != nil && !errors.Is(writerErr, context.Canceled) {
		t.Fatalf("параллельный писатель: %v", writerErr)
	}
	if backupErr != nil {
		t.Fatalf("резервное копирование при активной записи: %v", backupErr)
	}
	report, err := CheckDatabase(ctx, output, "../../migrations")
	if err != nil || !report.OK {
		t.Fatalf("копия при активной записи не прошла проверку: report=%#v err=%v", report, err)
	}
	if report.TableCounts["scan_cycles"] == 0 {
		t.Fatal("в резервной копии отсутствуют подтверждённые записи циклов")
	}
}

func TestCompareBackupCountsDetectsImpossibleSnapshot(t *testing.T) {
before := map[string]int64{
		"schema_migrations": 4, "markets": 2, "price_snapshots": 1,
		"opportunities": 3, "scan_cycles": 4,
	}
	copyCounts := map[string]int64{
		"schema_migrations": 4, "markets": 9, "price_snapshots": 1,
		"opportunities": 3, "scan_cycles": 4,
	}
	after := map[string]int64{
		"schema_migrations": 4, "markets": 3, "price_snapshots": 1,
		"opportunities": 3, "scan_cycles": 4,
	}
	_, err := compareBackupCounts(before, copyCounts, after)
	if err == nil {
		t.Fatal("ожидалась ошибка для несовместимых счётчиков")
	}
}
