package market

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// SupportedQuoteAssets содержит котируемые активы,
// разрешённые текущей версией scanner.
var SupportedQuoteAssets = []string{"USDT"}

// NormalizeMarket приводит структурированные данные рынка
// к единому внутреннему представлению.
//
// Поля BaseAsset и QuoteAsset считаются источником истины.
// Symbol сохраняется без изменения, потому что это оригинальное
// обозначение инструмента конкретной биржи.
func NormalizeMarket(m Market) (Market, error) {
	normalized := m

	normalized.Exchange = strings.TrimSpace(m.Exchange)
	normalized.BaseAsset = normalizeAsset(m.BaseAsset)
	normalized.QuoteAsset = normalizeAsset(m.QuoteAsset)

	if err := ValidateMarket(normalized); err != nil {
		return Market{}, err
	}

	return normalized, nil
}

// normalizeAsset приводит обозначение актива к каноническому виду.
func normalizeAsset(asset string) string {
	return strings.ToUpper(strings.TrimSpace(asset))
}

// NormalizeSymbolFromParts создаёт канонический символ
// из уже структурированных данных.
//
// Этот вариант является предпочтительным для exchange adapters,
// поскольку не требует угадывать структуру оригинального символа.
func NormalizeSymbolFromParts(baseAsset, quoteAsset string) (Symbol, error) {
	result := NewSymbol(baseAsset, quoteAsset)

	if !result.IsValid() {
		return Symbol{}, errors.New("base asset and quote asset are required")
	}

	if result.QuoteAsset != "USDT" {
		return Symbol{}, fmt.Errorf(
			"unsupported quote asset: %q",
			result.QuoteAsset,
		)
	}

	if result.BaseAsset == result.QuoteAsset {
		return Symbol{}, errors.New("base asset and quote asset must be different")
	}

	return result, nil
}

// ParseSymbol пытается разобрать оригинальный символ,
// когда структурированные BaseAsset и QuoteAsset недоступны.
//
// Для символов с разделителем используются только однозначные формы.
// Для слитых символов вроде BTCUSDT необходим список известных
// базовых активов. Это предотвращает опасное слепое удаление USDT.
func ParseSymbol(symbol string, knownBaseAssets, knownQuoteAssets []string) (Symbol, error) {
	normalized := strings.ToUpper(strings.TrimSpace(symbol))

	if normalized == "" {
		return Symbol{}, errors.New("symbol is empty")
	}

	if containsUnsupportedCharacters(normalized) {
		return Symbol{}, fmt.Errorf(
			"symbol contains unsupported characters: %q",
			symbol,
		)
	}

	if baseAsset, quoteAsset, ok := splitDelimitedSymbol(normalized); ok {
		return NormalizeSymbolFromParts(baseAsset, quoteAsset)
	}

	baseAssets := normalizeAssetList(knownBaseAssets)
	quoteAssets := normalizeAssetList(knownQuoteAssets)

	if len(baseAssets) == 0 {
		return Symbol{}, errors.New(
			"cannot parse concatenated symbol without known base assets",
		)
	}

	if len(quoteAssets) == 0 {
		return Symbol{}, errors.New(
			"cannot parse symbol without known quote assets",
		)
	}

	sort.Slice(baseAssets, func(i, j int) bool {
		return len(baseAssets[i]) > len(baseAssets[j])
	})

	sort.Slice(quoteAssets, func(i, j int) bool {
		return len(quoteAssets[i]) > len(quoteAssets[j])
	})

	for _, quoteAsset := range quoteAssets {
		if !strings.HasSuffix(normalized, quoteAsset) {
			continue
		}

		basePart := strings.TrimSuffix(normalized, quoteAsset)
		if basePart == "" {
			continue
		}

		for _, baseAsset := range baseAssets {
			if basePart == baseAsset {
				return NormalizeSymbolFromParts(baseAsset, quoteAsset)
			}
		}
	}

	return Symbol{}, fmt.Errorf(
		"cannot safely parse symbol %q from known assets",
		symbol,
	)
}

// CanonicalMarketKey возвращает ключ рынка без учёта биржи.
//
// Spot и Perpetual намеренно получают разные ключи.
func CanonicalMarketKey(m Market) (string, error) {
	normalized, err := NormalizeMarket(m)
	if err != nil {
		return "", err
	}

	switch {
	case normalized.IsSpot():
		return fmt.Sprintf(
			"%s:%s:SPOT",
			normalized.BaseAsset,
			normalized.QuoteAsset,
		), nil

	case normalized.IsPerpetual():
		return fmt.Sprintf(
			"%s:%s:PERPETUAL",
			normalized.BaseAsset,
			normalized.QuoteAsset,
		), nil

	default:
		return "", fmt.Errorf(
			"market is not supported: %s",
			normalized.Symbol,
		)
	}
}

// MarketGroup содержит рынки, имеющие один canonical market key.
type MarketGroup struct {
	Key     string
	Markets []Market
}

// GroupMarkets группирует активные поддерживаемые рынки
// по base asset, quote asset и типу рынка.
func GroupMarkets(markets []Market) (map[string][]Market, error) {
	groups := make(map[string][]Market)

	for _, m := range markets {
		normalized, err := NormalizeMarket(m)
		if err != nil {
			return nil, fmt.Errorf(
				"normalize market %q: %w",
				m.Symbol,
				err,
			)
		}

		if !normalized.IsSupported() {
			continue
		}

		key, err := CanonicalMarketKey(normalized)
		if err != nil {
			return nil, fmt.Errorf(
				"build canonical key for %q: %w",
				normalized.Symbol,
				err,
			)
		}

		groups[key] = append(groups[key], normalized)
	}

	for key := range groups {
		sort.Slice(groups[key], func(i, j int) bool {
			if groups[key][i].Exchange != groups[key][j].Exchange {
				return groups[key][i].Exchange < groups[key][j].Exchange
			}

			return groups[key][i].Symbol < groups[key][j].Symbol
		})
	}

	return groups, nil
}

// BuildMarketGroups возвращает отсортированный список групп.
//
// Метод удобен для будущего scanner, которому нужен стабильный порядок
// обработки результатов.
func BuildMarketGroups(markets []Market) ([]MarketGroup, error) {
	groupMap, err := GroupMarkets(markets)
	if err != nil {
		return nil, err
	}

	keys := make([]string, 0, len(groupMap))
	for key := range groupMap {
		keys = append(keys, key)
	}

	sort.Strings(keys)

	groups := make([]MarketGroup, 0, len(keys))

	for _, key := range keys {
		groups = append(groups, MarketGroup{
			Key:     key,
			Markets: groupMap[key],
		})
	}

	return groups, nil
}

// splitDelimitedSymbol разбирает символы, в которых части пары разделены
// одним из поддерживаемых разделителей.
func splitDelimitedSymbol(symbol string) (string, string, bool) {
	separators := []string{"-", "_", "/"}

	for _, separator := range separators {
		if !strings.Contains(symbol, separator) {
			continue
		}

		parts := strings.Split(symbol, separator)

		if len(parts) != 2 {
			return "", "", false
		}

		baseAsset := strings.TrimSpace(parts[0])
		quoteAsset := strings.TrimSpace(parts[1])

		if baseAsset == "" || quoteAsset == "" {
			return "", "", false
		}

		return baseAsset, quoteAsset, true
	}

	return "", "", false
}

// normalizeAssetList нормализует список известных активов
// и удаляет повторяющиеся значения.
func normalizeAssetList(assets []string) []string {
	seen := make(map[string]struct{}, len(assets))
	result := make([]string, 0, len(assets))

	for _, asset := range assets {
		normalized := normalizeAsset(asset)
		if normalized == "" {
			continue
		}

		if _, exists := seen[normalized]; exists {
			continue
		}

		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}

	return result
}

// containsUnsupportedCharacters проверяет очевидно недопустимые
// управляющие и пробельные символы внутри символа.
func containsUnsupportedCharacters(symbol string) bool {
	for _, r := range symbol {
		if unicode.IsControl(r) || unicode.IsSpace(r) {
			return true
		}
	}

	return false
}
