package statistics

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
)

// RenderJSON записывает полный отчёт в JSON без преобразования decimal в float64.
func RenderJSON(w io.Writer, report Report) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		return fmt.Errorf("сформировать JSON-отчёт: %w", err)
	}
	return nil
}

// RenderCSV экспортирует полный отчёт в плоские секции для табличных редакторов.
func RenderCSV(w io.Writer, report Report) error {
	location, err := time.LoadLocation(report.Timezone)
	if err != nil {
		return fmt.Errorf("неизвестная временная зона %q: %w", report.Timezone, err)
	}
	writer := csv.NewWriter(w)
	header := []string{
		"section", "key", "period_since", "period_until", "timezone",
		"observations", "unique_opportunities", "threshold_percent",
		"threshold_observations", "threshold_unique_opportunities", "episodes",
		"average_spread_percent", "max_spread_percent", "max_spread_at",
		"first_observed", "last_observed", "total_observed_duration_seconds",
		"average_episode_duration_seconds", "median_episode_duration_seconds",
		"max_episode_duration_seconds", "coverage_complete",
	}
	if err := writer.Write(header); err != nil {
		return err
	}
	since, until := reportPeriod(report)
	write := func(row []string) error { return writer.Write(row) }
	base := []string{"", "", since, until, report.Timezone}
	if err := write(append(append([]string(nil), base...), []string{
		strconv.FormatInt(report.Observations, 10),
		strconv.FormatInt(report.UniqueOpportunities, 10), "", "", "", "",
		report.Overall.AverageSpread.String(), report.Overall.MaxSpread.String(),
		formatTime(report.Overall.MaxSpreadAt, location),
		"", "", "", "", "", "", "",
	}...)); err != nil {
		return err
	}
	for _, metric := range report.Overall.Thresholds {
		if err := write(append(append([]string{"overall_threshold", metric.ThresholdPercent, since, until, report.Timezone}, []string{
			strconv.FormatInt(report.Observations, 10),
			strconv.FormatInt(report.UniqueOpportunities, 10),
			metric.ThresholdPercent,
			strconv.FormatInt(metric.Observations, 10),
			strconv.FormatInt(metric.UniqueOpportunities, 10),
			strconv.FormatInt(metric.Episodes, 10),
			metric.AverageSpreadPercent.String(),
			metric.MaxSpreadPercent.String(),
			"",
			formatTime(metric.FirstObserved, location),
			formatTime(metric.LastObserved, location),
			strconv.FormatInt(metric.TotalObservedDurationSeconds, 10),
			strconv.FormatInt(metric.AverageEpisodeDurationSeconds, 10),
			strconv.FormatInt(metric.MedianEpisodeDurationSeconds, 10),
			strconv.FormatInt(metric.MaxEpisodeDurationSeconds, 10),
			strconv.FormatBool(metric.CoverageComplete),
		}...)); err != nil {
			return err
		}
	}
	writeGroup := func(section, key string, summary GroupSummary) error {
		for _, metric := range summary.Thresholds {
			row := []string{
				section, key, since, until, report.Timezone,
				strconv.FormatInt(summary.Observations, 10),
				strconv.FormatInt(summary.UniqueOpportunities, 10),
				metric.ThresholdPercent,
				strconv.FormatInt(metric.Observations, 10),
				strconv.FormatInt(metric.UniqueOpportunities, 10),
				strconv.FormatInt(metric.Episodes, 10),
				metric.AverageSpreadPercent.String(), metric.MaxSpreadPercent.String(),
				formatTime(summary.MaxSpreadAt, location),
				formatTime(metric.FirstObserved, location), formatTime(metric.LastObserved, location),
				strconv.FormatInt(metric.TotalObservedDurationSeconds, 10),
				strconv.FormatInt(metric.AverageEpisodeDurationSeconds, 10),
				strconv.FormatInt(metric.MedianEpisodeDurationSeconds, 10),
				strconv.FormatInt(metric.MaxEpisodeDurationSeconds, 10),
				strconv.FormatBool(metric.CoverageComplete),
			}
			if err := write(row); err != nil {
				return err
			}
		}
		return nil
	}
	for _, coin := range report.Coins {
		if err := writeGroup("coin", coin.BaseAsset, coin.GroupSummary); err != nil {
			return err
		}
	}
	for _, route := range report.Routes {
		key := fmt.Sprintf("%s/%s %s→%s %s/%s",
			route.BuyExchange, route.BuyMarketType, route.BuyContractType,
			route.SellExchange, route.SellMarketType, route.SellContractType)
		if err := writeGroup("route", key, route.GroupSummary); err != nil {
			return err
		}
	}
	for _, hour := range report.Hours {
		for _, threshold := range hour.Thresholds {
			row := []string{
				"hour", fmt.Sprintf("%02d:00", hour.Hour), since, until, report.Timezone,
				strconv.FormatInt(hour.Observations, 10), "", threshold.ThresholdPercent,
				strconv.FormatInt(threshold.Observations, 10), "",
				strconv.FormatInt(threshold.EpisodesStarted, 10),
				hour.AverageSpread.String(), hour.MaxSpread.String(), "", "", "", "", "", "", "", "",
			}
			if err := write(row); err != nil {
				return err
			}
		}
	}
	for _, warning := range report.Warnings {
		row := []string{"warning", warning, since, until, report.Timezone}
		for len(row) < len(header) {
			row = append(row, "")
		}
		if err := write(row); err != nil {
			return err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return fmt.Errorf("сформировать CSV-отчёт: %w", err)
	}
	return nil
}

// RenderText выводит краткий отчёт и ограниченные таблицы рейтингов.
func RenderText(w io.Writer, report Report, limit int, sortBy string) error {
	if limit <= 0 {
		limit = 20
	}
	location, err := time.LoadLocation(report.Timezone)
	if err != nil {
		return err
	}
	writer := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	since, until := reportPeriod(report)
	fmt.Fprintf(writer, "ИСТОРИЧЕСКАЯ СТАТИСТИКА АРБИТРАЖНЫХ НАБЛЮДЕНИЙ\n")
	fmt.Fprintf(writer, "Период: %s — %s\n", since, until)
	fmt.Fprintf(writer, "Временная зона: %s | Сформирован: %s\n", report.Timezone, report.GeneratedAt.In(location).Format(time.RFC3339))
	fmt.Fprintf(writer, "Строк прочитано: %d | Пригодных наблюдений: %d | Пропущено некорректных: %d\n",
		report.ObservationRowsRead, report.Observations, report.InvalidObservationsSkipped)
	fmt.Fprintf(writer, "Уникальных возможностей: %d | Наблюдения без cycle_id: %d\n",
		report.UniqueOpportunities, report.LegacyObservations)
	fmt.Fprintf(writer, "Циклы Stage 12+: всего %d, успешно %d, частично %d, без данных %d, ошибки сбора %d, ошибки сохранения %d, отменено %d, незавершено %d\n",
		report.Cycles.Total, report.Cycles.Successful, report.Cycles.Partial, report.Cycles.NoData,
		report.Cycles.CollectionFailed, report.Cycles.SaveFailed, report.Cycles.Cancelled, report.Cycles.StillRunning)
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "СВОДКА ПО ПОРОГАМ")
	fmt.Fprintln(writer, "Порог\tНаблюдения\tУникальных\tЭпизоды\tСредний спред\tМаксимум\tСуммарная длительность, сек\tСредняя длительность, сек\tМаксимальная длительность, сек\tПолнота")
	for _, metric := range report.Overall.Thresholds {
		fmt.Fprintf(writer, "%s%%\t%d\t%d\t%d\t%s%%\t%s%%\t%d\t%d\t%d\t%t\n",
			metric.ThresholdPercent, metric.Observations, metric.UniqueOpportunities, metric.Episodes,
			metric.AverageSpreadPercent.StringFixed(4), metric.MaxSpreadPercent.StringFixed(4),
			metric.TotalObservedDurationSeconds, metric.AverageEpisodeDurationSeconds,
			metric.MaxEpisodeDurationSeconds, metric.CoverageComplete)
	}
	fmt.Fprintf(writer, "Общий средний спред: %s%% | Максимум: %s%% | Максимум наблюдался: %s\n",
		report.Overall.AverageSpread.StringFixed(4), report.Overall.MaxSpread.StringFixed(4),
		formatTime(report.Overall.MaxSpreadAt, location))
	fmt.Fprintln(writer)
	coins := append([]CoinSummary(nil), report.Coins...)
	SortCoins(coins, sortBy)
	fmt.Fprintf(writer, "ТОП МОНЕТ (сортировка: %s, строк: %d)\n", sortLabel(sortBy), minInt(limit, len(coins)))
	fmt.Fprintln(writer, "Актив\tНаблюдения\tУникальные\tСредний спред\tМаксимальный спред\tЭпизоды ≥1%\tЭпизоды ≥2%\tЭпизоды ≥3%\tСредняя длительность ≥1%, сек\tМаксимальная длительность ≥1%, сек\tМаксимальная пара")
	for i, coin := range coins {
		if i >= limit { break }
		fmt.Fprintf(writer, "%s\t%d\t%d\t%s%%\t%s%%\t%d\t%d\t%d\t%d\t%d\t%s\n",
			coin.BaseAsset, coin.Observations, coin.UniqueOpportunities,
			coin.AverageSpread.StringFixed(4), coin.MaxSpread.StringFixed(4),
			thresholdNumber(coin.GroupSummary, 0, func(t ThresholdMetrics) int64 { return t.Episodes }),
			thresholdNumber(coin.GroupSummary, 1, func(t ThresholdMetrics) int64 { return t.Episodes }),
			thresholdNumber(coin.GroupSummary, 2, func(t ThresholdMetrics) int64 { return t.Episodes }),
			thresholdNumber(coin.GroupSummary, 0, func(t ThresholdMetrics) int64 { return t.AverageEpisodeDurationSeconds }),
			thresholdNumber(coin.GroupSummary, 0, func(t ThresholdMetrics) int64 { return t.MaxEpisodeDurationSeconds }),
			coin.MaxSpreadPair)
	}
	fmt.Fprintln(writer)
	routes := append([]RouteSummary(nil), report.Routes...)
	SortRoutes(routes, sortBy)
	fmt.Fprintf(writer, "НАПРАВЛЕНИЯ (сортировка: %s, строк: %d)\n", sortLabel(sortBy), minInt(limit, len(routes)))
	fmt.Fprintln(writer, "Покупка\tПродажа\tТип покупки\tТип продажи\tНаблюдения\tУникальные\tСредний спред\tМаксимум\tЭпизоды ≥1%\tЭпизоды ≥2%\tЭпизоды ≥3%\tСредняя длительность ≥1%, сек\tМаксимальная длительность ≥1%, сек")
	for i, route := range routes {
		if i >= limit { break }
		fmt.Fprintf(writer, "%s\t%s\t%s/%s\t%s/%s\t%d\t%d\t%s%%\t%s%%\t%d\t%d\t%d\t%d\t%d\n",
			route.BuyExchange, route.SellExchange,
			route.BuyMarketType, route.BuyContractType, route.SellMarketType, route.SellContractType,
			route.Observations, route.UniqueOpportunities,
			route.AverageSpread.StringFixed(4), route.MaxSpread.StringFixed(4),
			thresholdNumber(route.GroupSummary, 0, func(t ThresholdMetrics) int64 { return t.Episodes }),
			thresholdNumber(route.GroupSummary, 1, func(t ThresholdMetrics) int64 { return t.Episodes }),
			thresholdNumber(route.GroupSummary, 2, func(t ThresholdMetrics) int64 { return t.Episodes }),
			thresholdNumber(route.GroupSummary, 0, func(t ThresholdMetrics) int64 { return t.AverageEpisodeDurationSeconds }),
			thresholdNumber(route.GroupSummary, 0, func(t ThresholdMetrics) int64 { return t.MaxEpisodeDurationSeconds }))
	}
	fmt.Fprintln(writer)
	fmt.Fprintln(writer, "РАСПРЕДЕЛЕНИЕ ПО ЧАСАМ")
	fmt.Fprintln(writer, "Час\tНаблюдения\tСредний спред\tМаксимум\tНаблюдения ≥1%\t≥2%\t≥3%\tЭпизоды ≥1%\t≥2%\t≥3%")
	for _, hour := range report.Hours {
		fmt.Fprintf(writer, "%02d:00\t%d\t%s%%\t%s%%\t%d\t%d\t%d\t%d\t%d\t%d\n",
			hour.Hour, hour.Observations, hour.AverageSpread.StringFixed(4), hour.MaxSpread.StringFixed(4),
			hour.Thresholds[0].Observations, hour.Thresholds[1].Observations, hour.Thresholds[2].Observations,
			hour.Thresholds[0].EpisodesStarted, hour.Thresholds[1].EpisodesStarted, hour.Thresholds[2].EpisodesStarted)
	}
	if len(report.Warnings) > 0 {
		fmt.Fprintln(writer)
		fmt.Fprintln(writer, "ПРЕДУПРЕЖДЕНИЯ")
		for _, warning := range report.Warnings {
			fmt.Fprintf(writer, "- %s\n", warning)
		}
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("сформировать текстовый отчёт: %w", err)
	}
	return nil
}

func reportPeriod(report Report) (string, string) {
	since, until := "весь доступный период", "весь доступный период"
	if report.Since != nil {
		since = report.Since.UTC().Format(time.RFC3339)
	}
	if report.Until != nil {
		until = report.Until.UTC().Format(time.RFC3339)
	}
	return since, until
}

func formatTime(value *time.Time, location *time.Location) string {
	if value == nil {
		return ""
	}
	return value.In(location).Format(time.RFC3339)
}

func sortLabel(sortBy string) string {
	switch sortBy {
	case "episodes": return "episodes"
	case "avg-spread": return "avg-spread"
	case "max-spread": return "max-spread"
	case "duration": return "duration ≥1%"
	default: return "observations"
	}
}

func minInt(a, b int) int {
	if a < b { return a }
	return b
}

// ValidateSort проверяет ключ сортировки отчёта.
func ValidateSort(value string) error {
	switch strings.TrimSpace(value) {
	case "", "observations", "episodes", "avg-spread", "max-spread", "duration":
		return nil
	default:
		return fmt.Errorf("неизвестная сортировка %q; доступны observations, episodes, avg-spread, max-spread, duration", value)
	}
}

// SortHours оставляет порядок часов 00–23, независимо от сортировок других таблиц.
func SortHours(items []HourSummary) {
	sort.Slice(items, func(i, j int) bool { return items[i].Hour < items[j].Hour })
}
