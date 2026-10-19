-- Добавляет ссылки на снимки, использованные при обнаружении возможности, и исходные символы рынков.
ALTER TABLE opportunities ADD COLUMN buy_symbol TEXT;
ALTER TABLE opportunities ADD COLUMN sell_symbol TEXT;
ALTER TABLE opportunities ADD COLUMN buy_snapshot_id INTEGER
    REFERENCES price_snapshots(id) ON DELETE RESTRICT;
ALTER TABLE opportunities ADD COLUMN sell_snapshot_id INTEGER
    REFERENCES price_snapshots(id) ON DELETE RESTRICT;

-- Ускоряет переход от исторической возможности к снимкам, на которых она основана.
CREATE INDEX IF NOT EXISTS idx_opportunities_buy_snapshot_id
    ON opportunities (buy_snapshot_id);
CREATE INDEX IF NOT EXISTS idx_opportunities_sell_snapshot_id
    ON opportunities (sell_snapshot_id);
