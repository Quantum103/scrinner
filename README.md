# Crypto Arbitrage Scanner

Research-oriented cross-exchange cryptocurrency arbitrage scanner written in Go.

The application does not execute trades.

## MVP scope

Supported market types:

- Spot USDT markets
- USDT-margined perpetual futures

Supported exchanges:

- Binance
- Bybit
- MEXC
- OKX
- Gate
- Bitget

The scanner will compare:

- Spot → Spot
- Spot → Perpetual Futures
- Perpetual Futures → Spot
- Perpetual Futures → Perpetual Futures

Prices are based on:

- ASK for BUY
- BID for SELL

The gross spread formula is:

spread_percent = (sell_bid / buy_ask - 1) * 100

The project uses decimal arithmetic instead of float64 for financial values.

## Current stage

Stage 1 establishes:

- configuration;
- market domain model;
- canonical symbol representation;
- ticker model;
- exchange abstraction;
- logging;
- application entry point.

Exchange API adapters and SQLite persistence are implemented in later stages.

## Run

```bash
go mod tidy
go test ./...
go run ./cmd/scanner