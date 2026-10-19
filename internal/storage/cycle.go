package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/example/crypto-arbitrage-scanner/internal/market"
)

// MarketSnapshotInput объединяет рынок и относящийся к нему снимок для атомарного сохранения.
type MarketSnapshotInput struct {
	Market   market.Market
	Snapshot PriceSnapshot
}

// PersistedMarketSnapshot возвращает идентификаторы сохранённых рынка и снимка.
type PersistedMarketSnapshot struct {
	Market     market.Market
	MarketID   int64
	SnapshotID int64
	Snapshot   PriceSnapshot
}

// PersistMarketSnapshots сохраняет рынки и снимки одной биржи в одной SQLite-транзакции.
func (d *DB) PersistMarketSnapshots(
	ctx context.Context,
	markets []market.Market,
	inputs []MarketSnapshotInput,
) ([]PersistedMarketSnapshot, error) {
	if d == nil || d.db == nil {
		return nil, fmt.Errorf("SQLite не открыт")
	}
	normalizedMarkets := make([]market.Market, 0, len(markets))
	for _, item := range markets {
		normalized, err := market.NormalizeMarket(item)
		if err != nil {
			return nil, fmt.Errorf("нормализовать рынок %s/%s: %w", item.Exchange, item.Symbol, err)
		}
		if !market.IsSupportedMarket(normalized) {
			return nil, fmt.Errorf("попытка сохранить неподдерживаемый рынок %s/%s", item.Exchange, item.Symbol)
		}
		normalizedMarkets = append(normalizedMarkets, normalized)
	}

	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("начать транзакцию рынков и снимков: %w", err)
	}
	rollback := true
	defer func() {
		if rollback {
			_ = tx.Rollback()
		}
	}()

	now := utcText(time.Now().UTC())
	upsertMarket, err := tx.PrepareContext(ctx, `INSERT INTO markets(
		exchange, symbol, base_asset, quote_asset, market_type, contract_type,
		active, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(exchange, symbol, market_type, contract_type) DO UPDATE SET
		base_asset = excluded.base_asset,
		quote_asset = excluded.quote_asset,
		active = excluded.active,
		updated_at = excluded.updated_at`)
	if err != nil {
		return nil, fmt.Errorf("подготовить запись рынков: %w", err)
	}
	defer upsertMarket.Close()

	marketIDs := make(map[string]int64, len(normalizedMarkets))
	for _, item := range normalizedMarkets {
		if _, err := upsertMarket.ExecContext(
			ctx, item.Exchange, item.Symbol, item.BaseAsset, item.QuoteAsset,
			item.MarketType, item.ContractType, boolInt(item.Active), now, now,
		); err != nil {
			return nil, fmt.Errorf("сохранить рынок %s/%s: %w", item.Exchange, item.Symbol, err)
		}
		var id int64
		if err := tx.QueryRowContext(
			ctx,
			`SELECT id FROM markets
			 WHERE exchange = ? AND symbol = ? AND market_type = ? AND contract_type = ?`,
			item.Exchange, item.Symbol, item.MarketType, item.ContractType,
		).Scan(&id); err != nil {
			return nil, fmt.Errorf("получить id рынка %s/%s: %w", item.Exchange, item.Symbol, err)
		}
		marketIDs[marketStorageKey(item)] = id
	}

	insertSnapshot, err := tx.PrepareContext(ctx, `INSERT INTO price_snapshots(
		market_id, timestamp, bid, ask, volume_24h, received_at, exchange_timestamp
	) VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return nil, fmt.Errorf("подготовить запись снимков цен: %w", err)
	}
	defer insertSnapshot.Close()

	saved := make([]PersistedMarketSnapshot, 0, len(inputs))
	for _, input := range inputs {
		normalized, err := market.NormalizeMarket(input.Market)
		if err != nil {
			return nil, fmt.Errorf("нормализовать рынок снимка %s/%s: %w", input.Market.Exchange, input.Market.Symbol, err)
		}
		key := marketStorageKey(normalized)
		marketID, ok := marketIDs[key]
		if !ok {
			return nil, fmt.Errorf("снимок ссылается на рынок вне пакета: %s", key)
		}
		snapshot := input.Snapshot
		snapshot.MarketID = marketID
		if err := validateSnapshot(snapshot); err != nil {
			return nil, fmt.Errorf("проверить снимок рынка %s/%s: %w", normalized.Exchange, normalized.Symbol, err)
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
		result, err := insertSnapshot.ExecContext(
			ctx, snapshot.MarketID, utcText(snapshot.Timestamp),
			snapshot.Bid.String(), snapshot.Ask.String(), volume, receivedAt, exchangeTimestamp,
		)
		if err != nil {
			return nil, fmt.Errorf("сохранить снимок рынка %s/%s: %w", normalized.Exchange, normalized.Symbol, err)
		}
		snapshotID, err := result.LastInsertId()
		if err != nil {
			return nil, fmt.Errorf("получить id снимка рынка %s/%s: %w", normalized.Exchange, normalized.Symbol, err)
		}
		snapshot.ID = snapshotID
		saved = append(saved, PersistedMarketSnapshot{
			Market: normalized, MarketID: marketID, SnapshotID: snapshotID, Snapshot: snapshot,
		})
	}
	if err := insertSnapshot.Close(); err != nil {
		return nil, fmt.Errorf("закрыть пакетную запись снимков: %w", err)
	}
	if err := upsertMarket.Close(); err != nil {
		return nil, fmt.Errorf("закрыть пакетную запись рынков: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("зафиксировать рынки и снимки SQLite: %w", err)
	}
	rollback = false
	return saved, nil
}

func marketStorageKey(item market.Market) string {
	return strings.ToLower(strings.TrimSpace(item.Exchange)) + "|" +
		strings.ToUpper(strings.TrimSpace(item.Symbol)) + "|" +
		string(item.MarketType) + "|" + string(item.ContractType)
}

// Ensure the public transaction helper remains type-checked against database/sql executors.
var _ interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
} = (*sql.Tx)(nil)
