package config

import (
	"errors"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config является корневой конфигурацией приложения.
type Config struct {
	Scanner   ScannerConfig   `yaml:"scanner"`
	Exchanges ExchangesConfig `yaml:"exchanges"`
	Database  DatabaseConfig  `yaml:"database"`
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
	Gate    ExchangeConfig `yaml:"gate"`
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

	if interval <= 0 {
		return errors.New(
			"scanner.interval должен быть больше нуля",
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

// Validate проверяет значения конфигурации.
func (c Config) Validate() error {
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
