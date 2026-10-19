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
- **Gate.io** — Spot и линейные USDT Perpetual.
- **Bitget API V3** — Spot и USDT-margined Perpetual.

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

## Gate API v4

Адаптер Gate использует официальные публичные REST endpoints без API-ключей и без торговых запросов. Базовый URL по умолчанию — `https://api.gateio.ws`; полный API-префикс `/api/v4` добавляется адаптером, поэтому в YAML следует указывать домен, а не адрес с повторным `/api/v4`. Официальная документация также указывает альтернативный futures-only домен `https://fx-api.gateio.ws`, но для одного согласованного набора Spot и Futures используется основной публичный API. Источник: https://www.gate.com/docs/developers/apiv4/en/

| Назначение | HTTP-метод и путь | Поля и особенности |
|---|---|---|
| Spot metadata | `GET /api/v4/spot/currency_pairs` | `id`, `base`, `quote`, `trade_status`, `type`, `delisting_time`. Ответ — массив, авторизация не требуется. |
| Spot bulk tickers | `GET /api/v4/spot/tickers` | `currency_pair`, `highest_bid`, `lowest_ask`, `base_volume`, `quote_volume`. Если не передавать `currency_pair`, возвращаются котировки всех пар. |
| USDT Perpetual metadata | `GET /api/v4/futures/usdt/contracts` | `name`, `type`, `quanto_multiplier`, `status`, `in_delisting`. Endpoint возвращает список контрактов; API также поддерживает необязательные `limit`/`offset`, но адаптер использует документированный полный список без пагинации. |
| USDT Perpetual bulk tickers | `GET /api/v4/futures/usdt/tickers` | `contract`, `highest_bid`, `lowest_ask`, `volume_24h_base`, `volume_24h_quote`, `volume_24h_settle`. Без `contract` возвращается список всех контрактов. |

**Лимиты запросов.** Текущая документация этих четырёх публичных endpoint-страниц не показывает отдельный числовой лимит для каждой из них. Поэтому адаптер не зашивает неподтверждённые числа: за один проход выполняется не более четырёх bulk-запросов (два metadata + два ticker), таймауты и повторы ограничены, а 429 учитывает `Retry-After`, если сервер его передаёт. Для актуальных квот смотри официальные страницы endpoint и общую документацию Gate.

### Фильтрация Spot и Perpetual

- Spot включается только при `quote=USDT`, `trade_status=tradable`, `type=normal` и согласованных структурированных полях `base`/`quote` с исходным `id`. Значения `buyable`, `sellable`, `untradable`, `premarket`, неизвестный статус или тип и ненулевой `delisting_time` исключаются.
- В тикерах Spot используются только `highest_bid` и `lowest_ask`; `last` не подменяет лучшие цены. ETF-котировки пропускаются, если в bulk ticker присутствуют положительные специальные поля `etf_leverage`, `etf_net_value` или `etf_pre_net_value`.
- Perpetual включается только при `type=direct`, `status=trading`, `in_delisting=false`, положительном `quanto_multiplier` и идентификаторе контракта вида `BASE_USDT`. Inverse, делистинговые, неактивные и инструменты с неизвестными обязательными метаданными исключаются.
- Для котировок используются только `highest_bid` и `lowest_ask`; `last`, `mark_price` и `index_price` не используются для Bid/Ask.
- Spot и Perpetual могут иметь одинаковый исходный символ, например `BTC_USDT`, но сохраняются отдельными рынками по `market_type` и `contract_type`.

### Точность, объёмы и время

- Цены и объёмы разбираются через `shopspring/decimal` напрямую из строкового JSON без промежуточного `float64`.
- Общая модель `Ticker.Volume24h` в проекте используется для объёма базового актива: для Spot это `base_volume`, для фьючерсов — `volume_24h_base`. Значения `quote_volume`/`volume_24h_quote` и `volume_24h_settle` не подменяют базовый объём.
- Пакетные схемы ticker не содержат отдельной временной метки котировки. Поэтому `Timestamp` и `ReceivedAt` получают локальное UTC-время получения ответа, а `ExchangeTimestamp` остаётся `NULL`.
- Невалидные цены, пересечённый стакан, ETF Spot-котировки, неизвестные символы и пустые bulk-ответы не приводят к записи фиктивных снимков.

### HTTP-обработка

HTTP-клиент переиспользуется, закрывает тела ответов, ограничивает размер ответа и время запроса, уважает отмену context, разбирает `label`/`message` у ошибок Gate, повторяет только временные сетевые сбои, HTTP 429 и 5xx ограниченное число раз. Ошибки клиента и ошибки JSON возвращаются вызывающему коду.

## Bitget API V3

Адаптер использует только публичный REST API V3 с базовым URL `https://api.bitget.com`. Ключи и подпись не нужны; запросы выставления ордеров отсутствуют. Все четыре метода имеют лимит **20 запросов в секунду на IP** по актуальной официальной документации. Обычный проход Bitget использует два bulk-запроса инструментов и максимум два bulk-запроса котировок.

| Назначение | Метод и путь | Поля и лимит |
|---|---|---|
| Spot-инструменты | `GET /api/v3/market/instruments?category=SPOT` | `category`, `symbol`, `baseCoin`, `quoteCoin`, `status`, `symbolType`, при наличии `instrumentType`, `isRwa`, `isReality`; 20 req/s/IP. |
| USDT Futures-инструменты | `GET /api/v3/market/instruments?category=USDT-FUTURES` | Те же структурированные поля и `type` (`perpetual`/`delivery`); 20 req/s/IP. |
| Spot-котировки | `GET /api/v3/market/tickers?category=SPOT` | `symbol`, `bid1Price`, `ask1Price`, `volume24h`, `turnover24h`, `ts`; 20 req/s/IP. |
| USDT Futures-котировки | `GET /api/v3/market/tickers?category=USDT-FUTURES` | Те же поля; дополнительные `markPrice` и `indexPrice` не используются для bid/ask; 20 req/s/IP. |

Официальные источники: [Get Instruments](https://www.bitget.com/docs/catalog/market-market-data/market-instruments), [Market Data / Get Tickers](https://www.bitget.com/docs/catalog/market/market-data), [руководство по переходу на UTA](https://www.bitget.com/docs/classic/uta-api-upgrade-guide).

### Фильтрация инструментов

- Принимаются только категории `SPOT` и `USDT-FUTURES`, котируемый актив `USDT`, непустые структурированные `baseCoin`/`quoteCoin` и `status=online`. Неизвестные статусы пропускаются с диагностикой; `listed`, `limit_open`, `limit_close`, `offline` и `restrictedAPI` не считаются полноценной двусторонней доступностью.
- Для Futures требуется `type=perpetual`; `delivery` и неизвестные типы исключаются. `COIN-FUTURES`, `USDC-FUTURES`, margin и опционы не запрашиваются.
- По документированному `symbolType` разрешается только `crypto`; металлы, акции и сырьевые инструменты исключаются. Если `instrumentType` присутствует, он также проверяется как дополнительный классификатор. Положительные `isRwa=YES` и Spot `isReality=yes` исключаются; отсутствующие необязательные флаги не требуют фиктивных значений.
- Символ хранится без преобразования. Один и тот же `BTCUSDT` у Bitget сохраняется как два разных рынка — Spot и Perpetual — через существующие типы и canonical key.

### Котировки, объёмы и время

- Bid и Ask берутся только из `bid1Price` и `ask1Price`. `lastPrice`, `markPrice` и `indexPrice` не являются заменой лучшим ценам.
- `volume24h` сохраняется как объём базового актива; `turnover24h` — отдельный показатель оборота, он не подменяет `Volume24h`. Цены и объёмы разбираются через `shopspring/decimal` без промежуточного `float64`.
- `ts` — Unix-время в миллисекундах. Оно преобразуется в UTC и сохраняется в `Timestamp`/ `ExchangeTimestamp`; `ReceivedAt` отдельно отражает локальное время получения HTTP-ответа.
- Размеры `bid1Size` и `ask1Size` декодируются как отдельные поля ответа, но не записываются в общую модель: она не содержит полей размера лучших уровней.
- Клиент проверяет HTTP-статус и прикладной `code=00000`, ограничивает timeout и размер ответа, закрывает тело ответа, уважает отмену context, учитывает `Retry-After` для 429 и ограниченно повторяет сетевые ошибки и 5xx. Пустой `data` не считается успешным обновлением.

## Конфигурация

Для каждого адаптера используется существующий раздел `exchanges`. MEXC, OKX, Gate и Bitget по умолчанию выключены, чтобы сохранить прежнее поведение. Чтобы включить эти адаптеры, задай:

```yaml
exchanges:
  mexc:
    enabled: true
  okx:
    enabled: true
    # Необязательно: официальный региональный URL, если он требуется аккаунту.
    # base_url: https://eea.okx.com
    # По умолчанию: https://openapi.okx.com
  gate:
    enabled: true
    # Необязательно; по умолчанию https://api.gateio.ws
    # base_url: https://api.gateio.ws
  bitget:
    enabled: true
```

У OKX `base_url` необязателен. Все запросы публичные, API-ключи не нужны. Настройки Bybit, Binance, интервал сканирования, порог спреда и путь SQLite менять не требуется. Выключенный адаптер не создаёт HTTP-клиент и не обращается к соответствующей бирже.

## Запуск и тестирование

Запускать из корня репозитория:

```bash
go test ./...
go vet ./...
go run ./cmd/scanner
```

Unit-тесты MEXC, OKX, Gate и Bitget используют `httptest` и не требуют доступа к публичной бирже. Проверка реального API может зависеть от сетевых или региональных ограничений; при ошибке биржевой API адаптер возвращает её и не генерирует фиктивные котировки.

