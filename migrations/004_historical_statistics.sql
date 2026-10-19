-- Журналирует циклы сканирования и связывает новые возможности с циклом обнаружения.
-- Старые наблюдения остаются без cycle_id: восстановить исходный цикл достоверно невозможно.
CREATE TABLE IF NOT EXISTS scan_cycles (
    id TEXT PRIMARY KEY,
    started_at TEXT NOT NULL,
    finished_at TEXT,
    status TEXT NOT NULL,
    enabled_exchanges INTEGER NOT NULL DEFAULT 0,
    succeeded_exchanges INTEGER NOT NULL DEFAULT 0,
    failed_exchanges INTEGER NOT NULL DEFAULT 0,
    markets_discovered INTEGER NOT NULL DEFAULT 0,
    tickers_received INTEGER NOT NULL DEFAULT 0,
    tickers_accepted INTEGER NOT NULL DEFAULT 0,
    tickers_rejected INTEGER NOT NULL DEFAULT 0,
    snapshots_saved INTEGER NOT NULL DEFAULT 0,
    opportunities_detected INTEGER NOT NULL DEFAULT 0,
    opportunities_saved INTEGER NOT NULL DEFAULT 0,
    persistence_errors INTEGER NOT NULL DEFAULT 0,
    min_spread_percent TEXT,
    error TEXT
);

ALTER TABLE opportunities ADD COLUMN cycle_id TEXT
    REFERENCES scan_cycles(id) ON DELETE RESTRICT;

-- Ускоряет статистику по направленным сочетаниям бирж и типов рынков.
CREATE INDEX IF NOT EXISTS idx_opportunities_direction_types_timestamp
    ON opportunities (
        buy_exchange, sell_exchange, buy_market_type, sell_market_type, timestamp
    );

-- Ускоряет потоковое чтение истории в порядке идентичности направления и времени.
CREATE INDEX IF NOT EXISTS idx_opportunities_history_identity_timestamp
    ON opportunities (
        base_asset, quote_asset, buy_exchange, buy_symbol,
        buy_market_type, buy_contract_type, sell_exchange, sell_symbol,
        sell_market_type, sell_contract_type, timestamp, id
    );

-- Ускоряет выборку зарегистрированных циклов за период.
CREATE INDEX IF NOT EXISTS idx_scan_cycles_started_at
    ON scan_cycles (started_at);
