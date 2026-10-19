package price

import (
	"time"

	"github.com/example/crypto-arbitrage-scanner/internal/market"
	"github.com/shopspring/decimal"
)

// Ticker содержит текущие лучшие значения bid/ask конкретного рынка.
type Ticker struct {
	Exchange string
	Symbol   string

	MarketType   market.MarketType
	ContractType market.ContractType

	Bid decimal.Decimal
	Ask decimal.Decimal

	Volume24h *decimal.Decimal

	// Timestamp — основная временная точка котировки по контракту текущего адаптера.
	Timestamp time.Time

	// ReceivedAt — локальное UTC-время получения HTTP-ответа с котировкой.
	// Нулевое значение означает, что актуальность получения подтвердить нельзя.
	ReceivedAt time.Time

	// ExchangeTimestamp — временная метка рыночных данных от биржи, если она предоставляется.
	// nil означает, что точное время биржевого обновления неизвестно.
	ExchangeTimestamp *time.Time
}

// IsValid проверяет, содержит ли тикер корректные рыночные данные.
func (t Ticker) IsValid() bool {
	if t.Exchange == "" || t.Symbol == "" {
		return false
	}
	if !t.Bid.GreaterThan(decimal.Zero) || !t.Ask.GreaterThan(decimal.Zero) {
		return false
	}
	if t.Timestamp.IsZero() {
		return false
	}
	// Пересечённый или инвертированный стакан не используется.
	if t.Bid.GreaterThan(t.Ask) {
		return false
	}
	return true
}

// Age возвращает возраст основной временной точки тикера.
func (t Ticker) Age(now time.Time) time.Duration {
	if t.Timestamp.IsZero() {
		return 0
	}
	return now.Sub(t.Timestamp)
}

// ObservationTime возвращает время данных, пригодное для сопоставления двух котировок.
// Биржевая метка имеет приоритет; при её отсутствии используется локальное время получения.
func (t Ticker) ObservationTime() time.Time {
	if t.ExchangeTimestamp != nil && !t.ExchangeTimestamp.IsZero() {
		return t.ExchangeTimestamp.UTC()
	}
	if !t.ReceivedAt.IsZero() {
		return t.ReceivedAt.UTC()
	}
	return time.Time{}
}

// IsFresh проверяет возраст котировки, время получения и доступную биржевую метку.
// Отсутствующее время получения не считается доказательством свежести.
func (t Ticker) IsFresh(now time.Time, maxAge time.Duration) bool {
	if !t.IsValid() || now.IsZero() || maxAge <= 0 || t.ReceivedAt.IsZero() {
		return false
	}
	if !timestampWithinAge(t.ReceivedAt, now, maxAge) {
		return false
	}
	if !timestampWithinAge(t.Timestamp, now, maxAge) {
		return false
	}
	if t.ExchangeTimestamp != nil && !timestampWithinAge(*t.ExchangeTimestamp, now, maxAge) {
		return false
	}
	return true
}

func timestampWithinAge(timestamp, now time.Time, maxAge time.Duration) bool {
	if timestamp.IsZero() {
		return false
	}
	age := now.Sub(timestamp)
	return age >= 0 && age <= maxAge
}
