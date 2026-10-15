-- Добавляет раздельное хранение времени получения ответа и времени котировки биржи.
-- NULL сохраняет совместимость со снимками от адаптеров, не заполняющих эти поля.
ALTER TABLE price_snapshots ADD COLUMN received_at TEXT;
ALTER TABLE price_snapshots ADD COLUMN exchange_timestamp TEXT;
