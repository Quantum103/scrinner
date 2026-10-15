# Crypto Arbitrage Scanner

Research-oriented cross-exchange cryptocurrency market scanner written in Go. The application is read-only and does not execute trades.

## Supported market types

- Spot markets quoted in USDT.
- USDT-margined perpetual futures.

## Planned exchanges

The longer-term plan includes Binance, Bybit, MEXC, OKX, Gate and Bitget. At the current stage, only the Bybit V5 public REST adapter is implemented.

## Current stage: Stage 4 — Bybit adapter

The adapter receives structured market metadata, applies the internal Stage 3 market normalizer, fetches best bid/ask and 24-hour volume, and stores markets and price snapshots in SQLite.

Official Bybit API endpoints:

- GET /v5/market/instruments-info?category=spot — Spot instruments. Spot does not use pagination; active instruments are selected by status=Trading. Documentation: https://bybit-exchange.github.io/docs/v5/market/instrument
- GET /v5/market/instruments-info?category=linear — Linear instruments. Requests use limit=1000 and follow nextPageCursor. Only status=Trading, contractType=LinearPerpetual and quoteCoin=USDT with settleCoin=USDT are retained. Documentation: https://bybit-exchange.github.io/docs/v5/market/instrument
- GET /v5/market/tickers?category=spot — Spot tickers.
- GET /v5/market/tickers?category=linear — Linear tickers. Both categories use bid1Price for BID, ask1Price for ASK and volume24h for the reported 24-hour base volume. The response-level time field supplies the timestamp. Documentation: https://bybit-exchange.github.io/docs/v5/market/tickers
- Retry handling respects temporary HTTP/API errors and Bybit response rate-limit headers. Official rules: https://bybit-exchange.github.io/docs/v5/rate-limit

The public adapter does not require API keys, does not make trading requests, and uses decimal arithmetic for price and volume values. Unit tests use httptest and do not require live Bybit access. The separately tagged integration test requires external API access.

## Run

Run these commands from the repository root:

- go test ./...
- go vet ./...
- go run ./cmd/scanner
- go test -tags=integration ./internal/exchange/bybit/... — optional live API test; requires network access to Bybit.

The public API can return HTTP 403 if access from the current network or region is blocked. Such errors are reported; the adapter does not generate substitute market data.
