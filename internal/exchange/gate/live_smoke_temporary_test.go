package gate

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/example/crypto-arbitrage-scanner/internal/market"
)

func TestGateLivePublicAPI(t *testing.T) {
	if os.Getenv("GATE_LIVE") != "1" {
		t.Skip("реальная проверка запускается только во временном live-smoke workflow")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()

	adapter := NewDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))
	markets, err := adapter.DiscoverMarkets(ctx)
	if err != nil {
		t.Fatalf("загрузить реальные Gate markets: %v", err)
	}
	spotMarkets, perpetualMarkets := 0, 0
	for _, item := range markets {
		switch {
		case item.IsSpot() && item.QuoteAsset == "USDT":
			spotMarkets++
		case item.IsPerpetual() && item.QuoteAsset == "USDT":
			perpetualMarkets++
		}
	}
	if spotMarkets == 0 || perpetualMarkets == 0 {
		t.Fatalf("ожидались активные Spot и USDT Perpetual рынки, получено Spot=%d Perpetual=%d", spotMarkets, perpetualMarkets)
	}

	tickers, stats, err := adapter.CollectTickersWithStats(ctx, markets)
	if err != nil {
		t.Fatalf("загрузить реальные Gate bulk tickers: %v", err)
	}
	spotTickers, perpetualTickers := 0, 0
	for _, ticker := range tickers {
		if !ticker.IsValid() {
			t.Errorf("адаптер вернул невалидный тикер: %#v", ticker)
			continue
		}
		switch {
		case ticker.Exchange == "gate" && ticker.MarketType == market.MarketTypeSpot && ticker.ContractType == market.ContractTypeSpot:
			spotTickers++
		case ticker.Exchange == "gate" && ticker.MarketType == market.MarketTypeFutures && ticker.ContractType == market.ContractTypePerpetual:
			perpetualTickers++
		}
	}
	if spotTickers == 0 || perpetualTickers == 0 {
		t.Fatalf("ожидались валидные Spot и Perpetual котировки, получено Spot=%d Perpetual=%d статистика=%#v", spotTickers, perpetualTickers, stats)
	}
	t.Logf("реальный Gate API: рынки Spot=%d Perpetual=%d; котировки получено=%d валидно=%d пропущено=%d; валидные Spot=%d Perpetual=%d",
		spotMarkets, perpetualMarkets, stats.Received, stats.Valid, stats.Skipped, spotTickers, perpetualTickers)
}
