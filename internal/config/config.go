package config

import (
	"errors"
	"fmt"
	"math"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config является корневой конфигурацией приложения.
type Config struct {
	Scanner   ScannerConfig   `yaml:"scanner"`
	Exchanges ExchangesConfig `yaml:"exchanges"`
	Database  DatabaseConfig  `yaml:"database"`
	Stats     StatsConfig     `yaml:"stats"`
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
	OKX     OKXConfig       `yaml:"okx"`
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

	if c.Database.Path == "" {
		return errors.New(
			"database.path обязателен",
		)
	}

	return nil
}
