-- Создаёт таблицу версий применённых миграций.
CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL
);

-- Хранит конкретные торговые рынки бирж.
CREATE TABLE IF NOT EXISTS markets (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    exchange TEXT NOT NULL,
    symbol TEXT NOT NULL,
    base_asset TEXT NOT NULL,
    quote_asset TEXT NOT NULL,
    market_type TEXT NOT NULL,
    contract_type TEXT NOT NULL,
    active INTEGER NOT NULL CHECK (active IN (0, 1)),
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE (exchange, symbol, market_type, contract_type)
);

-- Хранит исторические снимки лучшего bid/ask и объёма.
CREATE TABLE IF NOT EXISTS price_snapshots (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    market_id INTEGER NOT NULL,
    timestamp TEXT NOT NULL,
    bid TEXT NOT NULL,
    ask TEXT NOT NULL,
    volume_24h TEXT,
    FOREIGN KEY (market_id) REFERENCES markets(id) ON DELETE RESTRICT
);

-- Хранит конкретные арбитражные возможности между двумя рынками.
CREATE TABLE IF NOT EXISTS opportunities (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    timestamp TEXT NOT NULL,
    symbol TEXT NOT NULL,
    base_asset TEXT NOT NULL,
    quote_asset TEXT NOT NULL,
    buy_market_id INTEGER NOT NULL,
    buy_exchange TEXT NOT NULL,
    buy_market_type TEXT NOT NULL,
    buy_contract_type TEXT NOT NULL,
    buy_price TEXT NOT NULL,
    sell_market_id INTEGER NOT NULL,
    sell_exchange TEXT NOT NULL,
    sell_market_type TEXT NOT NULL,
    sell_contract_type TEXT NOT NULL,
    sell_price TEXT NOT NULL,
    spread_percent TEXT NOT NULL,
    FOREIGN KEY (buy_market_id) REFERENCES markets(id) ON DELETE RESTRICT,
    FOREIGN KEY (sell_market_id) REFERENCES markets(id) ON DELETE RESTRICT
);

-- Ускоряет выборку снимков по рынку и времени.
CREATE INDEX IF NOT EXISTS idx_price_snapshots_market_timestamp
    ON price_snapshots (market_id, timestamp);

-- Ускоряет исторические выборки снимков по времени.
CREATE INDEX IF NOT EXISTS idx_price_snapshots_timestamp
    ON price_snapshots (timestamp);

-- Ускоряет аналитику по активу и времени.
CREATE INDEX IF NOT EXISTS idx_opportunities_base_asset_timestamp
    ON opportunities (base_asset, timestamp);

-- Ускоряет выборку возможностей по символу и времени.
CREATE INDEX IF NOT EXISTS idx_opportunities_symbol_timestamp
    ON opportunities (symbol, timestamp);

-- Ускоряет аналитику по направлению между биржами и времени.
CREATE INDEX IF NOT EXISTS idx_opportunities_exchange_pair_timestamp
    ON opportunities (buy_exchange, sell_exchange, timestamp);

-- Ускоряет выборку возможностей по времени.
CREATE INDEX IF NOT EXISTS idx_opportunities_timestamp
    ON opportunities (timestamp);