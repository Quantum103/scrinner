//go:build integration

package bybit

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

// TestLiveBybitAPI проверяет один реальный проход через публичный Bybit V5 API.
// Этот тест запускается только с build tag integration и требует доступ к Интернету.
func TestLiveBybitAPI(t *testing.T) {
	adapter := NewDefault(slog.Default())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	markets, err := adapter.DiscoverMarkets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(markets) == 0 {
		t.Fatal("Bybit вернул 0 поддерживаемых рынков")
	}

	tickers, err := adapter.CollectTickers(ctx, markets)
	if err != nil {
		t.Fatal(err)
	}
	if len(tickers) == 0 {
		t.Fatal("Bybit вернул 0 валидных тикеров")
	}
}
