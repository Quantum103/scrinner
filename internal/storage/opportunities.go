package storage

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/example/crypto-arbitrage-scanner/internal/arbitrage"
	"github.com/example/crypto-arbitrage-scanner/internal/market"
	"github.com/shopspring/decimal"
)

// OpportunityRepository содержит операции хранения найденных арбитражных возможностей.
type OpportunityRepository struct {
	db *sql.DB
}

// NewOpportunityRepository создаёт репозиторий арбитражных возможностей.
func NewOpportunityRepository(db *DB) *OpportunityRepository {
	return &OpportunityRepository{db: db.db}
}

// InsertOpportunity сохраняет конкретную арбитражную возможность.
func (r *OpportunityRepository) InsertOpportunity(
	ctx context.Context,
	o arbitrage.Opportunity,
) (int64, error) {
	if err := validateOpportunity(o); err != nil {
		return 0, err
	}

	result, err := r.db.ExecContext(
		ctx,
		`INSERT INTO opportunities(
			timestamp,
			symbol,
			base_asset,
			quote_asset,
			buy_market_id,
			buy_exchange,
			buy_market_type,
			buy_contract_type,
			buy_price,
			sell_market_id,
			sell_exchange,
			sell_market_type,
			sell_contract_type,
			sell_price,
			spread_percent
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		utcText(o.Timestamp),
		o.Symbol,
		o.BaseAsset,
		o.QuoteAsset,
		o.BuyMarketID,
		o.BuyExchange,
		o.BuyMarketType,
		o.BuyContractType,
		o.BuyPrice.String(),
		o.SellMarketID,
		o.SellExchange,
		o.SellMarketType,
		o.SellContractType,
		o.SellPrice.String(),
		o.SpreadPercent.String(),
	)
	if err != nil {
		return 0, fmt.Errorf(
			"сохранить opportunity: %w",
			err,
		)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf(
			"получить id opportunity: %w",
			err,
		)
	}

	return id, nil
}

// GetRecentOpportunities возвращает последние найденные возможности.
func (r *OpportunityRepository) GetRecentOpportunities(
	ctx context.Context,
	limit int,
) ([]arbitrage.Opportunity, error) {
	if limit <= 0 {
		return nil, fmt.Errorf(
			"limit должен быть положительным",
		)
	}

	return r.list(
		ctx,
		`SELECT
			timestamp,
			symbol,
			base_asset,
			quote_asset,
			buy_market_id,
			buy_exchange,
			buy_market_type,
			buy_contract_type,
			buy_price,
			sell_market_id,
			sell_exchange,
			sell_market_type,
			sell_contract_type,
			sell_price,
			spread_percent
		 FROM opportunities
		 ORDER BY timestamp DESC, id DESC
		 LIMIT ?`,
		limit,
	)
}

// GetOpportunitiesBySymbol возвращает возможности по символу за период.
func (r *OpportunityRepository) GetOpportunitiesBySymbol(
	ctx context.Context,
	symbol string,
	from time.Time,
	to time.Time,
) ([]arbitrage.Opportunity, error) {
	return r.list(
		ctx,
		`SELECT
			timestamp,
			symbol,
			base_asset,
			quote_asset,
			buy_market_id,
			buy_exchange,
			buy_market_type,
			buy_contract_type,
			buy_price,
			sell_market_id,
			sell_exchange,
			sell_market_type,
			sell_contract_type,
			sell_price,
			spread_percent
		 FROM opportunities
		 WHERE symbol = ?
		   AND timestamp >= ?
		   AND timestamp <= ?
		 ORDER BY timestamp ASC, id ASC`,
		symbol,
		utcText(from),
		utcText(to),
	)
}

// GetOpportunitiesByPeriod возвращает возможности за заданный период.
func (r *OpportunityRepository) GetOpportunitiesByPeriod(
	ctx context.Context,
	from time.Time,
	to time.Time,
) ([]arbitrage.Opportunity, error) {
	return r.list(
		ctx,
		`SELECT
			timestamp,
			symbol,
			base_asset,
			quote_asset,
			buy_market_id,
			buy_exchange,
			buy_market_type,
			buy_contract_type,
			buy_price,
			sell_market_id,
			sell_exchange,
			sell_market_type,
			sell_contract_type,
			sell_price,
			spread_percent
		 FROM opportunities
		 WHERE timestamp >= ?
		   AND timestamp <= ?
		 ORDER BY timestamp ASC, id ASC`,
		utcText(from),
		utcText(to),
	)
}

// GetOpportunitiesByExchanges возвращает возможности для заданной пары бирж.
func (r *OpportunityRepository) GetOpportunitiesByExchanges(
	ctx context.Context,
	buyExchange string,
	sellExchange string,
	from time.Time,
	to time.Time,
) ([]arbitrage.Opportunity, error) {
	return r.list(
		ctx,
		`SELECT
			timestamp,
			symbol,
			base_asset,
			quote_asset,
			buy_market_id,
			buy_exchange,
			buy_market_type,
			buy_contract_type,
			buy_price,
			sell_market_id,
			sell_exchange,
			sell_market_type,
			sell_contract_type,
			sell_price,
			spread_percent
		 FROM opportunities
		 WHERE buy_exchange = ?
		   AND sell_exchange = ?
		   AND timestamp >= ?
		   AND timestamp <= ?
		 ORDER BY timestamp ASC, id ASC`,
		buyExchange,
		sellExchange,
		utcText(from),
		utcText(to),
	)
}

// GetOpportunitiesBySpread возвращает возможности с точным decimal-сравнением spread.
func (r *OpportunityRepository) GetOpportunitiesBySpread(
	ctx context.Context,
	minSpread decimal.Decimal,
	from time.Time,
	to time.Time,
) ([]arbitrage.Opportunity, error) {
	items, err := r.list(
		ctx,
		`SELECT
			timestamp,
			symbol,
			base_asset,
			quote_asset,
			buy_market_id,
			buy_exchange,
			buy_market_type,
			buy_contract_type,
			buy_price,
			sell_market_id,
			sell_exchange,
			sell_market_type,
			sell_contract_type,
			sell_price,
			spread_percent
		 FROM opportunities
		 WHERE timestamp >= ?
		   AND timestamp <= ?
		 ORDER BY timestamp ASC, id ASC`,
		utcText(from),
		utcText(to),
	)
	if err != nil {
		return nil, err
	}

	result := make([]arbitrage.Opportunity, 0, len(items))

	for _, item := range items {
		if item.SpreadPercent.GreaterThanOrEqual(minSpread) {
			result = append(result, item)
		}
	}

	return result, nil
}

func (r *OpportunityRepository) list(
	ctx context.Context,
	query string,
	args ...any,
) ([]arbitrage.Opportunity, error) {
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf(
			"получить opportunities: %w",
			err,
		)
	}
	defer rows.Close()

	result := make([]arbitrage.Opportunity, 0)

	for rows.Next() {
		opportunity, err := scanOpportunity(rows)
		if err != nil {
			return nil, err
		}

		result = append(result, opportunity)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf(
			"обработать opportunities: %w",
			err,
		)
	}

	return result, nil
}

func validateOpportunity(o arbitrage.Opportunity) error {
	if o.Timestamp.IsZero() {
		return fmt.Errorf(
			"timestamp opportunity не задан",
		)
	}

	if o.Symbol == "" ||
		o.BaseAsset == "" ||
		o.QuoteAsset == "" {
		return fmt.Errorf(
			"symbol, base_asset и quote_asset обязательны",
		)
	}

	if o.BuyMarketID <= 0 ||
		o.SellMarketID <= 0 {
		return fmt.Errorf(
			"buy_market_id и sell_market_id должны быть положительными",
		)
	}

	if o.BuyExchange == "" ||
		o.SellExchange == "" {
		return fmt.Errorf(
			"биржи покупки и продажи обязательны",
		)
	}

	if !o.BuyPrice.GreaterThan(decimal.Zero) ||
		!o.SellPrice.GreaterThan(decimal.Zero) {
		return fmt.Errorf(
			"цены покупки и продажи должны быть положительными",
		)
	}

	if o.SpreadPercent.IsNegative() {
		return fmt.Errorf(
			"spread_percent не может быть отрицательным",
		)
	}

	return nil
}

func scanOpportunity(
	s rowScanner,
) (arbitrage.Opportunity, error) {
	var opportunity arbitrage.Opportunity

	var timestamp string
	var buyType string
	var buyContract string
	var buyPrice string
	var sellType string
	var sellContract string
	var sellPrice string
	var spread string

	if err := s.Scan(
		&timestamp,
		&opportunity.Symbol,
		&opportunity.BaseAsset,
		&opportunity.QuoteAsset,
		&opportunity.BuyMarketID,
		&opportunity.BuyExchange,
		&buyType,
		&buyContract,
		&buyPrice,
		&opportunity.SellMarketID,
		&opportunity.SellExchange,
		&sellType,
		&sellContract,
		&sellPrice,
		&spread,
	); err != nil {
		return arbitrage.Opportunity{}, fmt.Errorf(
			"прочитать opportunity: %w",
			err,
		)
	}

	var err error

	opportunity.Timestamp, err = parseTime(timestamp)
	if err != nil {
		return arbitrage.Opportunity{}, err
	}

	opportunity.BuyMarketType = market.MarketType(buyType)
	opportunity.BuyContractType = market.ContractType(buyContract)
	opportunity.SellMarketType = market.MarketType(sellType)
	opportunity.SellContractType = market.ContractType(sellContract)

	opportunity.BuyPrice, err = decimal.NewFromString(buyPrice)
	if err != nil {
		return arbitrage.Opportunity{}, fmt.Errorf(
			"разобрать buy_price: %w",
			err,
		)
	}

	opportunity.SellPrice, err = decimal.NewFromString(sellPrice)
	if err != nil {
		return arbitrage.Opportunity{}, fmt.Errorf(
			"разобрать sell_price: %w",
			err,
		)
	}

	opportunity.SpreadPercent, err = decimal.NewFromString(spread)
	if err != nil {
		return arbitrage.Opportunity{}, fmt.Errorf(
			"разобрать spread_percent: %w",
			err,
		)
	}

	return opportunity, nil
}
