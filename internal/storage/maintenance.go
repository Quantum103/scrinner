package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// IntegrityReport содержит результат проверки читаемости, целостности и версии схемы SQLite.
type IntegrityReport struct {
	Path                  string           `json:"path"`
	GeneratedAt           time.Time        `json:"generated_at"`
	IntegrityResults      []string         `json:"integrity_check"`
	ForeignKeyViolations  []string         `json:"foreign_key_violations"`
	SchemaVersion         int64            `json:"schema_version"`
	ExpectedSchemaVersion int64            `json:"expected_schema_version"`
	TableCounts           map[string]int64 `json:"table_counts"`
	MissingTables         []string         `json:"missing_tables"`
	Issues                []string         `json:"issues"`
	Warnings              []string         `json:"warnings"`
	OK                    bool             `json:"ok"`
}

// BackupReport описывает проверенную резервную копию и её содержимое.
type BackupReport struct {
	Output      string           `json:"output"`
	GeneratedAt time.Time        `json:"generated_at"`
	TableCounts map[string]int64 `json:"table_counts"`
	Integrity   IntegrityReport  `json:"integrity"`
	Warnings    []string         `json:"warnings"`
}

// OpenReadOnly открывает уже существующий файл SQLite без создания файла и без применения миграций.
func OpenReadOnly(ctx context.Context, path string) (*DB, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("путь к базе данных не задан")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("определить абсолютный путь базы данных: %w", err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return nil, fmt.Errorf("найти базу данных %q: %w", absolute, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("путь базы данных %q не является обычным файлом", absolute)
	}

	fileURL := (&url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}).String()
	dsn := fileURL + "?mode=ro&_busy_timeout=5000&_foreign_keys=on"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("открыть SQLite только для чтения: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("проверить SQLite только для чтения: %w", err)
	}
	return &DB{db: db, path: absolute}, nil
}

// CheckDatabase проверяет SQLite без миграций и без изменения исторических записей.
func CheckDatabase(ctx context.Context, path, migrationsDir string) (IntegrityReport, error) {
	report := IntegrityReport{
		Path:                 filepath.Clean(path),
		GeneratedAt:          time.Now().UTC(),
		IntegrityResults:     []string{},
		ForeignKeyViolations: []string{},
		TableCounts:          make(map[string]int64),
		MissingTables:        []string{},
		Issues:               []string{},
		Warnings:             []string{},
	}
	expectedVersion, versions, err := expectedMigrations(migrationsDir)
	if err != nil {
		report.Issues = append(report.Issues, err.Error())
		return report, errors.New(strings.Join(report.Issues, "; "))
	}
	report.ExpectedSchemaVersion = expectedVersion

	db, err := OpenReadOnly(ctx, path)
	if err != nil {
		report.Issues = append(report.Issues, err.Error())
		return report, errors.New(strings.Join(report.Issues, "; "))
	}
	defer func() { _ = db.Close() }()

	if err := db.checkIntegrity(ctx, &report); err != nil {
		report.Issues = append(report.Issues, err.Error())
	}
	if err := db.checkSchema(ctx, versions, &report); err != nil {
		report.Issues = append(report.Issues, err.Error())
	}

	report.OK = len(report.Issues) == 0 && len(report.ForeignKeyViolations) == 0
	if !report.OK {
		return report, fmt.Errorf("проверка SQLite не пройдена: %s", strings.Join(report.Issues, "; "))
	}
	return report, nil
}

// checkIntegrity запускает встроенные проверки SQLite и собирает все обнаруженные нарушения.
func (d *DB) checkIntegrity(ctx context.Context, report *IntegrityReport) error {
	rows, err := d.db.QueryContext(ctx, "PRAGMA integrity_check")
	if err != nil {
		return fmt.Errorf("выполнить PRAGMA integrity_check: %w", err)
	}
	for rows.Next() {
		var result string
		if err := rows.Scan(&result); err != nil {
			_ = rows.Close()
			return fmt.Errorf("прочитать результат integrity_check: %w", err)
		}
		report.IntegrityResults = append(report.IntegrityResults, result)
		if result != "ok" {
			report.Issues = append(report.Issues, "integrity_check: "+result)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("обработать результат integrity_check: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("закрыть результат integrity_check: %w", err)
	}
	if len(report.IntegrityResults) == 0 {
		return errors.New("PRAGMA integrity_check не вернул результатов")
	}

	rows, err = d.db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("выполнить PRAGMA foreign_key_check: %w", err)
	}
	for rows.Next() {
		var table, parent string
		var rowID sql.NullInt64
		var constraintID int64
		if err := rows.Scan(&table, &rowID, &parent, &constraintID); err != nil {
			_ = rows.Close()
			return fmt.Errorf("прочитать результат foreign_key_check: %w", err)
		}
		rowText := "table=" + table + " parent=" + parent + " constraint=" + strconv.FormatInt(constraintID, 10)
		if rowID.Valid {
			rowText += " rowid=" + strconv.FormatInt(rowID.Int64, 10)
		}
		report.ForeignKeyViolations = append(report.ForeignKeyViolations, rowText)
		report.Issues = append(report.Issues, "foreign_key_check: "+rowText)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("обработать результат foreign_key_check: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("закрыть результат foreign_key_check: %w", err)
	}
	return nil
}

// checkSchema проверяет обязательные таблицы, применённые миграции и чтение основных сущностей.
func (d *DB) checkSchema(ctx context.Context, expectedVersions []int64, report *IntegrityReport) error {
	rows, err := d.db.QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type = 'table'")
	if err != nil {
		return fmt.Errorf("прочитать список таблиц SQLite: %w", err)
	}
	tables := make(map[string]struct{})
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			return fmt.Errorf("прочитать имя таблицы SQLite: %w", err)
		}
		tables[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("обработать список таблиц SQLite: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("закрыть список таблиц SQLite: %w", err)
	}

	required := []string{"schema_migrations", "markets", "price_snapshots", "opportunities", "scan_cycles"}
	for _, table := range required {
		if _, ok := tables[table]; !ok {
			report.MissingTables = append(report.MissingTables, table)
			report.Issues = append(report.Issues, "отсутствует обязательная таблица "+table)
		}
	}
	if len(report.MissingTables) > 0 {
		return nil
	}

	var appliedVersion sql.NullInt64
	if err := d.db.QueryRowContext(ctx, "SELECT MAX(version) FROM schema_migrations").Scan(&appliedVersion); err != nil {
		return fmt.Errorf("прочитать версию схемы из schema_migrations: %w", err)
	}
	if appliedVersion.Valid {
		report.SchemaVersion = appliedVersion.Int64
	}
	if report.SchemaVersion > report.ExpectedSchemaVersion {
		report.Issues = append(report.Issues, fmt.Sprintf(
			"версия схемы %d новее поддерживаемой приложением %d; автоматическое понижение запрещено",
			report.SchemaVersion, report.ExpectedSchemaVersion))
	} else if report.SchemaVersion < report.ExpectedSchemaVersion {
		report.Issues = append(report.Issues, fmt.Sprintf(
			"схема версии %d отстаёт от ожидаемой версии %d; диагностика не применяет миграции",
			report.SchemaVersion, report.ExpectedSchemaVersion))
	}

	var applied []int64
	versionRows, err := d.db.QueryContext(ctx, "SELECT version FROM schema_migrations ORDER BY version")
	if err != nil {
		return fmt.Errorf("прочитать список версий схемы: %w", err)
	}
	for versionRows.Next() {
		var version int64
		if err := versionRows.Scan(&version); err != nil {
			_ = versionRows.Close()
			return fmt.Errorf("прочитать версию схемы: %w", err)
		}
		applied = append(applied, version)
	}
	if err := versionRows.Err(); err != nil {
		_ = versionRows.Close()
		return fmt.Errorf("обработать версии схемы: %w", err)
	}
	if err := versionRows.Close(); err != nil {
		return fmt.Errorf("закрыть версии схемы: %w", err)
	}
	appliedSet := make(map[int64]struct{}, len(applied))
	for _, version := range applied {
		appliedSet[version] = struct{}{}
	}
	for _, version := range expectedVersions {
		if _, ok := appliedSet[version]; !ok {
			report.Issues = append(report.Issues, fmt.Sprintf("миграция версии %d не отмечена как применённая", version))
		}
	}

	countedTables := []string{"schema_migrations", "markets", "price_snapshots", "opportunities", "scan_cycles"}
	for _, table := range countedTables {
		var count int64
		if err := d.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
			report.Issues = append(report.Issues, fmt.Sprintf("не удалось прочитать таблицу %s: %v", table, err))
			continue
		}
		report.TableCounts[table] = count
	}
	return nil
}

// BackupTo создаёт онлайн-снимок SQLite через VACUUM INTO, проверяет его и только затем публикует файл.
func (d *DB) BackupTo(ctx context.Context, output string, force bool, migrationsDir string) (BackupReport, error) {
	result := BackupReport{
		Output:      filepath.Clean(output),
		GeneratedAt: time.Now().UTC(),
		TableCounts: make(map[string]int64),
		Warnings:    []string{},
	}
	if d == nil || d.db == nil || strings.TrimSpace(d.path) == "" {
		return result, errors.New("открытая SQLite-база не содержит исходного пути")
	}
	if strings.TrimSpace(output) == "" {
		return result, errors.New("путь резервной копии не задан")
	}

	sourcePath, err := canonicalExistingPath(d.path)
	if err != nil {
		return result, fmt.Errorf("определить исходную базу: %w", err)
	}
	outputPath, err := prepareOutputPath(output)
	if err != nil {
		return result, err
	}
	if sourcePath == outputPath || outputPath == sourcePath+"-wal" || outputPath == sourcePath+"-shm" {
		return result, errors.New("путь резервной копии совпадает с файлом активной SQLite-базы или её WAL/SHM")
	}
	if info, err := os.Lstat(outputPath); err == nil {
		if !force {
			return result, fmt.Errorf("файл назначения %q уже существует; для явной замены укажи --force", outputPath)
		}
		if !info.Mode().IsRegular() {
			return result, fmt.Errorf("при --force можно заменить только существующий обычный файл: %q", outputPath)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return result, fmt.Errorf("проверить файл назначения %q: %w", outputPath, err)
	}

	before, err := CheckDatabase(ctx, sourcePath, migrationsDir)
	if err != nil {
		return result, fmt.Errorf("исходная база не прошла проверку; резервная копия не создавалась: %w", err)
	}

	directory := filepath.Dir(outputPath)
	temporary, err := os.CreateTemp(directory, ".stage13-backup-*.tmp")
	if err != nil {
		return result, fmt.Errorf("создать временный файл резервной копии в %q: %w", directory, err)
	}
	tempPath := temporary.Name()
	if err := temporary.Close(); err != nil {
		_ = os.Remove(tempPath)
		return result, fmt.Errorf("закрыть временный файл резервной копии: %w", err)
	}
	published := false
	defer func() {
		if !published {
			_ = os.Remove(tempPath)
		}
	}()

	sqlPath := strings.ReplaceAll(tempPath, "'", "''")
	if _, err := d.db.ExecContext(ctx, "VACUUM INTO '"+sqlPath+"'"); err != nil {
		return result, fmt.Errorf("создать согласованную SQLite-копию через VACUUM INTO: %w", err)
	}
	file, err := os.OpenFile(tempPath, os.O_RDWR, 0)
	if err != nil {
		return result, fmt.Errorf("открыть временную копию для синхронизации: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return result, fmt.Errorf("синхронизировать резервную копию на диск: %w", err)
	}
	if err := file.Close(); err != nil {
		return result, fmt.Errorf("закрыть резервную копию после синхронизации: %w", err)
	}

	checked, err := CheckDatabase(ctx, tempPath, migrationsDir)
	if err != nil {
		return result, fmt.Errorf("созданная копия не прошла проверку целостности: %w", err)
	}
	after, err := CheckDatabase(ctx, sourcePath, migrationsDir)
	if err != nil {
		return result, fmt.Errorf("после копирования исходная база не прошла проверку: %w", err)
	}
	changedDuringCopy, err := compareBackupCounts(before.TableCounts, checked.TableCounts, after.TableCounts)
	if err != nil {
		return result, err
	}
	if changedDuringCopy {
		result.Warnings = append(result.Warnings,
			"Во время резервного копирования менялось количество записей; копия прошла integrity_check и согласуется со счётчиками до/после операции.")
	}

	result.TableCounts = checked.TableCounts
	result.Integrity = checked
	result.Warnings = append(result.Warnings, checked.Warnings...)
	if force {
		if err := os.Rename(tempPath, outputPath); err != nil {
			return result, fmt.Errorf("опубликовать проверенную резервную копию в %q: %w", outputPath, err)
		}
	} else {
		if err := os.Link(tempPath, outputPath); err != nil {
			if errors.Is(err, os.ErrExist) {
				return result, fmt.Errorf("файл назначения %q уже существует; он не был перезаписан", outputPath)
			}
			return result, fmt.Errorf("безопасно опубликовать резервную копию без перезаписи: %w", err)
		}
		if err := os.Remove(tempPath); err != nil {
			return result, fmt.Errorf("резервная копия опубликована, но не удалось удалить временную ссылку %q: %w", tempPath, err)
		}
	}
	published = true
	result.Output = outputPath
	if err := syncDirectory(directory); err != nil {
		return result, fmt.Errorf("резервная копия проверена и создана, но не удалось синхронизировать каталог назначения: %w", err)
	}
	return result, nil
}

func compareBackupCounts(before, copyCounts, after map[string]int64) (bool, error) {
	changed := false
	for _, table := range []string{"schema_migrations", "markets", "price_snapshots", "opportunities", "scan_cycles"} {
		beforeCount, beforeOK := before[table]
		copyCount, copyOK := copyCounts[table]
		afterCount, afterOK := after[table]
		if !beforeOK || !copyOK || !afterOK {
			return false, fmt.Errorf("не удалось сопоставить количество записей таблицы %s", table)
		}
		if beforeCount != afterCount {
			changed = true
			minimum := beforeCount
			if afterCount < minimum {
				minimum = afterCount
			}
			maximum := beforeCount
			if afterCount > maximum {
				maximum = afterCount
			}
			if copyCount < minimum || copyCount > maximum {
				return false, fmt.Errorf("количество строк в копии для %s (%d) не согласуется с исходными счётчиками до/после (%d/%d)", table, copyCount, beforeCount, afterCount)
			}
		} else if copyCount != beforeCount {
			return false, fmt.Errorf("количество строк в копии для %s равно %d, но в исходной базе до и после копирования — %d", table, copyCount, beforeCount)
		}
	}
	return changed, nil
}

func prepareOutputPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("определить путь назначения: %w", err)
	}
	directory := filepath.Dir(absolute)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return "", fmt.Errorf("создать каталог назначения резервной копии %q: %w", directory, err)
	}
	realDirectory, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return "", fmt.Errorf("разрешить путь каталога назначения %q: %w", directory, err)
	}
	return filepath.Join(realDirectory, filepath.Base(absolute)), nil
}

func canonicalExistingPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	real, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	return filepath.Clean(real), nil
}

func expectedMigrations(directory string) (int64, []int64, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return 0, nil, fmt.Errorf("прочитать каталог миграций %q: %w", directory, err)
	}
	type migrationVersion struct {
		name    string
		version int64
	}
	var migrations []migrationVersion
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		parts := strings.SplitN(entry.Name(), "_", 2)
		if len(parts) != 2 {
			return 0, nil, fmt.Errorf("некорректное имя миграции %q", entry.Name())
		}
		version, err := strconv.ParseInt(parts[0], 10, 64)
		if err != nil || version <= 0 {
			return 0, nil, fmt.Errorf("некорректная версия миграции %q", entry.Name())
		}
		migrations = append(migrations, migrationVersion{name: entry.Name(), version: version})
	}
	if len(migrations) == 0 {
		return 0, nil, fmt.Errorf("в каталоге %q нет SQL-миграций", directory)
	}
	sort.Slice(migrations, func(i, j int) bool { return migrations[i].version < migrations[j].version })
	versions := make([]int64, 0, len(migrations))
	for i, item := range migrations {
		if i > 0 && migrations[i-1].version == item.version {
			return 0, nil, fmt.Errorf("повторяется версия миграции %d", item.version)
		}
		versions = append(versions, item.version)
	}
	return migrations[len(migrations)-1].version, versions, nil
}

// syncDirectory запрашивает фиксацию изменения записи каталога там, где это поддерживает ОС.
func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return err
	}
	return nil
}
