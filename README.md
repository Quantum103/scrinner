# Crypto Arbitrage Scanner

Исследовательский scanner межбиржевого криптовалютного арбитража на Go. Приложение работает только на чтение публичных рыночных данных, не использует API keys и не выполняет торговые операции.

## Поддерживаемые рынки

- Spot с котируемым активом USDT.
- Бессрочные USDⓈ-M Futures с маржой и расчётами в USDT.

## Текущий этап: Stage 5 — адаптеры Bybit и Binance

Адаптеры преобразуют метаданные рынков и лучшие bid/ask в общие внутренние структуры, а рынки и снимки цен записываются в SQLite через существующий storage layer.

### Официальные endpoints Bybit V5

- `GET /v5/market/instruments-info?category=spot` — Spot-инструменты; отбираются активные рынки со статусом `Trading`. Документация: https://bybit-exchange.github.io/docs/v5/market/instrument
- `GET /v5/market/instruments-info?category=linear` — Linear-инструменты; используется pagination через `nextPageCursor`. Оставляются только `Trading`, `LinearPerpetual`, `quoteCoin=USDT`, `settleCoin=USDT`. Документация: https://bybit-exchange.github.io/docs/v5/market/instrument
- `GET /v5/market/tickers?category=spot` и `GET /v5/market/tickers?category=linear` — лучшие bid/ask и поле `volume24h`, которое представляет объём базового актива. Время ответа API используется как timestamp для этих категорий. Документация: https://bybit-exchange.github.io/docs/v5/market/tickers
- Временные HTTP/API ошибки обрабатываются с ограниченными повторами. Документация по ограничениям: https://bybit-exchange.github.io/docs/v5/rate-limit

### Официальные endpoints Binance

- `GET /api/v3/exchangeInfo` — метаданные Spot-инструментов. Отбираются только активные Spot USDT рынки, где `status=TRADING` и `isSpotTradingAllowed=true`. Документация: https://developers.binance.com/docs/binance-spot-api-docs/rest-api/general-endpoints
- `GET /fapi/v1/exchangeInfo` — метаданные USDⓈ-M Futures. Оставляются только `status=TRADING`, `contractType=PERPETUAL`, `quoteAsset=USDT` и `marginAsset=USDT`; срочные контракты исключаются. Документация: https://developers.binance.com/docs/derivatives/usds-margined-futures/market-data/rest-api/Exchange-Information
- `GET /api/v3/ticker/bookTicker` — массовое получение лучших Spot `bidPrice` и `askPrice`. Этот ответ не содержит времени формирования котировки, поэтому `Timestamp` использует локальное время получения, а `ExchangeTimestamp` остаётся пустым. Документация: https://developers.binance.com/docs/binance-spot-api-docs/rest-api/market-data-endpoints
- `GET /fapi/v1/ticker/bookTicker` — массовое получение лучших Futures `bidPrice` и `askPrice`; поле `time`, если оно есть и корректно, сохраняется как время биржи. Документация: https://developers.binance.com/docs/derivatives/usds-margined-futures/market-data/rest-api/Symbol-Price-Ticker
- `GET /api/v3/ticker/24hr` — Spot-объём за 24 часа. Запросы с `symbols` выполняются пакетами по максимум 100 символов. Поле `volume` представляет объём базового актива; `quoteVolume` намеренно не подменяет его. Документация: https://developers.binance.com/docs/binance-spot-api-docs/rest-api/market-data-endpoints
- `GET /fapi/v1/ticker/24hr` — USDⓈ-M Futures-объём за 24 часа; для согласованности используется поле `volume` (объём базового актива), а не `quoteVolume`. Документация: https://developers.binance.com/docs/derivatives/usds-margined-futures/market-data/rest-api/24hr-Ticker-Price-Change-Statistics

Binance использует публичные endpoints без API keys. HTTP-клиент повторно использует соединения, применяет timeout, учитывает отмену context, ограничивает размер ответа и количество retry. HTTP 418 не повторяется, чтобы не усиливать временную блокировку.

### Timestamp и точность

- Финансовые поля цены и объёма разбираются с помощью `shopspring/decimal`, без преобразования в `float64`.
- `Timestamp` — временная точка снимка, которую использует существующая история цен.
- `ReceivedAt` — локальное UTC-время получения ответа.
- `ExchangeTimestamp` — время котировки, предоставленное биржей, либо `NULL`, если endpoint его не сообщает.
- В SQLite добавляется миграция `002_price_snapshot_timestamps.sql`; существующие строки остаются совместимыми благодаря nullable полям.

## Конфигурация

В `configs/config.yaml` адаптеры включаются независимо:

```yaml
exchanges:
  binance:
    enabled: true
  bybit:
    enabled: true
```

Выключенный адаптер не обращается к API. Если включены обе биржи, обычный запуск выполняет один проход для Bybit и один для Binance. Постоянный scheduler и arbitrage detector в Stage 5 не добавляются.

## Запуск и проверки

Запускать из корня репозитория:

```bash
go test ./...
go vet ./...
go run ./cmd/scanner
```

Unit-тесты используют `httptest` и не требуют публичного доступа к биржам. Если сеть ограничивает доступ к API, адаптер вернёт ошибку и не будет создавать фиктивные рыночные данные.
