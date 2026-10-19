package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
)

// ScanCycle содержит агрегированный результат одного исторического цикла сканирования.
type ScanCycle struct {
	ID                    string
	StartedAt             time.Time
	FinishedAt            time.Time
	Status                string
	EnabledExchanges      int
	SucceededExchanges    int
	FailedExchanges       int
	MarketsDiscovered     int
	TickersReceived       int
	TickersAccepted       int
	TickersRejected       int
	SnapshotsSaved        int
	OpportunitiesDetected int
	OpportunitiesSaved    int
	PersistenceErrors     int
	MinSpreadPercent      decimal.Decimal
	MinSpreadKnown        bool
	Error                 string
}

// HistoricalObservation содержит проверенную строку истории возможности.
type HistoricalObservation struct {
	ID               int64
	CycleID          string
	Timestamp        time.Time
	Symbol           string
	BaseAsset        string
	QuoteAsset       string
	BuyMarketID      int64
	BuyExchange      string
	BuySymbol        string
	BuyMarketType    string
	BuyContractType  string
	BuyPrice         decimal.Decimal
	SellMarketID     int64
	SellExchange     string
	SellSymbol       string
	SellMarketType   string
	SellContractType string
	SellPrice        decimal.Decimal
	SpreadPercent    decimal.Decimal
}

// StartScanCycle сохраняет запись о начале цикла до сбора рыночных данных.
func (d *DB) StartScanCycle(ctx context.Context, cycle ScanCycle) error {
	if d == nil || d.db == nil {
		return fmt.Errorf("SQLite не открыт")
	}
	if cycle.ID == "" || cycle.StartedAt.IsZero() {
		return fmt.Errorf("идентификатор и время начала цикла обязательны")
	}
	_, err := d.db.ExecContext(ctx, `INSERT INTO scan_cycles (
		id, started_at, status, enabled_exchanges, min_spread_percent
	) VALUES (?, ?, ?, ?, ?)`,
		cycle.ID, utcText(cycle.StartedAt), "running",
		cycle.EnabledExchanges, cycle.MinSpreadPercent.String())
	if err != nil {
		return fmt.Errorf("сохранить начало цикла %s: %w", cycle.ID, err)
	}
	return nil
}

// FinishScanCycle сохраняет окончательные показатели уже зарегистрированного цикла.
func (d *DB) FinishScanCycle(ctx context.Context, cycle ScanCycle) error {
	if d == nil || d.db == nil {
		return fmt.Errorf("SQLite не открыт")
	}
	if cycle.ID == "" || cycle.FinishedAt.IsZero() || cycle.Status == "" {
		return fmt.Errorf("идентификатор, время завершения и статус цикла обязательны")
	}
	result, err := d.db.ExecContext(ctx, `UPDATE scan_cycles SET
		finished_at = ?, status = ?, enabled_exchanges = ?,
		succeeded_exchanges = ?, failed_exchanges = ?, markets_discovered = ?,
		tickers_received = ?, tickers_accepted = ?, tickers_rejected = ?,
		snapshots_saved = ?, opportunities_detected = ?, opportunities_saved = ?,
		persistence_errors = ?, min_spread_percent = ?, error = ?
		WHERE id = ?`,
		utcText(cycle.FinishedAt), cycle.Status, cycle.EnabledExchanges,
		cycle.SucceededExchanges, cycle.FailedExchanges, cycle.MarketsDiscovered,
		cycle.TickersReceived, cycle.TickersAccepted, cycle.TickersRejected,
		cycle.SnapshotsSaved, cycle.OpportunitiesDetected, cycle.OpportunitiesSaved,
		cycle.PersistenceErrors, cycle.MinSpreadPercent.String(), nullableText(cycle.Error), cycle.ID)
	if err != nil {
		return fmt.Errorf("сохранить результат цикла %s: %w", cycle.ID, err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("проверить обновление цикла %s: %w", cycle.ID, err)
	}
	if affected != 1 {
		return fmt.Errorf("цикл %s не найден при сохранении результата", cycle.ID)
	}
	return nil
}

// StreamHistoricalObservations передаёт проверенные наблюдения в порядке идентичности и времени.
// Границы периода задаются полуинтервалом [since, until).
func (r *OpportunityRepository) StreamHistoricalObservations(
	ctx context.Context,
	since, until time.Time,
	visit func(HistoricalObservation) error,
) (total int64, skipped int64, err error) {
	if visit == nil {
		return 0, 0, fmt.Errorf("обработчик исторических наблюдений не задан")
	}
	if !since.IsZero() && !until.IsZero() && !since.Before(until) {
		return 0, 0, fmt.Errorf("начало периода должно быть раньше конца")
	}
	query := `SELECT id, cycle_id, timestamp, symbol, base_asset, quote_asset,
		buy_market_id, buy_exchange, COALESCE(buy_symbol, symbol),
		buy_market_type, buy_contract_type, buy_price,
		sell_market_id, sell_exchange, COALESCE(sell_symbol, symbol),
		sell_market_type, sell_contract_type, sell_price, spread_percent
		FROM opportunities WHERE 1 = 1`
	args := make([]any, 0, 2)
	if !since.IsZero() {
		query += " AND timestamp >= ?"
		args = append(args, coarseTimeBound(since, -2*time.Second))
	}
	if !until.IsZero() {
		query += " AND timestamp < ?"
		args = append(args, coarseTimeBound(until, 2*time.Second))
	}
	query += ` ORDER BY base_asset, quote_asset, buy_exchange,
		COALESCE(buy_symbol, symbol), buy_market_type, buy_contract_type,
		sell_exchange, COALESCE(sell_symbol, symbol), sell_market_type,
		sell_contract_type, julianday(timestamp), id`

	rows, queryErr := r.db.QueryContext(ctx, query, args...)
	if queryErr != nil {
		return 0, 0, fmt.Errorf("прочитать историю возможностей: %w", queryErr)
	}
	defer rows.Close()

	for rows.Next() {
		var item HistoricalObservation
		var cycleID sql.NullString
		var timestamp, buyPrice, sellPrice, spread string
		if scanErr := rows.Scan(
			&item.ID, &cycleID, &timestamp, &item.Symbol, &item.BaseAsset, &item.QuoteAsset,
			&item.BuyMarketID, &item.BuyExchange, &item.BuySymbol,
			&item.BuyMarketType, &item.BuyContractType, &buyPrice,
			&item.SellMarketID, &item.SellExchange, &item.SellSymbol,
			&item.SellMarketType, &item.SellContractType, &sellPrice, &spread,
		); scanErr != nil {
			skipped++
			continue
		}
		if cycleID.Valid {
			item.CycleID = cycleID.String
		}
		item.Timestamp, queryErr = parseTime(timestamp)
		if queryErr != nil {
			skipped++
			continue
		}
		if !inHistoricalPeriod(item.Timestamp, since, until) {
			continue
		}
		total++
		item.BuyPrice, queryErr = decimal.NewFromString(buyPrice)
		if queryErr != nil {
			skipped++
			continue
		}
		item.SellPrice, queryErr = decimal.NewFromString(sellPrice)
		if queryErr != nil {
			skipped++
			continue
		}
		item.SpreadPercent, queryErr = decimal.NewFromString(spread)
		if queryErr != nil || !validHistoricalObservation(item) {
			skipped++
			continue
		}
		if callbackErr := visit(item); callbackErr != nil {
			return total, skipped, callbackErr
		}
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return total, skipped, fmt.Errorf("прочитать строки истории возможностей: %w", rowsErr)
	}
	return total, skipped, nil
}

// coarseTimeBound расширяет SQL-границы, чтобы безопасно отфильтровать RFC3339Nano с разной точностью дробной части в Go.
func coarseTimeBound(value time.Time, delta time.Duration) string {
	return value.UTC().Add(delta).Truncate(time.Second).Format(time.RFC3339)
}

func inHistoricalPeriod(value, since, until time.Time) bool {
	return (since.IsZero() || !value.Before(since)) && (until.IsZero() || value.Before(until))
}

func validHistoricalObservation(item HistoricalObservation) bool {
	if item.ID <= 0 || item.Timestamp.IsZero() ||
		item.BaseAsset == "" || item.QuoteAsset != "USDT" ||
		item.BuyMarketID <= 0 || item.SellMarketID <= 0 ||
		item.BuyMarketID == item.SellMarketID ||
		item.BuyExchange == "" || item.SellExchange == "" ||
		item.BuyExchange == item.SellExchange ||
		!item.BuyPrice.IsPositive() || !item.SellPrice.GreaterThan(item.BuyPrice) ||
		!item.SpreadPercent.IsPositive() {
		return false
	}
	buyTypeOK := (item.BuyMarketType == "spot" && item.BuyContractType == "spot") ||
		(item.BuyMarketType == "futures" && item.BuyContractType == "perpetual")
	sellTypeOK := (item.SellMarketType == "spot" && item.SellContractType == "spot") ||
		(item.SellMarketType == "futures" && item.SellContractType == "perpetual")
	if !buyTypeOK || !sellTypeOK {
		return false
	}
	calculated := item.SellPrice.Div(item.BuyPrice).Sub(decimal.NewFromInt(1)).Mul(decimal.NewFromInt(100))
	return calculated.Equal(item.SpreadPercent)
}

// StreamScanCycles передаёт записи циклов, начавшихся в заданном полуинтервале.
func (d *DB) StreamScanCycles(
	ctx context.Context,
	since, until time.Time,
	visit func(ScanCycle) error,
) error {
	if visit == nil {
		return fmt.Errorf("обработчик циклов не задан")
	}
	query := `SELECT id, started_at, COALESCE(finished_at, ''), status,
		enabled_exchanges, succeeded_exchanges, failed_exchanges,
		markets_discovered, tickers_received, tickers_accepted, tickers_rejected,
		snapshots_saved, opportunities_detected, opportunities_saved,
		persistence_errors, COALESCE(min_spread_percent, ''), COALESCE(error, '')
		FROM scan_cycles WHERE 1 = 1`
	args := make([]any, 0, 2)
	if !since.IsZero() {
		query += " AND started_at >= ?"
		args = append(args, coarseTimeBound(since, -2*time.Second))
	}
	if !until.IsZero() {
		query += " AND started_at < ?"
		args = append(args, coarseTimeBound(until, 2*time.Second))
	}
	query += " ORDER BY started_at, id"
	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("прочитать историю циклов: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cycle ScanCycle
		var startedAt, finishedAt, minSpread string
		if err := rows.Scan(
			&cycle.ID, &startedAt, &finishedAt, &cycle.Status,
			&cycle.EnabledExchanges, &cycle.SucceededExchanges, &cycle.FailedExchanges,
			&cycle.MarketsDiscovered, &cycle.TickersReceived, &cycle.TickersAccepted,
			&cycle.TickersRejected, &cycle.SnapshotsSaved, &cycle.OpportunitiesDetected,
			&cycle.OpportunitiesSaved, &cycle.PersistenceErrors, &minSpread, &cycle.Error,
		); err != nil {
			return fmt.Errorf("прочитать историю цикла: %w", err)
		}
		cycle.StartedAt, err = parseTime(startedAt)
		if err != nil {
			return err
		}
		if !inHistoricalPeriod(cycle.StartedAt, since, until) {
			continue
		}
		if finishedAt != "" {
			cycle.FinishedAt, err = parseTime(finishedAt)
			if err != nil {
				return err
			}
		}
		if minSpread != "" {
			cycle.MinSpreadPercent, err = decimal.NewFromString(minSpread)
			if err != nil {
				return fmt.Errorf("разобрать минимальный спред цикла %s: %w", cycle.ID, err)
			}
			cycle.MinSpreadKnown = true
		}
		if err := visit(cycle); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("обработать историю циклов: %w", err)
	}
	return nil
}
