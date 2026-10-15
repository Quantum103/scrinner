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

	// Timestamp задаёт временную точку снимка, используемую существующим storage.
	// Если API сообщает время котировки, оно используется; иначе указывается время получения.
	Timestamp time.Time

	// ReceivedAt задаёт локальное UTC-время получения ответа с тикером.
	// Поле может оставаться нулевым у адаптеров, которые ещё не заполняют его.
	ReceivedAt time.Time

	// ExchangeTimestamp содержит время котировки от биржи, если endpoint предоставляет его.
	// nil означает, что точное время биржевого обновления неизвестно.
	ExchangeTimestamp *time.Time
}

// IsValid проверяет, содержит ли тикер корректные рыночные данные.
func (t Ticker) IsValid() bool {
	if t.Exchange == "" || t.Symbol == "" {
		return false
	}

	if !t.Bid.GreaterThan(decimal.Zero) {
		return false
	}

	if !t.Ask.GreaterThan(decimal.Zero) {
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

// Age возвращает возраст временной точки тикера относительно указанного момента времени.
func (t Ticker) Age(now time.Time) time.Duration {
	if t.Timestamp.IsZero() {
		return 0
	}

	return now.Sub(t.Timestamp)
}

// IsFresh проверяет, не старше ли тикер допустимого возраста.
func (t Ticker) IsFresh(now time.Time, maxAge time.Duration) bool {
	if !t.IsValid() {
		return false
	}

	if maxAge <= 0 {
		return false
	}

	age := t.Age(now)

	// Будущие временные метки также считаются некорректными.
	if age < 0 {
		return false
	}

	return age <= maxAge
}
