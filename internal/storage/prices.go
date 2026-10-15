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
	ID        int64
	MarketID  int64
	Timestamp time.Time
	Bid       decimal.Decimal
	Ask       decimal.Decimal
	Volume24h *decimal.Decimal
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

	result, err := r.db.ExecContext(
		ctx,
		`INSERT INTO price_snapshots(
			market_id,
			timestamp,
			bid,
			ask,
			volume_24h
		)
		VALUES (?, ?, ?, ?, ?)`,
		s.MarketID,
		utcText(s.Timestamp),
		s.Bid.String(),
		s.Ask.String(),
		volume,
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
func (r *PriceRepository) InsertPriceSnapshots(
	ctx context.Context,
	snapshots []PriceSnapshot,
) error {
	if len(snapshots) == 0 {
		return nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf(
			"начать транзакцию пакетной записи цен: %w",
			err,
		)
	}

	stmt, err := tx.PrepareContext(
		ctx,
		`INSERT INTO price_snapshots(
			market_id,
			timestamp,
			bid,
			ask,
			volume_24h
		)
		VALUES (?, ?, ?, ?, ?)`,
	)
	if err != nil {
		_ = tx.Rollback()

		return fmt.Errorf(
			"подготовить пакетную запись цен: %w",
			err,
		)
	}

	defer stmt.Close()

	for _, snapshot := range snapshots {
		if err := validateSnapshot(snapshot); err != nil {
			_ = tx.Rollback()

			return err
		}

		var volume any

		if snapshot.Volume24h != nil {
			volume = snapshot.Volume24h.String()
		}

		if _, err := stmt.ExecContext(
			ctx,
			snapshot.MarketID,
			utcText(snapshot.Timestamp),
			snapshot.Bid.String(),
			snapshot.Ask.String(),
			volume,
		); err != nil {
			_ = tx.Rollback()

			return fmt.Errorf(
				"сохранить пакет снимков цен: %w",
				err,
			)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf(
			"зафиксировать пакет снимков цен: %w",
			err,
		)
	}

	return nil
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
				volume_24h
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
			volume_24h
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
		return fmt.Errorf(
			"market_id должен быть положительным",
		)
	}

	if s.Timestamp.IsZero() {
		return fmt.Errorf(
			"timestamp снимка цены не задан",
		)
	}

	if !s.Bid.GreaterThan(decimal.Zero) ||
		!s.Ask.GreaterThan(decimal.Zero) {
		return fmt.Errorf(
			"bid и ask должны быть положительными",
		)
	}

	if s.Bid.GreaterThan(s.Ask) {
		return fmt.Errorf(
			"bid не может быть больше ask",
		)
	}

	if s.Volume24h != nil && s.Volume24h.IsNegative() {
		return fmt.Errorf(
			"volume_24h не может быть отрицательным",
		)
	}

	return nil
}

func scanSnapshot(s rowScanner) (PriceSnapshot, error) {
	var result PriceSnapshot
	var timestamp string
	var bid string
	var ask string
	var volume sql.NullString

	if err := s.Scan(
		&result.ID,
		&result.MarketID,
		&timestamp,
		&bid,
		&ask,
		&volume,
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
		return PriceSnapshot{}, fmt.Errorf(
			"разобрать bid %q: %w",
			bid,
			err,
		)
	}

	result.Ask, err = decimal.NewFromString(ask)
	if err != nil {
		return PriceSnapshot{}, fmt.Errorf(
			"разобрать ask %q: %w",
			ask,
			err,
		)
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

	return result, nil
}
