# Crypto Arbitrage Scanner

Исследовательский сканер межбиржевого криптовалютного арбитража на Go. Приложение получает публичные рыночные данные, сохраняет рынки и снимки цен в SQLite. API-ключи не используются, торговые операции не выполняются.

## Поддерживаемые рынки

- Spot, котируемый актив — USDT.
- Бессрочные фьючерсы с расчётом в USDT.

## Текущие адаптеры

- **Bybit V5** — Spot и Linear Perpetual.
- **Binance** — Spot и USDⓈ-M Perpetual.
- **MEXC** — Spot и USDT Perpetual.
- **OKX** — Spot и линейные USDT-margined SWAP Perpetual.

Адаптеры используют общие модели `Market` и `Ticker`, нормализатор и существующие SQLite-репозитории. Spot и Perpetual остаются отдельными рынками, даже если относятся к одному базовому активу. Оригинальные символы бирж сохраняются без изменения.

## MEXC API

Для Spot v3 и актуального Futures API используется базовый адрес `https://api.mexc.com`. Согласно официальному журналу MEXC, базовый домен Futures API сменился с `contract.mexc.com` на `api.mexc.com` 19 января 2026 года: https://www.mexc.fm/api-docs/futures/update-log

| Назначение | HTTP-метод и путь | Поля / ограничения |
|---|---|---|
| Spot metadata | `GET /api/v3/exchangeInfo` | `status=1`, `baseAsset`, `quoteAsset=USDT`, `isSpotTradingAllowed`, `permissions`, `tradeSideType`. Вес IP: 25. |
| Spot best bid/ask | `GET /api/v3/ticker/bookTicker` | `bidPrice` / `askPrice`; без `symbol` возвращается массив всех пар. Вес IP: 10. |
| Spot 24h statistics | `GET /api/v3/ticker/24hr` | `volume` — базовый актив, `quoteVolume` — котируемый оборот. Адаптер записывает только `volume`. Вес IP: 25. |
| Futures contract metadata | `GET /api/v1/contract/detail/country` | `futureType=1`, `state=0`, `type=1` при наличии, `quoteCoin=USDT`, `settleCoin=USDT`, `contractSize`. Лимит: 10 запросов за 2 секунды. |
| Futures best bid/ask and stats | `GET /api/v1/contract/ticker` | `bid1` / `ask1`, `timestamp`, `volume24` (контракты), `amount24` (оборот). Лимит: 10 запросов за 2 секунды. |

Официальная документация:

- Spot introduction: https://www.mexc.io/api-docs/spot-v3/introduction
- Spot exchange info: https://www.mexc.io/api-docs/spot-v3/market-data-endpoints/exchange-information
- Spot book ticker: https://www.mexc.io/api-docs/spot-v3/market-data-endpoints/symbol-order-book-ticker
- Spot 24h statistics: https://www.mexc.io/api-docs/spot-v3/market-data-endpoints/api-24hr-ticker-price-change-statistics
- Futures introduction: https://www.mexc.fm/api-docs/futures/integration-guide
- Futures contract info: https://www.mexc.fm/api-docs/futures/market-endpoints/get-contract-info
- Futures ticker: https://www.mexc.fm/api-docs/futures/market-endpoints/get-ticker-contract-market-data
- Futures change log: https://www.mexc.fm/api-docs/futures/update-log

### Фильтрация инструментов

Для Spot принимаются только активные USDT-пары: `status=1`, разрешённая Spot-торговля и доступность как покупки, так и продажи, если поле `tradeSideType` присутствует. Значения `tradeSideType=2`, `3` и `4` исключаются.

Для Futures принимаются только бессрочные контракты с `futureType=1`, `state=0`, `quoteCoin=USDT` и `settleCoin=USDT`. При наличии `type` оставляется только обычный тип `1`; поставочные, остановленные, приостановленные и другие неподдерживаемые контракты исключаются.

Преобразование символа не строится на удалении суффикса `USDT`: базовый и котируемый активы берутся из полей API. Например, `BTCUSDT` (Spot) и `BTC_USDT` (Futures) хранятся раздельно.

### Точность, объёмы и время

- Цены и значения объёмов разбираются напрямую через `shopspring/decimal`, без промежуточного `float64`.
- Spot-объём `volume` — количество базового актива за 24 часа.
- Futures `volume24` возвращается в контрактах. Адаптер умножает его на документированный `contractSize`, чтобы привести объём к базовому активу. Если размер контракта недоступен, объём остаётся неизвестным (`NULL`), а не подменяется `amount24`.
- `amount24` — оборот и не смешивается с базовым объёмом.
- Spot book ticker не сообщает время биржевой котировки: используются локальные `Timestamp` и `ReceivedAt`, поле `ExchangeTimestamp` пустое.
- Для Futures время `timestamp` сохраняется в `Timestamp` и `ExchangeTimestamp`; `ReceivedAt` записывает локальное UTC-время получения.
- Используется существующая миграция `002_price_snapshot_timestamps.sql`; дополнительные таблицы для MEXC не создаются.

### HTTP-обработка

Клиент переиспользует HTTP-соединения, учитывает `context.Context`, ограничивает timeout и размер ответа, проверяет статус HTTP и ошибки внутри JSON. Retry ограничены; 418 не повторяется, 429 учитывает `Retry-After`, а 5xx повторяется с паузой. Если API ответило ошибкой или структура ответа некорректна, ошибка возвращается вызывающему коду.


## OKX API

Адаптер OKX использует только публичные REST-эндпоинты без ключей и без торговых запросов. Глобальный URL по умолчанию — `https://openapi.okx.com`: официальный журнал документации OKX от 20 мая 2026 года рекомендует этот домен для REST. Для зарегистрированных в отдельных регионах пользователей домен может отличаться; его можно задать параметром `exchanges.okx.base_url`, например `https://eea.okx.com` или `https://us.okx.com`, если такой адрес предписан аккаунту. Источник: https://www.okx.com/docs-v5/log_en/

| Назначение | HTTP-метод и путь | Используемые поля и ограничения |
|---|---|---|
| Spot metadata | `GET /api/v5/public/instruments?instType=SPOT` | `instType`, `instId`, `baseCcy`, `quoteCcy`, `state`. Лимит: 20 запросов за 2 секунды на тип инструмента. |
| SWAP metadata | `GET /api/v5/public/instruments?instType=SWAP` | `instType`, `instId`, `ctType`, `settleCcy`, `state`, `ctValCcy`, `uly`, `instFamily`, при наличии `baseCcy`/`quoteCcy`. |
| Spot bulk tickers | `GET /api/v5/market/tickers?instType=SPOT` | `instType`, `instId`, `bidPx`, `askPx`, `ts`, `vol24h`. Лимит: 20 запросов за 2 секунды на IP. |
| SWAP bulk tickers | `GET /api/v5/market/tickers?instType=SWAP` | `instType`, `instId`, `bidPx`, `askPx`, `ts`, `volCcy24h`. Лимит: 20 запросов за 2 секунды на IP. |

Все перечисленные endpoints публичные. Ответы имеют оболочку `code`, `msg`, `data`; `code` должен быть строкой или числом со значением `0`. HTTP 200 при ненулевом `code` считается ошибкой. Рыночные endpoints могут обслуживаться независимыми кешами, поэтому metadata и котировки не гарантируют один и тот же момент времени.

### Фильтрация рынков

- Spot: принимается только `instType=SPOT`, `quoteCcy=USDT`, `state=live`, заполненные `baseCcy` и `instId`. Идентификатор сверяется с метаданными, но актив и котируемая валюта берутся не из одной строки.
- Perpetual: принимаются только `instType=SWAP`, `ctType=linear`, `settleCcy=USDT`, `state=live`. Когда `quoteCcy` или `uly`/`instFamily` доступны, они также должны согласовываться с USDT. Базовый актив берётся из `baseCcy`, `ctValCcy` и/или полей underlying; противоречивые или неизвестные metadata пропускаются с диагностикой.
- Исключаются `FUTURES`, `OPTION`, inverse-контракты, приостановленные рынки и неподдерживаемые расчётные валюты.
- Оригинальный `instId` сохраняется. Spot `BTC-USDT` и SWAP `BTC-USDT-SWAP` остаются разными рынками и имеют разные canonical keys.

### Котировки, время и объём

- Для bid/ask используются исключительно `bidPx` и `askPx`; поле `last` никогда не подменяет лучшие цены.
- Цена и объём разбираются через `shopspring/decimal` из JSON-строк или их исходного числового представления, без промежуточного `float64`.
- `ts` преобразуется из Unix timestamp в миллисекундах в UTC. В `Timestamp` и `ExchangeTimestamp` сохраняется время OKX, а в `ReceivedAt` — локальное UTC-время получения ответа.
- `vol24h` у Spot измеряется в базовом активе. Для деривативов `vol24h` выражен в контрактах, а `volCcy24h` — в базовой валюте; именно его адаптер сохраняет в общем `Volume24h`. Источник: https://www.okx.com/docs-v5/en/
- Пустые/некорректные bulk-ответы, неизвестные символы, отсутствующие временные метки, невалидные или пересечённые цены не создают ценовые снимки.

## Конфигурация

MEXC уже объявлен в существующей структуре конфигурации. По умолчанию адаптер выключен, поэтому прежний запуск Stage 1–5 не изменяется. Чтобы включить MEXC, установи:

```yaml
exchanges:
  mexc:
    enabled: true
```

Настройки Bybit, Binance, интервал сканирования, порог спреда и путь SQLite менять не требуется. Если MEXC выключен, приложение не создаёт его HTTP-клиент и не обращается к API биржи.

## Запуск и тестирование

Запускать из корня репозитория:

```bash
go test ./...
go vet ./...
go run ./cmd/scanner
```

Unit-тесты MEXC используют `httptest` и не требуют доступа к публичной бирже. Проверка реального API может зависеть от сетевых или региональных ограничений; при ошибке MEXC адаптер возвращает её и не генерирует фиктивные котировки.

