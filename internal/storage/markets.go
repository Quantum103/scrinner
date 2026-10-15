package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/example/crypto-arbitrage-scanner/internal/market"
)

// MarketRepository содержит операции хранения торговых рынков.
type MarketRepository struct {
	db *sql.DB
}

// NewMarketRepository создаёт репозиторий рынков на основе открытой базы.
func NewMarketRepository(db *DB) *MarketRepository {
	return &MarketRepository{db: db.db}
}

// CreateMarket добавляет новый рынок и возвращает его идентификатор.
func (r *MarketRepository) CreateMarket(
	ctx context.Context,
	m market.Market,
) (int64, error) {
	if err := validateMarket(m); err != nil {
		return 0, err
	}

	now := utcText(time.Now())

	result, err := r.db.ExecContext(
		ctx,
		`INSERT INTO markets(
			exchange,
			symbol,
			base_asset,
			quote_asset,
			market_type,
			contract_type,
			active,
			created_at,
			updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.Exchange,
		m.Symbol,
		m.BaseAsset,
		m.QuoteAsset,
		m.MarketType,
		m.ContractType,
		boolInt(m.Active),
		now,
		now,
	)
	if err != nil {
		return 0, fmt.Errorf(
			"создать рынок %s/%s: %w",
			m.Exchange,
			m.Symbol,
			err,
		)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf(
			"получить id созданного рынка: %w",
			err,
		)
	}

	return id, nil
}

// UpsertMarket добавляет рынок или обновляет существующий по его естественному ключу.
func (r *MarketRepository) UpsertMarket(
	ctx context.Context,
	m market.Market,
) (int64, error) {
	if err := validateMarket(m); err != nil {
		return 0, err
	}

	now := utcText(time.Now())

	_, err := r.db.ExecContext(
		ctx,
		`INSERT INTO markets(
			exchange,
			symbol,
			base_asset,
			quote_asset,
			market_type,
			contract_type,
			active,
			created_at,
			updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(
			exchange,
			symbol,
			market_type,
			contract_type
		)
		DO UPDATE SET
			base_asset = excluded.base_asset,
			quote_asset = excluded.quote_asset,
			active = excluded.active,
			updated_at = excluded.updated_at`,
		m.Exchange,
		m.Symbol,
		m.BaseAsset,
		m.QuoteAsset,
		m.MarketType,
		m.ContractType,
		boolInt(m.Active),
		now,
		now,
	)
	if err != nil {
		return 0, fmt.Errorf(
			"обновить рынок %s/%s: %w",
			m.Exchange,
			m.Symbol,
			err,
		)
	}

	var id int64

	err = r.db.QueryRowContext(
		ctx,
		`SELECT id
		 FROM markets
		 WHERE exchange = ?
		   AND symbol = ?
		   AND market_type = ?
		   AND contract_type = ?`,
		m.Exchange,
		m.Symbol,
		m.MarketType,
		m.ContractType,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf(
			"получить id рынка после upsert: %w",
			err,
		)
	}

	return id, nil
}

// GetMarketByID возвращает рынок по идентификатору.
func (r *MarketRepository) GetMarketByID(
	ctx context.Context,
	id int64,
) (market.Market, error) {
	return scanMarket(
		r.db.QueryRowContext(
			ctx,
			`SELECT
				exchange,
				symbol,
				base_asset,
				quote_asset,
				market_type,
				contract_type,
				active
			 FROM markets
			 WHERE id = ?`,
			id,
		),
	)
}

// GetMarket возвращает рынок по бирже, символу и типу инструмента.
func (r *MarketRepository) GetMarket(
	ctx context.Context,
	exchange string,
	symbol string,
	marketType market.MarketType,
	contractType market.ContractType,
) (market.Market, error) {
	return scanMarket(
		r.db.QueryRowContext(
			ctx,
			`SELECT
				exchange,
				symbol,
				base_asset,
				quote_asset,
				market_type,
				contract_type,
				active
			 FROM markets
			 WHERE exchange = ?
			   AND symbol = ?
			   AND market_type = ?
			   AND contract_type = ?`,
			exchange,
			symbol,
			marketType,
			contractType,
		),
	)
}

// ListMarkets возвращает все рынки в порядке их идентификаторов.
func (r *MarketRepository) ListMarkets(
	ctx context.Context,
) ([]market.Market, error) {
	return r.list(
		ctx,
		`SELECT
			exchange,
			symbol,
			base_asset,
			quote_asset,
			market_type,
			contract_type,
			active
		 FROM markets
		 ORDER BY id`,
	)
}

// ListActiveMarkets возвращает только активные рынки.
func (r *MarketRepository) ListActiveMarkets(
	ctx context.Context,
) ([]market.Market, error) {
	return r.list(
		ctx,
		`SELECT
			exchange,
			symbol,
			base_asset,
			quote_asset,
			market_type,
			contract_type,
			active
		 FROM markets
		 WHERE active = 1
		 ORDER BY id`,
	)
}

func (r *MarketRepository) list(
	ctx context.Context,
	query string,
) ([]market.Market, error) {
	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf(
			"получить список рынков: %w",
			err,
		)
	}
	defer rows.Close()

	result := make([]market.Market, 0)

	for rows.Next() {
		m, err := scanMarket(rows)
		if err != nil {
			return nil, err
		}

		result = append(result, m)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(
			"обработать рынки: %w",
			err,
		)
	}

	return result, nil
}

type rowScanner interface {
	Scan(...any) error
}

func scanMarket(s rowScanner) (market.Market, error) {
	var result market.Market
	var active int
	var marketType string
	var contractType string

	if err := s.Scan(
		&result.Exchange,
		&result.Symbol,
		&result.BaseAsset,
		&result.QuoteAsset,
		&marketType,
		&contractType,
		&active,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return market.Market{}, sql.ErrNoRows
		}

		return market.Market{}, fmt.Errorf(
			"прочитать рынок: %w",
			err,
		)
	}

	result.MarketType = market.MarketType(marketType)
	result.ContractType = market.ContractType(contractType)
	result.Active = active != 0

	return result, nil
}

func validateMarket(m market.Market) error {
	if m.Exchange == "" ||
		m.Symbol == "" ||
		m.BaseAsset == "" ||
		m.QuoteAsset == "" {
		return fmt.Errorf(
			"exchange, symbol, base_asset и quote_asset обязательны",
		)
	}

	if m.QuoteAsset != "USDT" {
		return fmt.Errorf(
			"поддерживаются только рынки с quote_asset=USDT",
		)
	}

	switch {
	case m.MarketType == market.MarketTypeSpot &&
		m.ContractType == market.ContractTypeSpot:
	case m.MarketType == market.MarketTypeFutures &&
		m.ContractType == market.ContractTypePerpetual:
	default:
		return fmt.Errorf(
			"неподдерживаемая комбинация market_type=%q и contract_type=%q",
			m.MarketType,
			m.ContractType,
		)
	}

	return nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}

	return 0
}
