package okx

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/example/crypto-arbitrage-scanner/internal/market"
	"github.com/shopspring/decimal"
)

func TestCollectTickersUsesBidAskTimestampAndBaseVolume(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("instType") == "SPOT" {
			_, _ = io.WriteString(w, "{\"code\":\"0\",\"msg\":\"\",\"data\":["+
				"{\"instType\":\"SPOT\",\"instId\":\"BTC-USDT\",\"last\":\"1\",\"bidPx\":\"65000.123456789123\",\"askPx\":\"65000.223456789123\",\"ts\":\"1792050000123\",\"vol24h\":\"1.23456789\",\"volCcy24h\":\"999999\"},"+
				"{\"instType\":\"SPOT\",\"instId\":\"ETH-USDT\",\"bidPx\":\"0\",\"askPx\":\"2\",\"ts\":\"1792050000123\"},"+
				"{\"instType\":\"SPOT\",\"instId\":\"UNKNOWN-USDT\",\"bidPx\":\"1\",\"askPx\":\"2\",\"ts\":\"1792050000123\"}]}")
			return
		}
		_, _ = io.WriteString(w, "{\"code\":\"0\",\"msg\":\"\",\"data\":["+
			"{\"instType\":\"SWAP\",\"instId\":\"BTC-USDT-SWAP\",\"last\":\"1\",\"bidPx\":\"64990.123456789123\",\"askPx\":\"64990.223456789123\",\"ts\":\"1792050000123\",\"vol24h\":\"999999\",\"volCcy24h\":\"0.123456789\"},"+
			"{\"instType\":\"SWAP\",\"instId\":\"BTC-USDC-SWAP\",\"bidPx\":\"1\",\"askPx\":\"2\",\"ts\":\"1792050000123\"}]}")
	}))
	defer server.Close()

	client := newTestClient(t, server.URL, 0, time.Second)
	adapter := New(client, slog.New(slog.NewTextHandler(io.Discard, nil)))
	markets := []market.Market{
		{Exchange: "okx", Symbol: "BTC-USDT", BaseAsset: "BTC", QuoteAsset: "USDT", MarketType: market.MarketTypeSpot, ContractType: market.ContractTypeSpot, Active: true},
		{Exchange: "okx", Symbol: "BTC-USDT-SWAP", BaseAsset: "BTC", QuoteAsset: "USDT", MarketType: market.MarketTypeFutures, ContractType: market.ContractTypePerpetual, Active: true},
		{Exchange: "binance", Symbol: "BTCUSDT", BaseAsset: "BTC", QuoteAsset: "USDT", MarketType: market.MarketTypeSpot, ContractType: market.ContractTypeSpot, Active: true},
	}
	tickers, stats, err := adapter.CollectTickersWithStats(context.Background(), markets)
	if err != nil {
		t.Fatal(err)
	}
	if len(tickers) != 2 {
		t.Fatalf("получено %d тикеров, ожидалось 2: %#v", len(tickers), tickers)
	}
	if stats.Received != 5 || stats.Valid != 2 || stats.Skipped != 3 {
		t.Fatalf("неверная статистика: %#v", stats)
	}
	wantTimestamp := int64(1792050000123)
	for _, ticker := range tickers {
		if !ticker.IsValid() || ticker.Timestamp.Location() != time.UTC {
			t.Fatalf("невалидный тикер: %#v", ticker)
		}
		if ticker.Timestamp.UnixMilli() != wantTimestamp || ticker.ExchangeTimestamp == nil ||
			ticker.ExchangeTimestamp.UnixMilli() != wantTimestamp {
			t.Fatalf("временная метка не сохранена в миллисекундах: %#v", ticker)
		}
		if ticker.ReceivedAt.IsZero() {
			t.Fatal("ReceivedAt должен содержать время получения ответа")
		}
		switch ticker.MarketType {
		case market.MarketTypeSpot:
			bid, _ := decimal.NewFromString("65000.123456789123")
			volume, _ := decimal.NewFromString("1.23456789")
			if !ticker.Bid.Equal(bid) || ticker.Volume24h == nil || !ticker.Volume24h.Equal(volume) {
				t.Fatalf("потеря точности Spot или неверная семантика объёма: %#v", ticker)
			}
		case market.MarketTypeFutures:
			bid, _ := decimal.NewFromString("64990.123456789123")
			volume, _ := decimal.NewFromString("0.123456789")
			if !ticker.Bid.Equal(bid) || ticker.Volume24h == nil || !ticker.Volume24h.Equal(volume) {
				t.Fatalf("потеря точности SWAP или вместо базового объёма использованы контракты: %#v", ticker)
			}
		default:
			t.Fatalf("неожиданный тип рынка: %#v", ticker)
		}
	}
}

func TestBuildTickersSkipsInvalidPricesTimestampAndUnknownSymbols(t *testing.T) {
	t.Parallel()
	requested := map[string]market.Market{
		"BTC-USDT": {Exchange: "okx", Symbol: "BTC-USDT", BaseAsset: "BTC", QuoteAsset: "USDT", MarketType: market.MarketTypeSpot, ContractType: market.ContractTypeSpot, Active: true},
	}
	response := []marketTicker{
		{InstType: "SPOT", InstID: "BTC-USDT", BidPx: []byte("\"0\""), AskPx: []byte("\"2\""), Timestamp: []byte("\"1792050000123\"")},
		{InstType: "SPOT", InstID: "BTC-USDT", BidPx: []byte("\"-1\""), AskPx: []byte("\"2\""), Timestamp: []byte("\"1792050000123\"")},
		{InstType: "SPOT", InstID: "BTC-USDT", BidPx: []byte("\"1\""), AskPx: []byte("\"bad\""), Timestamp: []byte("\"1792050000123\"")},
		{InstType: "SPOT", InstID: "BTC-USDT", BidPx: []byte("\"2\""), AskPx: []byte("\"1\""), Timestamp: []byte("\"1792050000123\"")},
		{InstType: "SPOT", InstID: "BTC-USDT", BidPx: []byte("\"1\""), AskPx: []byte("\"2\""), Timestamp: []byte("\"bad\"")},
		{InstType: "SWAP", InstID: "BTC-USDT", BidPx: []byte("\"1\""), AskPx: []byte("\"2\""), Timestamp: []byte("\"1792050000123\"")},
		{InstType: "SPOT", InstID: "UNKNOWN-USDT", BidPx: []byte("\"1\""), AskPx: []byte("\"2\""), Timestamp: []byte("\"1792050000123\"")},
	}
	tickers, stats, err := buildTickers(response, requested, "SPOT", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(tickers) != 0 || stats.Received != len(response) || stats.Valid != 0 || stats.Skipped != len(response) {
		t.Fatalf("некорректная обработка невалидных данных: тикеры=%#v статистика=%#v", tickers, stats)
	}
}

func TestFetchTickersRejectsEmptyBulkResponse(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "{\"code\":\"0\",\"msg\":\"\",\"data\":[]}")
	}))
	defer server.Close()
	adapter := New(newTestClient(t, server.URL, 0, time.Second), slog.New(slog.NewTextHandler(io.Discard, nil)))
	requested := map[string]market.Market{
		"BTC-USDT": {Exchange: "okx", Symbol: "BTC-USDT", BaseAsset: "BTC", QuoteAsset: "USDT", MarketType: market.MarketTypeSpot, ContractType: market.ContractTypeSpot, Active: true},
	}
	if _, _, err := adapter.fetchTickers(context.Background(), "SPOT", requested); err == nil {
		t.Fatal("пустой bulk ticker должен возвращать ошибку")
	}
}
