package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadGateConfigEnabledAndBaseURL(t *testing.T) {
	t.Parallel()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	const yamlConfig = `scanner:
  interval: 30s
  min_spread_percent: 1
  max_data_age: 10s
exchanges:
  gate:
    enabled: true
    base_url: https://api.gateio.ws
database:
  path: ./data/test.db
`
	if err := os.WriteFile(configPath, []byte(yamlConfig), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Exchanges.Gate.Enabled || cfg.Exchanges.Gate.BaseURL != "https://api.gateio.ws" {
		t.Fatalf("конфигурация Gate загружена неверно: %#v", cfg.Exchanges.Gate)
	}
}

func TestLoadGateDefaultsDisabled(t *testing.T) {
	t.Parallel()
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	const yamlConfig = `scanner:
  interval: 30s
  min_spread_percent: 1
  max_data_age: 10s
exchanges: {}
database:
  path: ./data/test.db
`
	if err := os.WriteFile(configPath, []byte(yamlConfig), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Exchanges.Gate.Enabled || cfg.Exchanges.Gate.BaseURL != "" {
		t.Fatalf("Gate должен быть выключен по умолчанию: %#v", cfg.Exchanges.Gate)
	}
}
