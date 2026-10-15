package okx

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/example/crypto-arbitrage-scanner/internal/market"
)

func TestOKXLivePublicAPI(t *testing.T) {
	if os.Getenv("OKX_LIVE") != "1" {
		t.Skip("реальная проверка запускается только в отдельном live-smoke workflow")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	adapter := NewDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))
	markets, err := adapter.DiscoverMarkets(ctx)
	if err != nil {
		t.Fatalf("загрузить реальные OKX instruments: %v", err)
	}
	spotCount, swapCount := 0, 0
	for _, item := range markets {
		switch {
		case item.IsSpot() && item.QuoteAsset == "USDT":
			spotCount++
		case item.IsPerpetual() && item.QuoteAsset == "USDT":
			swapCount++
		}
	}
	if spotCount == 0 || swapCount == 0 {
		t.Fatalf("ожидались реальные Spot и линейные USDT SWAP рынки, получено Spot=%d SWAP=%d", spotCount, swapCount)
	}

	tickers, stats, err := adapter.CollectTickersWithStats(ctx, markets)
	if err != nil {
		t.Fatalf("загрузить реальные пакетные OKX tickers: %v", err)
	}
	spotTickers, swapTickers := 0, 0
	for _, ticker := range tickers {
		if !ticker.IsValid() {
			t.Errorf("адаптер вернул невалидный тикер: %#v", ticker)
			continue
		}
		switch {
		case ticker.MarketType == market.MarketTypeSpot && ticker.ContractType == market.ContractTypeSpot:
			spotTickers++
		case ticker.MarketType == market.MarketTypeFutures && ticker.ContractType == market.ContractTypePerpetual:
			swapTickers++
			spotTickers++
		case ticker.MarketType == market.MarketTypeFutures:
			swapTickers++
		}
	}
	if spotTickers == 0 || swapTickers == 0 {
		t.Fatalf("ожидались реальные Spot и SWAP tickers, получено Spot=%d SWAP=%d stats=%#v", spotTickers, swapTickers, stats)
	}
	t.Logf("реальный OKX API: рынки Spot=%d SWAP=%d; котировки получено=%d валидно=%d пропущено=%d; валидные Spot=%d SWAP=%d",
		spotCount, swapCount, stats.Received, stats.Valid, stats.Skipped, spotTickers, swapTickers)
}
