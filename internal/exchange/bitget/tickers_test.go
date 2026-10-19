package bitget

import (
 "context"
 "io"
 "log/slog"
 "net/http"
 "net/http/httptest"
 "testing"
 "time"
 "github.com/example/crypto-arbitrage-scanner/internal/market"
 "github.com/shopspring/decimal"
)

func TestCollectTickersUsesBestPricesBaseVolumeAndMilliseconds(t *testing.T) {
 const ts int64 = 1792440000123
 spot := `{"code":"00000","msg":"success","data":[{"category":"SPOT","symbol":"BTCUSDT","bid1Price":"65000.123456789123456789","ask1Price":"65000.223456789123456789","lastPrice":"1","volume24h":"123.4567890123456789","turnover24h":"987654321.123","ts":"1792440000123"},{"category":"SPOT","symbol":"BADUSDT","bid1Price":"0","ask1Price":"2","ts":"1792440000123"},{"category":"SPOT","symbol":"UNKNOWNUSDT","bid1Price":"1","ask1Price":"2","ts":"1792440000123"}]}`
futures := `{"code":"00000","msg":"success","data":[{"category":"USDT-FUTURES","symbol":"BTCUSDT","bid1Price":"64990.123456789123456789","ask1Price":"64990.223456789123456789","lastPrice":"1","markPrice":"1.5","indexPrice":"1.6","volume24h":"0.01234567890123456789","turnover24h":"777777.123","ts":1792440000123},{"category":"USDT-FUTURES","symbol":"BADFUTUSDT","bid1Price":"1","ask1Price":"-1","ts":"1792440000123"}]}`
server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
 if r.URL.Path != tickersEndpoint { t.Errorf("неверный endpoint: %s", r.URL.Path) }
 w.Header().Set("Content-Type","application/json")
 if r.URL.Query().Get("category")==spotCategory { _,_=io.WriteString(w,spot) } else { _,_=io.WriteString(w,futures) }
}))
defer server.Close()
adapter:=New(newTestClient(t,server.URL,0,time.Second),slog.New(slog.NewTextHandler(io.Discard,nil)))
markets:=[]market.Market{
 {Exchange:"bitget",Symbol:"BTCUSDT",BaseAsset:"BTC",QuoteAsset:"USDT",MarketType:market.MarketTypeSpot,ContractType:market.ContractTypeSpot,Active:true},
 {Exchange:"bitget",Symbol:"BTCUSDT",BaseAsset:"BTC",QuoteAsset:"USDT",MarketType:market.MarketTypeFutures,ContractType:market.ContractTypePerpetual,Active:true},
 {Exchange:"binance",Symbol:"BTCUSDT",BaseAsset:"BTC",QuoteAsset:"USDT",MarketType:market.MarketTypeSpot,ContractType:market.ContractTypeSpot,Active:true},
}
before:=time.Now().UTC()
got,stats,err:=adapter.CollectTickersWithStats(context.Background(),markets)
if err!=nil {t.Fatal(err)}
if len(got)!=2 || stats.Received!=5 || stats.Valid!=2 || stats.Skipped!=3 {t.Fatalf("неверная статистика: %#v, tickers=%d",stats,len(got))}
wantTime:=time.UnixMilli(ts).UTC()
seen:=map[market.MarketType]bool{}
for _,x:=range got {
 if x.ExchangeTimestamp==nil || !x.ExchangeTimestamp.Equal(wantTime) || !x.Timestamp.Equal(wantTime) || x.ReceivedAt.Before(before) || x.ReceivedAt.Location()!=time.UTC {t.Fatalf("неверные времена: %#v",x)}
 seen[x.MarketType]=true
 var bid,ask,volume string
 switch x.MarketType {
 case market.MarketTypeSpot: bid="65000.123456789123456789"; ask="65000.223456789123456789"; volume="123.4567890123456789"
 case market.MarketTypeFutures: bid="64990.123456789123456789"; ask="64990.223456789123456789"; volume="0.01234567890123456789"
 default: t.Fatalf("неверный тип: %s",x.MarketType)
 }
 assertBitgetDecimal(t,x.Bid.String(),bid); assertBitgetDecimal(t,x.Ask.String(),ask)
 if x.Volume24h==nil {t.Fatal("отсутствует volume24h")}; assertBitgetDecimal(t,x.Volume24h.String(),volume)
}
if !seen[market.MarketTypeSpot] || !seen[market.MarketTypeFutures] {t.Fatalf("Spot/Perpetual not separated: %#v",seen)}
}

func TestTimestampAndPriceParsersRejectInvalidInput(t *testing.T) {
 for _,raw:=range []string{"","null",`"bad"`,`"0"`,`"-1"`,`1.5`} {if _,err:=parseTimestampMilli([]byte(raw)); err==nil {t.Errorf("ts=%q should fail",raw)}}
 for _,raw:=range []string{"","null",`"0"`,`"-1"`,`"bad"`} {if _,err:=parsePositiveDecimal([]byte(raw)); err==nil {t.Errorf("price=%q should fail",raw)}}
}

func assertBitgetDecimal(t *testing.T,gotText,wantText string) {
 t.Helper()
 got,err:=decimal.NewFromString(gotText); if err!=nil {t.Fatal(err)}
 want,err:=decimal.NewFromString(wantText); if err!=nil {t.Fatal(err)}
 if !got.Equal(want) {t.Fatalf("decimal differs: got=%s want=%s",got,want)}
}
