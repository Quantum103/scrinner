package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
)

// PriceSnapshot представляет сохранённый снимок bid/ask конкретного рынка.
type PriceSnapshot struct {
	ID                int64
	MarketID          int64
	Timestamp         time.Time
	ReceivedAt        time.Time
	ExchangeTimestamp *time.Time
	Bid               decimal.Decimal
	Ask               decimal.Decimal
	Volume24h         *decimal.Decimal
}

// PriceRepository содержит операции хранения исторических цен.
type PriceRepository struct {
	db *sql.DB
}

// NewPriceRepository создаёт репозиторий снимков цен.
func NewPriceRepository(db *DB) *PriceRepository {
	return &PriceRepository{db: db.db}
}

// InsertPriceSnapshot сохраняет один снимок цены.
func (r *PriceRepository) InsertPriceSnapshot(
	ctx context.Context,
	s PriceSnapshot,
) (int64, error) {
	if err := validateSnapshot(s); err != nil {
		return 0, err
	}

	var volume any
	if s.Volume24h != nil {
		volume = s.Volume24h.String()
	}
	var receivedAt any
	if !s.ReceivedAt.IsZero() {
		receivedAt = utcText(s.ReceivedAt)
	}
	var exchangeTimestamp any
	if s.ExchangeTimestamp != nil {
		exchangeTimestamp = utcText(*s.ExchangeTimestamp)
	}

	result, err := r.db.ExecContext(
		ctx,
		`INSERT INTO price_snapshots(
			market_id,
			timestamp,
			bid,
			ask,
			volume_24h,
			received_at,
			exchange_timestamp
		)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		s.MarketID,
		utcText(s.Timestamp),
		s.Bid.String(),
		s.Ask.String(),
		volume,
		receivedAt,
		exchangeTimestamp,
	)
	if err != nil {
		return 0, fmt.Errorf(
			"сохранить снимок цены: %w",
			err,
		)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf(
			"получить id снимка цены: %w",
			err,
		)
	}

	return id, nil
}

// InsertPriceSnapshots сохраняет набор снимков одной транзакцией.
// InsertPriceSnapshots сохраняет набор снимков одной транзакцией.
func (r *PriceRepository) InsertPriceSnapshots(
	ctx context.Context,
	snapshots []PriceSnapshot,
) error {
	_, err := r.InsertPriceSnapshotsWithIDs(ctx, snapshots)
	return err
}

// InsertPriceSnapshotsWithIDs атомарно сохраняет снимки и возвращает их ID в исходном порядке.
func (r *PriceRepository) InsertPriceSnapshotsWithIDs(
	ctx context.Context,
	snapshots []PriceSnapshot,
) ([]int64, error) {
	ids := make([]int64, 0, len(snapshots))
	if len(snapshots) == 0 {
		return ids, nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("начать транзакцию пакетной записи цен: %w", err)
	}
	stmt, err := tx.PrepareContext(
		ctx,
		`INSERT INTO price_snapshots(
			market_id,
			timestamp,
			bid,
			ask,
			volume_24h,
			received_at,
			exchange_timestamp
		)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("подготовить пакетную запись цен: %w", err)
	}

	for _, snapshot := range snapshots {
		if err := validateSnapshot(snapshot); err != nil {
			_ = stmt.Close()
			_ = tx.Rollback()
			return nil, err
		}

		var volume any
		if snapshot.Volume24h != nil {
			volume = snapshot.Volume24h.String()
		}
		var receivedAt any
		if !snapshot.ReceivedAt.IsZero() {
			receivedAt = utcText(snapshot.ReceivedAt)
		}
		var exchangeTimestamp any
		if snapshot.ExchangeTimestamp != nil {
			exchangeTimestamp = utcText(*snapshot.ExchangeTimestamp)
		}

		result, err := stmt.ExecContext(
			ctx,
			snapshot.MarketID,
			utcText(snapshot.Timestamp),
			snapshot.Bid.String(),
			snapshot.Ask.String(),
			volume,
			receivedAt,
			exchangeTimestamp,
		)
		if err != nil {
			_ = stmt.Close()
			_ = tx.Rollback()
			return nil, fmt.Errorf("сохранить пакет снимков цен: %w", err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			_ = stmt.Close()
			_ = tx.Rollback()
			return nil, fmt.Errorf("получить id снимка цены: %w", err)
		}
		ids = append(ids, id)
	}

	if err := stmt.Close(); err != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("закрыть пакетную запись цен: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("зафиксировать пакет снимков цен: %w", err)
	}
	return ids, nil
}

// GetLatestPrice возвращает последний снимок указанного рынка.
func (r *PriceRepository) GetLatestPrice(
	ctx context.Context,
	marketID int64,
) (PriceSnapshot, error) {
	return scanSnapshot(
		r.db.QueryRowContext(
			ctx,
			`SELECT
				id,
				market_id,
				timestamp,
				bid,
				ask,
				volume_24h,
				received_at,
				exchange_timestamp
			 FROM price_snapshots
			 WHERE market_id = ?
			 ORDER BY timestamp DESC, id DESC
			 LIMIT 1`,
			marketID,
		),
	)
}

// GetPriceHistory возвращает историю снимков рынка за заданный период.
func (r *PriceRepository) GetPriceHistory(
	ctx context.Context,
	marketID int64,
	from time.Time,
	to time.Time,
) ([]PriceSnapshot, error) {
	rows, err := r.db.QueryContext(
		ctx,
		`SELECT
			id,
			market_id,
			timestamp,
			bid,
			ask,
			volume_24h,
			received_at,
			exchange_timestamp
		 FROM price_snapshots
		 WHERE market_id = ?
		   AND timestamp >= ?
		   AND timestamp <= ?
		 ORDER BY timestamp ASC, id ASC`,
		marketID,
		utcText(from),
		utcText(to),
	)
	if err != nil {
		return nil, fmt.Errorf(
			"получить историю цен: %w",
			err,
		)
	}
	defer rows.Close()

	result := make([]PriceSnapshot, 0)
	for rows.Next() {
		snapshot, err := scanSnapshot(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, snapshot)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(
			"обработать историю цен: %w",
			err,
		)
	}
	return result, nil
}

func validateSnapshot(s PriceSnapshot) error {
	if s.MarketID <= 0 {
		return fmt.Errorf("market_id должен быть положительным")
	}
	if s.Timestamp.IsZero() {
		return fmt.Errorf("timestamp снимка цены не задан")
	}
	if !s.Bid.GreaterThan(decimal.Zero) || !s.Ask.GreaterThan(decimal.Zero) {
		return fmt.Errorf("bid и ask должны быть положительными")
	}
	if s.Bid.GreaterThan(s.Ask) {
		return fmt.Errorf("bid не может быть больше ask")
	}
	if s.Volume24h != nil && s.Volume24h.IsNegative() {
		return fmt.Errorf("volume_24h не может быть отрицательным")
	}
	return nil
}

func scanSnapshot(s rowScanner) (PriceSnapshot, error) {
	var result PriceSnapshot
	var timestamp string
	var bid string
	var ask string
	var volume sql.NullString
	var receivedAt sql.NullString
	var exchangeTimestamp sql.NullString

	if err := s.Scan(
		&result.ID,
		&result.MarketID,
		&timestamp,
		&bid,
		&ask,
		&volume,
		&receivedAt,
		&exchangeTimestamp,
	); err != nil {
		return PriceSnapshot{}, fmt.Errorf(
			"прочитать снимок цены: %w",
			err,
		)
	}

	var err error
	result.Timestamp, err = parseTime(timestamp)
	if err != nil {
		return PriceSnapshot{}, err
	}
	result.Bid, err = decimal.NewFromString(bid)
	if err != nil {
		return PriceSnapshot{}, fmt.Errorf("разобрать bid %q: %w", bid, err)
	}
	result.Ask, err = decimal.NewFromString(ask)
	if err != nil {
		return PriceSnapshot{}, fmt.Errorf("разобрать ask %q: %w", ask, err)
	}
	if volume.Valid {
		value, err := decimal.NewFromString(volume.String)
		if err != nil {
			return PriceSnapshot{}, fmt.Errorf(
				"разобрать volume_24h %q: %w",
				volume.String,
				err,
			)
		}
		result.Volume24h = &value
	}
	if receivedAt.Valid {
		result.ReceivedAt, err = parseTime(receivedAt.String)
		if err != nil {
			return PriceSnapshot{}, fmt.Errorf("разобрать received_at: %w", err)
		}
	}
	if exchangeTimestamp.Valid {
		value, err := parseTime(exchangeTimestamp.String)
		if err != nil {
			return PriceSnapshot{}, fmt.Errorf("разобрать exchange_timestamp: %w", err)
		}
		result.ExchangeTimestamp = &value
	}
	return result, nil
}
