// Package statistics строит отчёты по историческим наблюдениям сканера.
package statistics

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/example/crypto-arbitrage-scanner/internal/storage"
	"github.com/shopspring/decimal"
)

var spreadThresholds = []decimal.Decimal{
	decimal.NewFromInt(1),
	decimal.NewFromInt(2),
	decimal.NewFromInt(3),
}

// Options задаёт границы периода и параметры анализа истории.
type Options struct {
	Since             time.Time
	Until             time.Time
	Timezone          string
	MaxObservationGap time.Duration
}

// ThresholdMetrics содержит статистику наблюдений и эпизодов выше порога.
type ThresholdMetrics struct {
	ThresholdPercent              string          `json:"threshold_percent"`
	Observations                  int64           `json:"observations"`
	UniqueOpportunities           int64           `json:"unique_opportunities"`
	Episodes                      int64           `json:"episodes"`
	AverageSpreadPercent          decimal.Decimal `json:"average_spread_percent"`
	MaxSpreadPercent              decimal.Decimal `json:"max_spread_percent"`
	FirstObserved                 *time.Time      `json:"first_observed,omitempty"`
	LastObserved                  *time.Time      `json:"last_observed,omitempty"`
	TotalObservedDurationSeconds  int64           `json:"total_observed_duration_seconds"`
	AverageEpisodeDurationSeconds int64           `json:"average_episode_duration_seconds"`
	MedianEpisodeDurationSeconds  int64           `json:"median_episode_duration_seconds"`
	MaxEpisodeDurationSeconds     int64           `json:"max_episode_duration_seconds"`
	CoverageComplete              bool            `json:"coverage_complete_within_saved_signal_history"`
}

// GroupSummary содержит сводные метрики для общего набора, монеты или направления.
type GroupSummary struct {
	Observations        int64              `json:"observations"`
	UniqueOpportunities int64              `json:"unique_opportunities"`
	AverageSpread       decimal.Decimal    `json:"average_spread_percent"`
	MaxSpread           decimal.Decimal    `json:"max_spread_percent"`
	MaxSpreadAt         *time.Time         `json:"max_spread_at,omitempty"`
	MaxSpreadPair       string             `json:"max_spread_pair,omitempty"`
	Thresholds          []ThresholdMetrics `json:"thresholds"`
}

// CoinSummary содержит статистику наблюдений одной базовой валюты.
type CoinSummary struct {
	BaseAsset string `json:"base_asset"`
	GroupSummary
}

// RouteSummary содержит статистику одного направленного сочетания бирж и типов рынков.
type RouteSummary struct {
	BuyExchange      string `json:"buy_exchange"`
	SellExchange     string `json:"sell_exchange"`
	BuyMarketType    string `json:"buy_market_type"`
	BuyContractType  string `json:"buy_contract_type"`
	SellMarketType   string `json:"sell_market_type"`
	SellContractType string `json:"sell_contract_type"`
	GroupSummary
}

// HourSummary показывает распределение наблюдений по часу в выбранной временной зоне.
type HourSummary struct {
	Hour          int             `json:"hour"`
	Observations  int64           `json:"observations"`
	AverageSpread decimal.Decimal `json:"average_spread_percent"`
	MaxSpread     decimal.Decimal `json:"max_spread_percent"`
	Thresholds    []HourThreshold `json:"thresholds"`
}

// HourThreshold содержит счётчики порога для часового интервала.
type HourThreshold struct {
	ThresholdPercent string `json:"threshold_percent"`
	Observations     int64  `json:"observations"`
	EpisodesStarted  int64  `json:"episodes_started"`
}

// CycleSummary содержит статистику зарегистрированных после Stage 12 циклов.
type CycleSummary struct {
	Total            int64 `json:"total"`
	Successful       int64 `json:"successful"`
	Partial          int64 `json:"partial"`
	NoData           int64 `json:"no_data"`
	CollectionFailed int64 `json:"collection_failed"`
	SaveFailed       int64 `json:"save_failed"`
	Cancelled        int64 `json:"cancelled"`
	StillRunning     int64 `json:"still_running"`
}

// Report представляет полный исторический отчёт.
type Report struct {
	GeneratedAt                time.Time      `json:"generated_at"`
	Since                      *time.Time     `json:"since,omitempty"`
	Until                      *time.Time     `json:"until,omitempty"`
	Timezone                   string         `json:"timezone"`
	MaxObservationGapSeconds   int64          `json:"max_observation_gap_seconds"`
	ObservationRowsRead        int64          `json:"observation_rows_read"`
	Observations               int64          `json:"observations"`
	InvalidObservationsSkipped int64          `json:"invalid_observations_skipped"`
	LegacyObservations         int64          `json:"observations_without_cycle_id"`
	UniqueOpportunities        int64          `json:"unique_opportunities"`
	Cycles                     CycleSummary   `json:"cycles"`
	Overall                    GroupSummary   `json:"overall"`
	Coins                      []CoinSummary  `json:"coins"`
	Routes                     []RouteSummary `json:"routes"`
	Hours                      []HourSummary  `json:"hours"`
	Warnings                   []string       `json:"warnings"`
}

type accumulator struct {
	count      int64
	sum        decimal.Decimal
	max        decimal.Decimal
	maxAt      *time.Time
	maxPair    string
	seen       map[string]struct{}
	thresholds [3]*thresholdAccumulator
	identity   string
	gap        time.Duration
	location   *time.Location
	onEpisode  func(int, time.Time)
}

type thresholdAccumulator struct {
	count     int64
	sum       decimal.Decimal
	max       decimal.Decimal
	first     *time.Time
	last      *time.Time
	seen      map[string]struct{}
	episodes  int64
	total     time.Duration
	durations []time.Duration
	active    bool
	start     time.Time
	lastAbove time.Time
	lastSeen  time.Time
}

type hourAccumulator struct {
	count           int64
	sum             decimal.Decimal
	max             decimal.Decimal
	thresholdCounts [3]int64
	episodesStarted [3]int64
}

type routeKey struct {
	buyExchange      string
	sellExchange     string
	buyMarketType    string
	buyContractType  string
	sellMarketType   string
	sellContractType string
}

type builder struct {
	report                Report
	location              *time.Location
	gap                   time.Duration
	overall               *accumulator
	coins                 map[string]*accumulator
	routes                map[routeKey]*accumulator
	hours                 [24]*hourAccumulator
	unique                map[string]struct{}
	coverageLost          [3]bool
	cycleMinUnknown       bool
	cyclesWithHighMinimum [3]int64
}

func Build(ctx context.Context, db *storage.DB, options Options) (Report, error) {
	if db == nil {
		return Report{}, fmt.Errorf("SQLite не открыт")
	}
	if !options.Since.IsZero() && !options.Until.IsZero() && !options.Since.Before(options.Until) {
		return Report{}, fmt.Errorf("начало периода должно быть раньше конца")
	}
	if options.Timezone == "" {
		options.Timezone = "UTC"
	}
	location, err := time.LoadLocation(options.Timezone)
	if err != nil {
		return Report{}, fmt.Errorf("неизвестная временная зона %q: %w", options.Timezone, err)
	}
	if options.MaxObservationGap <= 0 {
		options.MaxObservationGap = 60 * time.Second
	}

	b := &builder{
		location: location,
		gap:      options.MaxObservationGap,
		coins:    make(map[string]*accumulator),
		routes:   make(map[routeKey]*accumulator),
		unique:   make(map[string]struct{}),
	}
	for hour := range b.hours {
		b.hours[hour] = &hourAccumulator{}
	}
	b.overall = newAccumulator(b.gap, location, func(threshold int, start time.Time) {
		b.hours[start.In(location).Hour()].episodesStarted[threshold]++
	})

	b.report = Report{
		GeneratedAt:              time.Now().UTC(),
		Timezone:                 options.Timezone,
		MaxObservationGapSeconds: int64(options.MaxObservationGap.Seconds()),
		Warnings:                 []string{},
	}
	if !options.Since.IsZero() {
		value := options.Since.UTC()
		b.report.Since = &value
	}
	if !options.Until.IsZero() {
		value := options.Until.UTC()
		b.report.Until = &value
	}

	cycleErr := db.StreamScanCycles(ctx, options.Since, options.Until, func(cycle storage.ScanCycle) error {
		b.report.Cycles.Total++
		switch cycle.Status {
		case "success":
			b.report.Cycles.Successful++
		case "partial":
			b.report.Cycles.Partial++
		case "no_data":
			b.report.Cycles.NoData++
		case "collection_failed":
			b.report.Cycles.CollectionFailed++
		case "save_failed":
			b.report.Cycles.SaveFailed++
		case "cancelled":
			b.report.Cycles.Cancelled++
		case "running":
			b.report.Cycles.StillRunning++
		}
		if !cycle.MinSpreadKnown {
			b.cycleMinUnknown = true
		}
		for i, threshold := range spreadThresholds {
			if cycle.MinSpreadKnown && cycle.MinSpreadPercent.GreaterThan(threshold) {
				b.cyclesWithHighMinimum[i]++
			}
		}
		return nil
	})
	if cycleErr != nil {
		return Report{}, cycleErr
	}

	repo := storage.NewOpportunityRepository(db)
	totalRows, skipped, err := repo.StreamHistoricalObservations(ctx, options.Since, options.Until, func(item storage.HistoricalObservation) error {
		b.report.Observations++
		if item.CycleID == "" {
			b.report.LegacyObservations++
		}
		identity := opportunityIdentity(item)
		route := routeKey{
			buyExchange: item.BuyExchange, sellExchange: item.SellExchange,
			buyMarketType: item.BuyMarketType, buyContractType: item.BuyContractType,
			sellMarketType: item.SellMarketType, sellContractType: item.SellContractType,
		}
		pair := fmt.Sprintf("%s/%s → %s/%s", item.BuyExchange, item.BuySymbol, item.SellExchange, item.SellSymbol)
		b.overall.add(identity, item.Timestamp, item.SpreadPercent, pair)
		coinKey := strings.ToUpper(item.BaseAsset)
		if b.coins[coinKey] == nil {
			b.coins[coinKey] = newAccumulator(b.gap, location, nil)
		}
		b.coins[coinKey].add(identity, item.Timestamp, item.SpreadPercent, pair)
		if b.routes[route] == nil {
			b.routes[route] = newAccumulator(b.gap, location, nil)
		}
		b.routes[route].add(identity, item.Timestamp, item.SpreadPercent, pair)

		hour := item.Timestamp.In(location).Hour()
		h := b.hours[hour]
		h.count++
		h.sum = h.sum.Add(item.SpreadPercent)
		if h.count == 1 || item.SpreadPercent.GreaterThan(h.max) {
			h.max = item.SpreadPercent
		}
		for i, threshold := range spreadThresholds {
			if item.SpreadPercent.GreaterThanOrEqual(threshold) {
				h.thresholdCounts[i]++
			}
		}
		b.unique[identity] = struct{}{}
		return nil
	})
	if err != nil {
		return Report{}, err
	}
	b.report.ObservationRowsRead = totalRows
	b.report.InvalidObservationsSkipped = skipped
	b.report.UniqueOpportunities = int64(len(b.unique))
	b.overall.finishAll()
	for _, bucket := range b.coins {
		bucket.finishAll()
	}
	for _, bucket := range b.routes {
		bucket.finishAll()
	}

	b.report.Overall = b.overall.summary()
	coinKeys := make([]string, 0, len(b.coins))
	for key := range b.coins {
		coinKeys = append(coinKeys, key)
	}
	sort.Strings(coinKeys)
	for _, key := range coinKeys {
		b.report.Coins = append(b.report.Coins, CoinSummary{BaseAsset: key, GroupSummary: b.coins[key].summary()})
	}
	routeKeys := make([]routeKey, 0, len(b.routes))
	for key := range b.routes {
		routeKeys = append(routeKeys, key)
	}
	sort.Slice(routeKeys, func(i, j int) bool { return routeString(routeKeys[i]) < routeString(routeKeys[j]) })
	for _, key := range routeKeys {
		b.report.Routes = append(b.report.Routes, RouteSummary{
			BuyExchange: key.buyExchange, SellExchange: key.sellExchange,
			BuyMarketType: key.buyMarketType, BuyContractType: key.buyContractType,
			SellMarketType: key.sellMarketType, SellContractType: key.sellContractType,
			GroupSummary: b.routes[key].summary(),
		})
	}
	for hour, values := range b.hours {
		hourReport := HourSummary{
			Hour: hour, Observations: values.count,
			AverageSpread: average(values.sum, values.count), MaxSpread: values.max,
		}
		for i, threshold := range spreadThresholds {
			hourReport.Thresholds = append(hourReport.Thresholds, HourThreshold{
				ThresholdPercent: threshold.String(),
				Observations:     values.thresholdCounts[i],
				EpisodesStarted:  values.episodesStarted[i],
			})
		}
		b.report.Hours = append(b.report.Hours, hourReport)
	}

	for i, metric := range b.report.Overall.Thresholds {
		complete := b.report.Cycles.Total > 0 && b.report.Observations > 0 &&
			b.report.InvalidObservationsSkipped == 0 && b.report.LegacyObservations == 0 &&
			!b.cycleMinUnknown && b.cyclesWithHighMinimum[i] == 0
		b.report.Overall.Thresholds[i].CoverageComplete = complete
		for coinIndex := range b.report.Coins {
			b.report.Coins[coinIndex].Thresholds[i].CoverageComplete = complete
		}
		for routeIndex := range b.report.Routes {
			b.report.Routes[routeIndex].Thresholds[i].CoverageComplete = complete
		}
		if !complete {
			b.report.Warnings = append(b.report.Warnings, fmt.Sprintf(
				"Порог %s%%: история неполна — часть старых наблюдений не привязана к циклам или минимум обнаружения был выше этого порога.",
				metric.ThresholdPercent))
		}
	}
	if b.report.LegacyObservations > 0 {
		b.report.Warnings = append(b.report.Warnings,
			"Часть наблюдений создана до журналирования циклов Stage 12: cycle_id не заполнен, прежние периоды простоя и порог обнаружения восстановить нельзя.")
	}
	if b.report.Cycles.Partial+b.report.Cycles.NoData+b.report.Cycles.CollectionFailed+b.report.Cycles.SaveFailed+b.report.Cycles.Cancelled+b.report.Cycles.StillRunning > 0 {
		b.report.Warnings = append(b.report.Warnings,
			"За выбранный период были частичные, неудачные или незавершённые циклы; отсутствие наблюдения не доказывает отсутствие возможности.")
	}
	if b.report.Observations > 0 {
		b.report.Warnings = append(b.report.Warnings,
			"Статистика отражает только сохранённые сигналы детектора выше действовавшего порога, а не все сочетания рынков и не чистую прибыль.")
		b.report.Warnings = append(b.report.Warnings,
			"Эпизоды рассчитаны по положительным наблюдениям: отрицательные состояния рынка не сохраняются, поэтому фактический момент исчезновения спреда между циклами неизвестен; эпизодная статистика имеет ограниченную полноту.")
		b.report.Warnings = append(b.report.Warnings,
			"Медиана спреда не рассчитывается: для точного decimal-ранжирования без загрузки всей истории в память текущая SQLite-схема не предоставляет подходящего упорядоченного числового поля.")
	}
	if b.report.InvalidObservationsSkipped > 0 {
		b.report.Warnings = append(b.report.Warnings, fmt.Sprintf(
			"Пропущено некорректных исторических строк: %d; статистика по этому периоду может быть неполной.",
			b.report.InvalidObservationsSkipped))
	}
	if b.report.Observations == 0 {
		b.report.Warnings = append(b.report.Warnings, "В выбранном периоде нет пригодных исторических наблюдений.")
	}
	return b.report, nil
}

func newAccumulator(gap time.Duration, location *time.Location, onEpisode func(int, time.Time)) *accumulator {
	a := &accumulator{gap: gap, location: location, onEpisode: onEpisode, seen: make(map[string]struct{})}
	for i := range a.thresholds {
		a.thresholds[i] = &thresholdAccumulator{seen: make(map[string]struct{})}
	}
	return a
}

func (a *accumulator) add(identity string, timestamp time.Time, spread decimal.Decimal, pair string) {
	if a.identity != identity {
		a.finishAll()
		a.identity = identity
	}
	a.count++
	a.sum = a.sum.Add(spread)
	a.seen[identity] = struct{}{}
	if a.count == 1 || spread.GreaterThan(a.max) {
		a.max = spread
		value := timestamp
		a.maxAt = &value
		a.maxPair = pair
	}
	for i, threshold := range spreadThresholds {
		state := a.thresholds[i]
		if state.active {
			gap := timestamp.Sub(state.lastSeen)
			if gap < 0 || gap > a.gap {
				a.finishEpisode(i)
			}
		}
		if spread.GreaterThanOrEqual(threshold) {
			state = a.thresholds[i]
			state.count++
			state.sum = state.sum.Add(spread)
			state.seen[identity] = struct{}{}
			if state.first == nil || timestamp.Before(*state.first) {
				value := timestamp
				state.first = &value
			}
			if state.last == nil || timestamp.After(*state.last) {
				value := timestamp
				state.last = &value
			}
			if !state.active {
				state.active = true
				state.start = timestamp
				state.lastAbove = timestamp
				if a.onEpisode != nil {
					a.onEpisode(i, timestamp)
				}
			} else {
				state.lastAbove = timestamp
			}
			state.lastSeen = timestamp
			if state.count == 1 || spread.GreaterThan(state.max) {
				state.max = spread
			}
		} else if state.active {
			a.finishEpisode(i)
		}
	}
}

func (a *accumulator) finishEpisode(index int) {
	state := a.thresholds[index]
	if !state.active {
		return
	}
	duration := state.lastAbove.Sub(state.start)
	if duration < 0 {
		duration = 0
	}
	state.episodes++
	state.total += duration
	state.durations = append(state.durations, duration)
	state.active = false
	state.start = time.Time{}
	state.lastAbove = time.Time{}
	state.lastSeen = time.Time{}
}

func (a *accumulator) finishAll() {
	for i := range a.thresholds {
		a.finishEpisode(i)
	}
}

func (a *accumulator) summary() GroupSummary {
	result := GroupSummary{
		Observations:        a.count,
		UniqueOpportunities: int64(len(a.seen)),
		AverageSpread:       average(a.sum, a.count),
		MaxSpread:           a.max,
		MaxSpreadAt:         a.maxAt,
		MaxSpreadPair:       a.maxPair,
	}
	for i, state := range a.thresholds {
		durationValues := make([]int64, len(state.durations))
		for j, duration := range state.durations {
			durationValues[j] = int64(duration / time.Second)
		}
		sort.Slice(durationValues, func(i, j int) bool { return durationValues[i] < durationValues[j] })
		median := int64(0)
		if len(durationValues) > 0 {
			middle := len(durationValues) / 2
			if len(durationValues)%2 == 1 {
				median = durationValues[middle]
			} else {
				median = (durationValues[middle-1] + durationValues[middle]) / 2
			}
		}
		metric := ThresholdMetrics{
			ThresholdPercent:             spreadThresholds[i].String(),
			Observations:                 state.count,
			UniqueOpportunities:          int64(len(state.seen)),
			Episodes:                     state.episodes,
			AverageSpreadPercent:         average(state.sum, state.count),
			MaxSpreadPercent:             state.max,
			TotalObservedDurationSeconds: int64(state.total / time.Second),
			MaxEpisodeDurationSeconds:    maxDurationSeconds(state.durations),
			MedianEpisodeDurationSeconds: median,
			CoverageComplete:             true,
		}
		if state.episodes > 0 {
			metric.AverageEpisodeDurationSeconds = int64((state.total / time.Duration(state.episodes)) / time.Second)
		}
		if state.first != nil {
			value := *state.first
			metric.FirstObserved = &value
		}
		if state.last != nil {
			value := *state.last
			metric.LastObserved = &value
		}
		result.Thresholds = append(result.Thresholds, metric)
	}
	return result
}

func average(sum decimal.Decimal, count int64) decimal.Decimal {
	if count == 0 {
		return decimal.Zero
	}
	return sum.DivRound(decimal.NewFromInt(count), 8)
}

func maxDurationSeconds(values []time.Duration) int64 {
	var max time.Duration
	for _, value := range values {
		if value > max {
			max = value
		}
	}
	return int64(max / time.Second)
}

func opportunityIdentity(item storage.HistoricalObservation) string {
	return strings.Join([]string{
		strings.ToUpper(item.BaseAsset), strings.ToUpper(item.QuoteAsset),
		strings.ToLower(item.BuyExchange), strings.ToUpper(item.BuySymbol),
		strings.ToLower(item.BuyMarketType), strings.ToLower(item.BuyContractType),
		strings.ToLower(item.SellExchange), strings.ToUpper(item.SellSymbol),
		strings.ToLower(item.SellMarketType), strings.ToLower(item.SellContractType),
	}, "|")
}

func routeString(key routeKey) string {
	return strings.Join([]string{key.buyExchange, key.buyMarketType, key.buyContractType,
		key.sellExchange, key.sellMarketType, key.sellContractType}, " → ")
}

// SortCoins сортирует сводку монет по observations, episodes, avg-spread, max-spread или duration.
func SortCoins(items []CoinSummary, sortBy string) {
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]
		switch sortBy {
		case "episodes":
			return thresholdNumber(a.GroupSummary, 0, func(t ThresholdMetrics) int64 { return t.Episodes }) >
				thresholdNumber(b.GroupSummary, 0, func(t ThresholdMetrics) int64 { return t.Episodes })
		case "avg-spread":
			return a.AverageSpread.GreaterThan(b.AverageSpread)
		case "max-spread":
			return a.MaxSpread.GreaterThan(b.MaxSpread)
		case "duration":
			return thresholdNumber(a.GroupSummary, 0, func(t ThresholdMetrics) int64 { return t.TotalObservedDurationSeconds }) >
				thresholdNumber(b.GroupSummary, 0, func(t ThresholdMetrics) int64 { return t.TotalObservedDurationSeconds })
		default:
			return a.Observations > b.Observations
		}
	})
}

// SortRoutes сортирует направления по выбранной метрике.
func SortRoutes(items []RouteSummary, sortBy string) {
	sort.Slice(items, func(i, j int) bool {
		a, b := items[i], items[j]
		switch sortBy {
		case "episodes":
			return thresholdNumber(a.GroupSummary, 0, func(t ThresholdMetrics) int64 { return t.Episodes }) >
				thresholdNumber(b.GroupSummary, 0, func(t ThresholdMetrics) int64 { return t.Episodes })
		case "avg-spread":
			return a.AverageSpread.GreaterThan(b.AverageSpread)
		case "max-spread":
			return a.MaxSpread.GreaterThan(b.MaxSpread)
		case "duration":
			return thresholdNumber(a.GroupSummary, 0, func(t ThresholdMetrics) int64 { return t.TotalObservedDurationSeconds }) >
				thresholdNumber(b.GroupSummary, 0, func(t ThresholdMetrics) int64 { return t.TotalObservedDurationSeconds })
		default:
			return a.Observations > b.Observations
		}
	})
}

func thresholdNumber(summary GroupSummary, index int, read func(ThresholdMetrics) int64) int64 {
	if len(summary.Thresholds) <= index {
		return 0
	}
	return read(summary.Thresholds[index])
}
