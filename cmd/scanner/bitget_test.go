package main

import (
 "context"
 "database/sql"
 "io"
 "log/slog"
 "net/http"
 "net/http/httptest"
 "path/filepath"
 "testing"
 "time"

 "github.com/example/crypto-arbitrage-scanner/internal/exchange/bitget"
 "github.com/example/crypto-arbitrage-scanner/internal/storage"
)

func TestRunBitgetPassStoresSpotAndPerpetualInSQLite(t *testing.T) {
 server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request) {
  w.Header().Set("Content-Type","application/json")
  switch r.URL.Path {
  case "/api/v3/market/instruments":
   switch r.URL.Query().Get("category") {
   case "SPOT": _,_=io.WriteString(w,`{"code":"00000","msg":"success","data":[{"category":"SPOT","symbol":"BTCUSDT","baseCoin":"BTC","quoteCoin":"USDT","status":"online","symbolType":"crypto","isReality":"no"}]}`)
   case "USDT-FUTURES": _,_=io.WriteString(w,`{"code":"00000","msg":"success","data":[{"category":"USDT-FUTURES","symbol":"BTCUSDT","baseCoin":"BTC","quoteCoin":"USDT","status":"online","type":"perpetual","symbolType":"crypto","isRwa":"NO"}]}`)
   default: http.Error(w,"category",http.StatusBadRequest)
   }
  case "/api/v3/market/tickers":
   switch r.URL.Query().Get("category") {
   case "SPOT": _,_=io.WriteString(w,`{"code":"00000","msg":"success","data":[{"category":"SPOT","symbol":"BTCUSDT","bid1Price":"65000.123456789123456789","ask1Price":"65000.223456789123456789","lastPrice":"1","volume24h":"1.23456789","turnover24h":"999999","ts":"1792440000123"}]}`)
   case "USDT-FUTURES": _,_=io.WriteString(w,`{"code":"00000","msg":"success","data":[{"category":"USDT-FUTURES","symbol":"BTCUSDT","bid1Price":"64990.123456789123456789","ask1Price":"64990.223456789123456789","lastPrice":"1","markPrice":"1.5","indexPrice":"1.6","volume24h":"0.123456789","turnover24h":"888888","ts":"1792440001123"}]}`)
   default: http.Error(w,"category",http.StatusBadRequest)
   }
  default: http.NotFound(w,r)
  }
 }))
 defer server.Close()
 ctx:=context.Background()
 db,err:=storage.Open(ctx,filepath.Join(t.TempDir(),"bitget.db")); if err!=nil {t.Fatal(err)}; defer db.Close()
 if err:=db.Migrate(ctx,"../../migrations"); err!=nil {t.Fatal(err)}
 client,err:=bitget.NewClient(bitget.ClientConfig{BaseURL:server.URL,HTTPClient:&http.Client{Timeout:time.Second},MaxRetries:0,RetryBaseDelay:time.Millisecond}); if err!=nil {t.Fatal(err)}
 logger:=slog.New(slog.NewTextHandler(io.Discard,nil))
 if err:=runBitgetPassWithAdapter(ctx,logger,db,bitget.New(client,logger)); err!=nil {t.Fatal(err)}
 var count int
 if err:=db.SQLDB().QueryRowContext(ctx,"SELECT COUNT(*) FROM markets WHERE exchange='bitget'").Scan(&count); err!=nil {t.Fatal(err)}
 if count!=2 {t.Fatalf("ожидалось 2 рынка, получено %d",count)}
 if err:=db.SQLDB().QueryRowContext(ctx,"SELECT COUNT(*) FROM price_snapshots p JOIN markets m ON m.id=p.market_id WHERE m.exchange='bitget'").Scan(&count); err!=nil {t.Fatal(err)}
 if count!=2 {t.Fatalf("ожидалось 2 ценовых снимка, получено %d",count)}
 rows,err:=db.SQLDB().QueryContext(ctx,"SELECT m.symbol,m.market_type,m.contract_type,p.bid,p.ask,p.volume_24h,p.exchange_timestamp,p.received_at FROM price_snapshots p JOIN markets m ON m.id=p.market_id WHERE m.exchange='bitget' ORDER BY m.market_type")
 if err!=nil {t.Fatal(err)}; defer rows.Close()
 seenSpot,seenPerpetual:=false,false
 for rows.Next() {
  var symbol,typ,contract,bid,ask string
  var volume,exchangeTime,receivedAt sql.NullString
  if err:=rows.Scan(&symbol,&typ,&contract,&bid,&ask,&volume,&exchangeTime,&receivedAt); err!=nil {t.Fatal(err)}
  if symbol!="BTCUSDT" || !exchangeTime.Valid || !receivedAt.Valid || !volume.Valid {t.Fatalf("неполный снимок: %q %q %q %#v %#v %#v",symbol,typ,contract,volume,exchangeTime,receivedAt)}
  switch typ {
  case "spot":
   seenSpot=true
   if contract!="spot" || bid!="65000.123456789123456789" || ask!="65000.223456789123456789" || volume.String!="1.23456789" {t.Fatalf("неверный Spot снимок: %s %s %s %s %#v",contract,bid,ask,volume.String,volume)}
  case "futures":
   seenPerpetual=true
   if contract!="perpetual" || bid!="64990.123456789123456789" || ask!="64990.223456789123456789" || volume.String!="0.123456789" {t.Fatalf("неверный Perpetual снимок: %s %s %s %s",contract,bid,ask,volume.String)}
  default: t.Fatalf("неизвестный market_type %q",typ)
  }
 }
 if err:=rows.Err(); err!=nil {t.Fatal(err)}
 if !seenSpot || !seenPerpetual {t.Fatal("Spot и Perpetual не сохранены раздельно")}
}

func TestRunBitgetPassDisabledDoesNotAccessNetworkOrDatabase(t *testing.T) {
 logger:=slog.New(slog.NewTextHandler(io.Discard,nil))
 if err:=runBitgetPassIfEnabled(context.Background(),false,logger,nil); err!=nil {t.Fatalf("выключенный Bitget не должен обращаться к сети или БД: %v",err)}
}
