package statistics

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/example/crypto-arbitrage-scanner/internal/arbitrage"
	"github.com/example/crypto-arbitrage-scanner/internal/market"
	"github.com/example/crypto-arbitrage-scanner/internal/storage"
	"github.com/shopspring/decimal"
)

func openStatisticsDB(t *testing.T) *storage.DB {
	t.Helper()
	ctx := context.Background()
	db, err := storage.Open(ctx, filepath.Join(t.TempDir(), "stats.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(ctx, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	return db
}

func addObservation(t *testing.T, db *storage.DB, cycleID string, at time.Time, base, buyType, sellType, spread string) {
	t.Helper()
	ctx := context.Background()
	buyContract := market.ContractTypeSpot
	buyMarketType := market.MarketTypeSpot
	if buyType == "perpetual" {
		buyContract = market.ContractTypePerpetual
		buyMarketType = market.MarketTypeFutures
	}
	sellContract := market.ContractTypeSpot
	sellMarketType := market.MarketTypeSpot
	if sellType == "perpetual" {
		sellContract = market.ContractTypePerpetual
		sellMarketType = market.MarketTypeFutures
	}
	buySymbol := base + "USDT"
	sellSymbol := base + "_USDT"
	buy := market.Market{
		Exchange: "binance", Symbol: buySymbol, BaseAsset: base, QuoteAsset: "USDT",
		MarketType: buyMarketType, ContractType: buyContract, Active: true,
	}
	sell := market.Market{
		Exchange: "bybit", Symbol: sellSymbol, BaseAsset: base, QuoteAsset: "USDT",
		MarketType: sellMarketType, ContractType: sellContract, Active: true,
	}
	buyID, err := storage.NewMarketRepository(db).UpsertMarket(ctx, buy)
	if err != nil {
		t.Fatal(err)
	}
	sellID, err := storage.NewMarketRepository(db).UpsertMarket(ctx, sell)
	if err != nil {
		t.Fatal(err)
	}

	minimum := decimal.RequireFromString("0.5")
	if err := db.StartScanCycle(ctx, storage.ScanCycle{
		ID: cycleID, StartedAt: at, EnabledExchanges: 2, MinSpreadPercent: minimum,
	}); err != nil { t.Fatal(err) }
	if err := db.FinishScanCycle(ctx, storage.ScanCycle{
		ID: cycleID, StartedAt: at, FinishedAt: at.Add(time.Second),
		Status: "success", EnabledExchanges: 2, SucceededExchanges: 2,
		MinSpreadPercent: minimum,
	}); err != nil { t.Fatal(err) }

	buyPrice := decimal.RequireFromString("100")
	spreadValue := decimal.RequireFromString(spread)
	sellPrice := buyPrice.Mul(decimal.NewFromInt(1).Add(spreadValue.Div(decimal.NewFromInt(100))))
	op := arbitrage.Opportunity{
		CycleID: cycleID, Timestamp: at,
		Symbol: base + "/USDT", BaseAsset: base, QuoteAsset: "USDT",
		BuyMarketID: buyID, BuySymbol: buy.Symbol, BuyExchange: buy.Exchange,
		BuyMarketType: buy.MarketType, BuyContractType: buy.ContractType, BuyPrice: buyPrice,
		SellMarketID: sellID, SellSymbol: sell.Symbol, SellExchange: sell.Exchange,
		SellMarketType: sell.MarketType, SellContractType: sell.ContractType, SellPrice: sellPrice,
		SpreadPercent: spreadValue,
	}
	if _, err := storage.NewOpportunityRepository(db).InsertOpportunity(ctx, op); err != nil {
		t.Fatalf("вставить наблюдение %s: %v", spread, err)
	}
}

func TestBuildThresholdCountsEpisodesAndPeriodBoundary(t *testing.T) {
	db := openStatisticsDB(t)
	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	values := []struct {
		offset time.Duration
		spread string
	}{
		{0, "1"}, {30 * time.Second, "1.8"}, {60 * time.Second, "2"},
		{90 * time.Second, "0.7"}, {120 * time.Second, "0.99"}, {150 * time.Second, "3"},
	}
	for i, value := range values {
		addObservation(t, db, fmt.Sprintf("cycle-%d", i), start.Add(value.offset), "BTC", "spot", "spot", value.spread)
	}

	report, err := Build(context.Background(), db, Options{
		Since: start, Until: start.Add(3 * time.Minute), Timezone: "UTC", MaxObservationGap:60 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Observations != 6 || report.UniqueOpportunities != 1 {
		t.Fatalf("наблюдения/уникальные возможности = %d/%d, ожидалось 6/1", report.Observations, report.UniqueOpportunities)
	}
	if got := report.Overall.AverageSpread.String(); got != "1.58166667" {
		t.Fatalf("средний спред = %s, ожидалось 1.58166667", got)
	}
	if got := report.Overall.MaxSpread.String(); got != "3" {
		t.Fatalf("максимальный спред = %s, ожидалось 3", got)
	}
	if report.Overall.MaxSpreadAt == nil || !report.Overall.MaxSpreadAt.Equal(start.Add(150 * time.Second)) {
		t.Fatalf("неверное время максимального спреда: %#v", report.Overall.MaxSpreadAt)
	}
	thresholds := report.Overall.Thresholds
	if thresholds[0].Observations != 4 || thresholds[0].UniqueOpportunities != 1 ||
		thresholds[0].Episodes != 2 || thresholds[0].TotalObservedDurationSeconds != 60 ||
		thresholds[0].AverageEpisodeDurationSeconds != 30 || thresholds[0].MaxEpisodeDurationSeconds != 60 {
		t.Fatalf("неверная статистика порога 1%%: %#v", thresholds[0])
	}
	if thresholds[1].Observations != 2 || thresholds[1].Episodes != 2 {
		t.Fatalf("ровно 2%% должно проходить порог 2%%, статистика: %#v", thresholds[1])
	}
	if thresholds[2].Observations != 1 || thresholds[2].Episodes != 1 ||
		thresholds[2].MaxEpisodeDurationSeconds != 0 {
		t.Fatalf("ровно 3%% должно пройти порог и дать эпизод длительностью 0: %#v", thresholds[2])
	}
	if report.Cycles.Total != 6 || report.Cycles.Successful != 6 {
		t.Fatalf("неверная статистика циклов: %#v", report.Cycles)
	}
	if len(report.Routes) != 1 || report.Routes[0].BuyMarketType != "spot" || report.Routes[0].SellMarketType != "spot" {
		t.Fatalf("неверная группировка направления: %#v", report.Routes)
	}

	bounded, err := Build(context.Background(), db, Options{
		Since: start, Until: start.Add(150 * time.Second), Timezone: "UTC", MaxObservationGap:60 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if bounded.Observations != 5 {
		t.Fatalf("конец периода должен быть исключён: наблюдений %d, ожидалось 5", bounded.Observations)
	}
}

func TestBuildSeparatesAllFourMarketTypeCombinations(t *testing.T) {
	db := openStatisticsDB(t)
	start := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	combinations := []struct{ base, buy, sell string }{
		{"BTC", "spot", "spot"}, {"ETH", "spot", "perpetual"},
		{"SOL", "perpetual", "spot"}, {"ADA", "perpetual", "perpetual"},
	}
	for i, combo := range combinations {
		addObservation(t, db, fmt.Sprintf("combo-%d", i), start.Add(time.Duration(i)*time.Second),
			combo.base, combo.buy, combo.sell, "1")
	}
	report, err := Build(context.Background(), db, Options{
		Since: start, Until: start.Add(time.Hour), Timezone: "UTC", MaxObservationGap:60 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Routes) != 4 {
		t.Fatalf("ожидалось 4 независимых сочетания рынков, получено %d: %#v", len(report.Routes), report.Routes)
	}
	if len(report.Coins) != 4 {
		t.Fatalf("ожидалось 4 актива, получено %d", len(report.Coins))
	}
}

func TestEmptyPeriodAndRenderFormats(t *testing.T) {
	db := openStatisticsDB(t)
	start := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	report, err := Build(context.Background(), db, Options{
		Since: start, Until: start.Add(24 * time.Hour), Timezone: "UTC", MaxObservationGap:time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Observations != 0 || len(report.Coins) != 0 || len(report.Routes) != 0 || len(report.Hours) != 24 {
		t.Fatalf("неверный пустой отчёт: %#v", report)
	}

	var textOut bytes.Buffer
	if err := RenderText(&textOut, report, 20, "observations"); err != nil {
		t.Fatal(err)
	}
	if textOut.Len() == 0 {
		t.Fatal("текстовый отчёт пуст")
	}
	var jsonOut bytes.Buffer
	if err := RenderJSON(&jsonOut, report); err != nil {
		t.Fatal(err)
	}
	var decoded Report
	if err := json.Unmarshal(jsonOut.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	var csvOut bytes.Buffer
	if err := RenderCSV(&csvOut, report); err != nil {
		t.Fatal(err)
	}
	reader := csv.NewReader(&csvOut)
	reader.FieldsPerRecord = -1
	for {
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if len(record) != 21 {
			t.Fatalf("CSV имеет неверное число столбцов (%d): %#v", len(record), record)
		}
	}
}

func TestTimezoneChangesHourAggregation(t *testing.T) {
	db := openStatisticsDB(t)
	at := time.Date(2026, 10, 1, 23, 30, 0, 0, time.UTC)
	addObservation(t, db, "amsterdam-hour", at, "BTC", "spot", "spot", "1")
	reportUTC, err := Build(context.Background(), db, Options{
		Since: at, Until: at.Add(time.Hour), Timezone: "UTC", MaxObservationGap: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	reportLocal, err := Build(context.Background(), db, Options{
		Since: at, Until: at.Add(time.Hour), Timezone: "Europe/Amsterdam", MaxObservationGap: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if reportUTC.Hours[23].Observations != 1 || reportLocal.Hours[1].Observations != 1 {
		t.Fatalf("часовая группировка в разных зонах неверна: UTC[23]=%d Amsterdam[1]=%d",
			reportUTC.Hours[23].Observations, reportLocal.Hours[1].Observations)
	}
}

func TestStreamAndReportPreserveLegacyObservationWithoutCycle(t *testing.T) {
	db := openStatisticsDB(t)
	start := time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)
	addObservation(t, db, "known-cycle", start, "BTC", "spot", "spot", "1")
	_, err := db.SQLDB().ExecContext(context.Background(), "UPDATE opportunities SET cycle_id = NULL")
	if err != nil {
		t.Fatal(err)
	}
	report, err := Build(context.Background(), db, Options{
		Since: start, Until: start.Add(time.Hour), Timezone: "UTC", MaxObservationGap:time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.LegacyObservations != 1 {
		t.Fatalf("старое наблюдение должно остаться без цикла, значение=%d", report.LegacyObservations)
	}
	if report.Overall.Thresholds[0].CoverageComplete {
		t.Fatal("отчёт не должен объявлять полным порог при legacy-наблюдениях")
	}
}

func TestObservationIdentityIncludesDirectionAndRawSymbols(t *testing.T) {
	a := storage.HistoricalObservation{BaseAsset: "BTC", QuoteAsset: "USDT", BuyExchange: "binance", BuySymbol: "BTCUSDT", BuyMarketType: "spot", BuyContractType: "spot", SellExchange: "bybit", SellSymbol: "BTC_USDT", SellMarketType: "futures", SellContractType: "perpetual"}
	b := a
	b.BuyExchange, b.SellExchange = b.SellExchange, b.BuyExchange
	b.BuySymbol, b.SellSymbol = b.SellSymbol, b.BuySymbol
	b.BuyMarketType, b.SellMarketType = b.SellMarketType, b.BuyMarketType
	b.BuyContractType, b.SellContractType = b.SellContractType, b.BuyContractType
	if opportunityIdentity(a) == opportunityIdentity(b) {
		t.Fatal("разворот направления не должен сохранять идентичность возможности")
	}
}

// TestMaxObservationGapSeparatesEpisodes проверяет разрыв длиннее допустимого интервала.
func TestMaxObservationGapSeparatesEpisodes(t *testing.T) {
	db := openStatisticsDB(t)
	start := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	addObservation(t, db, "gap-a", start, "BTC", "spot", "spot", "1")
	addObservation(t, db, "gap-b", start.Add(90 * time.Second), "BTC", "spot", "spot", "1.5")
	report, err := Build(context.Background(), db, Options{
		Since: start, Until: start.Add(2 * time.Minute), Timezone: "UTC", MaxObservationGap: 60 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	metric := report.Overall.Thresholds[0]
	if metric.Observations != 2 || metric.Episodes != 2 || metric.TotalObservedDurationSeconds != 0 {
		t.Fatalf("разрыв больше max_observation_gap должен разделять эпизоды: %#v", metric)
	}
}
