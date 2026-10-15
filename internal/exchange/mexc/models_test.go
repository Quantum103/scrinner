package mexc

import (
	"encoding/json"
	"testing"
)

// TestListModelsAcceptObjectAndArray проверяет оба документируемых формата ответов MEXC.
func TestListModelsAcceptObjectAndArray(t *testing.T) {
	t.Run("Spot bookTicker object", func(t *testing.T) {
		var response spotTickerList
		if err := json.Unmarshal([]byte(`{"symbol":"BTCUSDT","bidPrice":"1.0","askPrice":"1.1"}`), &response); err != nil {
			t.Fatal(err)
		}
		if len(response) != 1 || response[0].Symbol != "BTCUSDT" {
			t.Fatalf("неверно разобран объект Spot bookTicker: %#v", response)
		}
	})

	t.Run("Spot 24hr array", func(t *testing.T) {
		var response spotStatsList
		if err := json.Unmarshal([]byte(`[{"symbol":"BTCUSDT","volume":"12.34"},{"symbol":"ETHUSDT","volume":"56.78"}]`), &response); err != nil {
			t.Fatal(err)
		}
		if len(response) != 2 || response[1].Symbol != "ETHUSDT" {
			t.Fatalf("неверно разобран массив Spot 24hr: %#v", response)
		}
	})

	t.Run("Futures contract object", func(t *testing.T) {
		var response futuresContractList
		if err := json.Unmarshal([]byte(`{"symbol":"BTC_USDT","baseCoin":"BTC","quoteCoin":"USDT","settleCoin":"USDT","futureType":1,"state":0}`), &response); err != nil {
			t.Fatal(err)
		}
		if len(response) != 1 || response[0].Symbol != "BTC_USDT" {
			t.Fatalf("неверно разобран объект Futures contract: %#v", response)
		}
	})

	t.Run("Futures ticker array", func(t *testing.T) {
		var response futuresTickerList
		if err := json.Unmarshal([]byte(`[{"symbol":"BTC_USDT","bid1":1,"ask1":2},{"symbol":"ETH_USDT","bid1":3,"ask1":4}]`), &response); err != nil {
			t.Fatal(err)
		}
		if len(response) != 2 || response[0].Symbol != "BTC_USDT" {
			t.Fatalf("неверно разобран массив Futures ticker: %#v", response)
		}
	})
}
