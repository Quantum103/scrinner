package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const validConfigYAML = `scanner:
  interval: 30s
  min_spread_percent: 1.0
  max_data_age: 10s
exchanges:
  binance:
    enabled: true
  bybit:
    enabled: true
database:
  path: ./data/arbitrage.db
stats:
  timezone: UTC
  max_observation_gap: 60s
`

func writeValidationConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadValidatesRequiredFieldsAndDurations(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want string
	}{
		{"missing interval", strings.Replace(validConfigYAML, "  interval: 30s\n", "", 1), "scanner.interval"},
		{"missing spread", strings.Replace(validConfigYAML, "  min_spread_percent: 1.0\n", "", 1), "scanner.min_spread_percent"},
		{"missing max age", strings.Replace(validConfigYAML, "  max_data_age: 10s\n", "", 1), "scanner.max_data_age"},
		{"missing database path", strings.Replace(validConfigYAML, "  path: ./data/arbitrage.db\n", "", 1), "database.path"},
		{"zero interval", strings.Replace(validConfigYAML, "30s", "0s", 1), "scanner.interval"},
		{"negative interval", strings.Replace(validConfigYAML, "30s", "-1s", 1), "scanner.interval"},
		{"bad spread", strings.Replace(validConfigYAML, "min_spread_percent: 1.0", "min_spread_percent: .nan", 1), "scanner.min_spread_percent"},
		{"negative max age", strings.Replace(validConfigYAML, "max_data_age: 10s", "max_data_age: -1s", 1), "scanner.max_data_age"},
		{"bad max age", strings.Replace(validConfigYAML, "max_data_age: 10s", "max_data_age: soon", 1), "scanner.max_data_age"},
		{"bad URL", strings.Replace(validConfigYAML, "  bybit:\n    enabled: true", "  bybit:\n    enabled: true\n  okx:\n    enabled: true\n    base_url: https://user:password@example.com", 1), "exchanges.okx.base_url"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Load(writeValidationConfig(t, test.yaml))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ожидалась ошибка с %q, получено %v", test.want, err)
			}
			if strings.Contains(err.Error(), "password") {
				t.Fatal("ошибка конфигурации раскрыла секретоподобную часть URL")
			}
		})
	}
}

func TestLoadMalformedYAMLAndUnknownFields(t *testing.T) {
	_, err := Load(writeValidationConfig(t, "scanner:\n interval: [\n"))
	if err == nil || !strings.Contains(err.Error(), "разобрать конфигурацию") {
		t.Fatalf("ожидалась диагностируемая ошибка YAML, получено %v", err)
	}

	text := strings.Replace(validConfigYAML, "  interval: 30s", "  interval: 30s\n  interval_typo: 5s", 1)
	text = strings.Replace(text, "exchanges:\n", "unknown_root: true\nexchanges:\n", 1)
	cfg, err := Load(writeValidationConfig(t, text))
	if err != nil {
		t.Fatalf("неизвестные поля не должны блокировать загрузку: %v", err)
	}
	if len(cfg.Warnings) != 2 || cfg.Warnings[0] != "scanner.interval_typo" || cfg.Warnings[1] != "unknown_root" {
		t.Fatalf("неверные предупреждения неизвестных полей: %#v", cfg.Warnings)
	}
}

func TestLoadKeepsExchangeAndStatsDefaults(t *testing.T) {
	text := strings.Replace(validConfigYAML, "stats:\n  timezone: UTC\n  max_observation_gap: 60s\n", "", 1)
	cfg, err := Load(writeValidationConfig(t, text))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Exchanges.MEXC.Enabled || cfg.Exchanges.OKX.Enabled || cfg.Exchanges.Gate.Enabled || cfg.Exchanges.Bitget.Enabled {
		t.Fatal("выключенные по умолчанию адаптеры неожиданно включились")
	}
	if cfg.Stats.Timezone != "UTC" || cfg.Stats.MaxObservationGap != time.Minute {
		t.Fatalf("неверные значения статистики по умолчанию: %#v", cfg.Stats)
	}
}

func TestLoadRejectsUnknownEnabledExchange(t *testing.T) {
	text := strings.Replace(validConfigYAML, "exchanges:\n", "exchanges:\n  kraken:\n    enabled: true\n", 1)
	_, err := Load(writeValidationConfig(t, text))
	if err == nil || !strings.Contains(err.Error(), "exchanges.kraken.enabled") {
		t.Fatalf("включение неподдерживаемой биржи должно быть отклонено: %v", err)
	}
}

func TestLoadURLValidationDoesNotEchoUserInfo(t *testing.T) {
	text := strings.Replace(validConfigYAML, "  bybit:\n    enabled: true", "  bybit:\n    enabled: true\n  gate:\n    enabled: true\n    base_url: https://secret:secret@example.org", 1)
	_, err := Load(writeValidationConfig(t, text))
	if err == nil || strings.Contains(err.Error(), "secret") || !strings.Contains(err.Error(), "exchanges.gate.base_url") {
		t.Fatalf("URL должен отклоняться без раскрытия userinfo: %v", err)
	}
}
