package gate

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

func TestCollectTickersUsesBestBidAskDecimalBaseVolumesAndReceiveTime(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case spotTickersEndpoint:
			_, _ = io.WriteString(w, "["+
				"{\"currency_pair\":\"BTC_USDT\",\"last\":\"1\",\"highest_bid\":\"65000.123456789123\",\"lowest_ask\":\"65000.223456789123\",\"base_volume\":\"1.23456789\",\"quote_volume\":\"999999\"},"+
				"{\"currency_pair\":\"BTC3L_USDT\",\"highest_bid\":\"2\",\"lowest_ask\":\"2.1\",\"base_volume\":\"100\",\"etf_leverage\":\"3\",\"etf_net_value\":\"2.0\"},"+
				"{\"currency_pair\":\"UNKNOWN_USDT\",\"highest_bid\":\"1\",\"lowest_ask\":\"2\"}]")
		case futuresTickersEndpoint:
			_, _ = io.WriteString(w, "["+
				"{\"contract\":\"BTC_USDT\",\"last\":\"1\",\"mark_price\":\"1.5\",\"index_price\":\"1.6\",\"highest_bid\":\"64990.123456789123\",\"lowest_ask\":\"64990.223456789123\",\"volume_24h_base\":\"0.123456789\",\"volume_24h_quote\":\"999999\",\"volume_24h_settle\":\"888888\"},"+
				"{\"contract\":\"UNKNOWN_USDT\",\"highest_bid\":\"1\",\"lowest_ask\":\"2\"}]")
		default:
			t.Fatalf("неожиданный endpoint %s", r.URL.Path)
		}
	}))
	defer server.Close()
	receivedBefore := time.Now().UTC()
	adapter := New(newTestClient(t, server.URL, 0, time.Second), slog.New(slog.NewTextHandler(io.Discard, nil)))
	markets := []market.Market{
		{Exchange: "gate", Symbol: "BTC_USDT", BaseAsset: "BTC", QuoteAsset: "USDT", MarketType: market.MarketTypeSpot, ContractType: market.ContractTypeSpot, Active: true},
		{Exchange: "gate", Symbol: "BTC3L_USDT", BaseAsset: "BTC3L", QuoteAsset: "USDT", MarketType: market.MarketTypeSpot, ContractType: market.ContractTypeSpot, Active: true},
		{Exchange: "gate", Symbol: "BTC_USDT", BaseAsset: "BTC", QuoteAsset: "USDT", MarketType: market.MarketTypeFutures, ContractType: market.ContractTypePerpetual, Active: true},
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
	for _, ticker := range tickers {
		if !ticker.IsValid() || ticker.Exchange != "gate" {
			t.Fatalf("невалидный тикер: %#v", ticker)
		}
		if ticker.ExchangeTimestamp != nil {
			t.Fatalf("у Gate ticker нет биржевой временной отметки: %#v", ticker)
		}
		if ticker.ReceivedAt.Before(receivedBefore) || ticker.Timestamp.Before(receivedBefore) ||
			ticker.ReceivedAt.Location() != time.UTC || ticker.Timestamp.Location() != time.UTC {
			t.Fatalf("неверное время получения: %#v", ticker)
		}
		switch ticker.MarketType {
		case market.MarketTypeSpot:
			wantBid, _ := decimal.NewFromString("65000.123456789123")
			wantAsk, _ := decimal.NewFromString("65000.223456789123")
			wantVolume, _ := decimal.NewFromString("1.23456789")
			if !ticker.Bid.Equal(wantBid) || !ticker.Ask.Equal(wantAsk) ||
				ticker.Volume24h == nil || !ticker.Volume24h.Equal(wantVolume) {
				t.Fatalf("Spot-котировка потеряла точность или объём взят не из base_volume: %#v", ticker)
			}
		case market.MarketTypeFutures:
			wantBid, _ := decimal.NewFromString("64990.123456789123")
			wantAsk, _ := decimal.NewFromString("64990.223456789123")
			wantVolume, _ := decimal.NewFromString("0.123456789")
			if !ticker.Bid.Equal(wantBid) || !ticker.Ask.Equal(wantAsk) ||
				ticker.Volume24h == nil || !ticker.Volume24h.Equal(wantVolume) {
				t.Fatalf("Futures-котировка потеряла точность или объём взят не из volume_24h_base: %#v", ticker)
			}
		default:
			t.Fatalf("неожиданный тип рынка: %#v", ticker)
		}
	}
}

func TestBuildSpotTickersSkipsBadPricesUnknownMarketsAndETFs(t *testing.T) {
	t.Parallel()
	requested := map[string]market.Market{
		"BTC_USDT":   {Exchange: "gate", Symbol: "BTC_USDT", BaseAsset: "BTC", QuoteAsset: "USDT", MarketType: market.MarketTypeSpot, ContractType: market.ContractTypeSpot, Active: true},
		"BTC3L_USDT": {Exchange: "gate", Symbol: "BTC3L_USDT", BaseAsset: "BTC3L", QuoteAsset: "USDT", MarketType: market.MarketTypeSpot, ContractType: market.ContractTypeSpot, Active: true},
	}
	response := []spotTicker{
		{CurrencyPair: "BTC_USDT", HighestBid: []byte("\"0\""), LowestAsk: []byte("\"2\"")},
		{CurrencyPair: "BTC_USDT", HighestBid: []byte("\"-1\""), LowestAsk: []byte("\"2\"")},
		{CurrencyPair: "BTC_USDT", HighestBid: []byte("\"1\""), LowestAsk: []byte("\"bad\"")},
		{CurrencyPair: "BTC_USDT", HighestBid: []byte("\"2\""), LowestAsk: []byte("\"1\"")},
		{CurrencyPair: "BTC_USDT", HighestBid: []byte("null"), LowestAsk: []byte("\"2\"")},
		{CurrencyPair: "UNKNOWN_USDT", HighestBid: []byte("\"1\""), LowestAsk: []byte("\"2\"")},
		{CurrencyPair: "BTC3L_USDT", HighestBid: []byte("\"1\""), LowestAsk: []byte("\"2\""), ETFLeverage: []byte("\"3\"")},
	}
	tickers, stats, err := buildSpotTickers(response, requested, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(tickers) != 0 || stats.Valid != 0 || stats.Received != len(response) || stats.Skipped != len(response) {
		t.Fatalf("неверная фильтрация некорректных/ETF тикеров: tickers=%#v stats=%#v", tickers, stats)
	}
}

func TestBuildFuturesTickersSkipsInvalidPricesAndUnknownContracts(t *testing.T) {
	t.Parallel()
	requested := map[string]market.Market{
		"BTC_USDT": {Exchange: "gate", Symbol: "BTC_USDT", BaseAsset: "BTC", QuoteAsset: "USDT", MarketType: market.MarketTypeFutures, ContractType: market.ContractTypePerpetual, Active: true},
	}
	response := []futuresTicker{
		{Contract: "BTC_USDT", HighestBid: []byte("\"0\""), LowestAsk: []byte("\"2\"")},
		{Contract: "BTC_USDT", HighestBid: []byte("\"1\""), LowestAsk: []byte("\"-2\"")},
		{Contract: "BTC_USDT", HighestBid: []byte("\"1\""), LowestAsk: []byte("\"2\""), Volume24hBase: []byte("\"-1\"")},
		{Contract: "BTC_USDT", HighestBid: []byte("\"1\""), LowestAsk: []byte("\"2\""), Volume24hBase: []byte("\"bad\"")},
		{Contract: "UNKNOWN_USDT", HighestBid: []byte("\"1\""), LowestAsk: []byte("\"2\"")},
	}
	tickers, stats, err := buildFuturesTickers(response, requested, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(tickers) != 0 || stats.Valid != 0 || stats.Skipped != len(response) {
		t.Fatalf("неверная фильтрация futures-тикеров: tickers=%#v stats=%#v", tickers, stats)
	}
}

func TestFetchTickerMethodsRejectEmptyArray(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "[]")
	}))
	defer server.Close()
	adapter := New(newTestClient(t, server.URL, 0, time.Second), slog.New(slog.NewTextHandler(io.Discard, nil)))
	requested := map[string]market.Market{
		"BTC_USDT": {Exchange: "gate", Symbol: "BTC_USDT", BaseAsset: "BTC", QuoteAsset: "USDT", MarketType: market.MarketTypeSpot, ContractType: market.ContractTypeSpot, Active: true},
	}
	if _, _, err := adapter.fetchSpotTickers(context.Background(), requested); err == nil {
		t.Fatal("Spot: pustoy bulk ticker должен возвращать ошибку")
	}
	futuresRequested := map[string]market.Market{
		"BTC_USDT": {Exchange: "gate", Symbol: "BTC_USDT", BaseAsset: "BTC", QuoteAsset: "USDT", MarketType: market.MarketTypeFutures, ContractType: market.ContractTypePerpetual, Active: true},
	}
	if _, _, err := adapter.fetchFuturesTickers(context.Background(), futuresRequested); err == nil {
		t.Fatal("Futures: pustoy bulk ticker должен возвращать ошибку")
	}
}
