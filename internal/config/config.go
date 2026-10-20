package config

import (
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config является корневой конфигурацией приложения.
type Config struct {
	Scanner   ScannerConfig   `yaml:"scanner"`
	Exchanges ExchangesConfig `yaml:"exchanges"`
	Database  DatabaseConfig  `yaml:"database"`
	Stats     StatsConfig     `yaml:"stats"`
	Warnings  []string        `yaml:"-"`
}

// ScannerConfig содержит настройки времени работы scanner.
type ScannerConfig struct {
	Interval         time.Duration `yaml:"-"`
	MinSpreadPercent float64       `yaml:"min_spread_percent"`
	MaxDataAge       time.Duration `yaml:"-"`
	IntervalRaw      string        `yaml:"interval"`
	MaxDataAgeRaw    string        `yaml:"max_data_age"`
}

// ExchangesConfig содержит настройки отдельных бирж.
type ExchangesConfig struct {
	Binance ExchangeConfig `yaml:"binance"`
	Bybit   ExchangeConfig `yaml:"bybit"`
	MEXC    ExchangeConfig `yaml:"mexc"`
	OKX     OKXConfig      `yaml:"okx"`
	Gate    GateConfig     `yaml:"gate"`
	Bitget  ExchangeConfig `yaml:"bitget"`
}

// ExchangeConfig включает или выключает адаптер биржи.
type ExchangeConfig struct {
	Enabled bool `yaml:"enabled"`
}

// OKXConfig содержит настройки публичного API OKX.
type OKXConfig struct {
	Enabled bool   `yaml:"enabled"`
	BaseURL string `yaml:"base_url"`
}

// GateConfig содержит настройки публичного API Gate.
type GateConfig struct {
	Enabled bool   `yaml:"enabled"`
	BaseURL string `yaml:"base_url"`
}

// StatsConfig содержит настройки формирования исторических отчётов.
type StatsConfig struct {
	Timezone             string        `yaml:"timezone"`
	MaxObservationGap    time.Duration `yaml:"-"`
	MaxObservationGapRaw string        `yaml:"max_observation_gap"`
}

// DatabaseConfig содержит настройки SQLite.
type DatabaseConfig struct {
	Path string `yaml:"path"`
}

// Load загружает и проверяет конфигурацию из YAML-файла.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf(
			"прочитать конфигурацию %q: %w",
			path,
			err,
		)
	}

	var cfg Config

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf(
			"разобрать конфигурацию %q: %w",
			path,
			err,
		)
	}
	if err := validateRequiredFields(data); err != nil {
		return Config{}, err
	}
	cfg.Warnings = unknownYAMLFields(data)

	if err := cfg.parseDurations(); err != nil {
		return Config{}, err
	}

	if err := cfg.parseStats(); err != nil {
		return Config{}, err
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// validateRequiredFields проверяет наличие параметров, значения по умолчанию для которых опасны.
func validateRequiredFields(data []byte) error {
	var raw struct {
		Scanner  map[string]any `yaml:"scanner"`
		Database map[string]any `yaml:"database"`
	}
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("проверить обязательные поля конфигурации: %w", err)
	}

	required := []struct {
		path   string
		values map[string]any
		key    string
	}{
		{path: "scanner.interval", values: raw.Scanner, key: "interval"},
		{path: "scanner.min_spread_percent", values: raw.Scanner, key: "min_spread_percent"},
		{path: "scanner.max_data_age", values: raw.Scanner, key: "max_data_age"},
		{path: "database.path", values: raw.Database, key: "path"},
	}
	for _, field := range required {
		value, exists := field.values[field.key]
		if !exists || value == nil {
			return fmt.Errorf("%s обязателен и не может быть null", field.path)
		}
	}
	return nil
}

// unknownYAMLFields находит неизвестные ключи для диагностики, не блокируя старые конфигурации.
func unknownYAMLFields(data []byte) []string {
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil || len(document.Content) == 0 {
		return nil
	}
	root := document.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil
	}

	rootFields := map[string]bool{
		"scanner": true, "exchanges": true, "database": true, "stats": true,
	}
	sectionFields := map[string]map[string]bool{
		"scanner":  {"interval": true, "min_spread_percent": true, "max_data_age": true},
		"database": {"path": true},
		"stats":    {"timezone": true, "max_observation_gap": true},
	}
	exchangeFields := map[string]map[string]bool{
		"binance": {"enabled": true},
		"bybit":   {"enabled": true},
		"mexc":    {"enabled": true},
		"okx":     {"enabled": true, "base_url": true},
		"gate":    {"enabled": true, "base_url": true},
		"bitget":  {"enabled": true},
	}

	var warnings []string
	for i := 0; i+1 < len(root.Content); i += 2 {
		section := root.Content[i].Value
		value := root.Content[i+1]
		if !rootFields[section] {
			warnings = append(warnings, section)
			continue
		}
		if section == "exchanges" {
			if value.Kind != yaml.MappingNode {
				continue
			}
			for j := 0; j+1 < len(value.Content); j += 2 {
				exchangeName := value.Content[j].Value
				exchangeNode := value.Content[j+1]
				fields, known := exchangeFields[exchangeName]
				if !known {
					warnings = append(warnings, "exchanges."+exchangeName)
					continue
				}
				if exchangeNode.Kind != yaml.MappingNode {
					continue
				}
				for k := 0; k+1 < len(exchangeNode.Content); k += 2 {
					fieldName := exchangeNode.Content[k].Value
					if !fields[fieldName] {
						warnings = append(warnings, "exchanges."+exchangeName+"."+fieldName)
					}
				}
			}
			continue
		}
		if value.Kind != yaml.MappingNode {
			continue
		}
		fields := sectionFields[section]
		for j := 0; j+1 < len(value.Content); j += 2 {
			fieldName := value.Content[j].Value
			if !fields[fieldName] {
				warnings = append(warnings, section+"."+fieldName)
			}
		}
	}
	sort.Strings(warnings)
	return warnings
}

// parseDurations преобразует строковые интервалы YAML в time.Duration.
func (c *Config) parseDurations() error {
	if c.Scanner.IntervalRaw == "" {
		return errors.New(
			"scanner.interval обязателен",
		)
	}

	interval, err := time.ParseDuration(
		c.Scanner.IntervalRaw,
	)
	if err != nil {
		return fmt.Errorf(
			"разобрать scanner.interval %q: %w",
			c.Scanner.IntervalRaw,
			err,
		)
	}

	if interval < time.Second {
		return errors.New(
			"scanner.interval должен быть не меньше 1s",
		)
	}

	c.Scanner.Interval = interval

	if c.Scanner.MaxDataAgeRaw == "" {
		return errors.New(
			"scanner.max_data_age обязателен",
		)
	}

	maxDataAge, err := time.ParseDuration(
		c.Scanner.MaxDataAgeRaw,
	)
	if err != nil {
		return fmt.Errorf(
			"разобрать scanner.max_data_age %q: %w",
			c.Scanner.MaxDataAgeRaw,
			err,
		)
	}

	if maxDataAge <= 0 {
		return errors.New(
			"scanner.max_data_age должен быть больше нуля",
		)
	}

	c.Scanner.MaxDataAge = maxDataAge

	return nil
}

// parseStats применяет значения по умолчанию и проверяет настройки статистики.
func (c *Config) parseStats() error {
	if c.Stats.Timezone == "" {
		c.Stats.Timezone = "UTC"
	}
	if _, err := time.LoadLocation(c.Stats.Timezone); err != nil {
		return fmt.Errorf("stats.timezone %q: %w", c.Stats.Timezone, err)
	}
	if c.Stats.MaxObservationGapRaw == "" {
		c.Stats.MaxObservationGap = 60 * time.Second
		if candidate := 2 * c.Scanner.Interval; candidate > c.Stats.MaxObservationGap {
			c.Stats.MaxObservationGap = candidate
		}
		return nil
	}
	gap, err := time.ParseDuration(c.Stats.MaxObservationGapRaw)
	if err != nil {
		return fmt.Errorf("разобрать stats.max_observation_gap %q: %w", c.Stats.MaxObservationGapRaw, err)
	}
	if gap <= 0 {
		return errors.New("stats.max_observation_gap должен быть больше нуля")
	}
	c.Stats.MaxObservationGap = gap
	return nil
}

// Validate проверяет значения конфигурации.
func (c Config) Validate() error {
	if c.Scanner.Interval < time.Second {
		return errors.New("scanner.interval должен быть не меньше 1s")
	}
	if c.Scanner.MaxDataAge <= 0 {
		return errors.New("scanner.max_data_age должен быть больше нуля")
	}
	if math.IsNaN(c.Scanner.MinSpreadPercent) || math.IsInf(c.Scanner.MinSpreadPercent, 0) {
		return errors.New(
			"scanner.min_spread_percent должен быть конечным числом",
		)
	}
	if c.Scanner.MinSpreadPercent < 0 {
		return errors.New(
			"scanner.min_spread_percent не может быть отрицательным",
		)
	}

	if strings.TrimSpace(c.Database.Path) == "" {
		return errors.New("database.path обязателен и не может быть пустым")
	}
	if err := validateOptionalBaseURL("exchanges.okx.base_url", c.Exchanges.OKX.BaseURL); err != nil {
		return err
	}
	if err := validateOptionalBaseURL("exchanges.gate.base_url", c.Exchanges.Gate.BaseURL); err != nil {
		return err
	}
	return nil
}

// validateOptionalBaseURL проверяет настраиваемый URL, не печатая потенциальные учётные данные.
func validateOptionalBaseURL(field, raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed == nil || parsed.Hostname() == "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return fmt.Errorf("%s должен быть абсолютным HTTP(S) URL без учётных данных, пути, query и fragment", field)
	}
	return nil
}
