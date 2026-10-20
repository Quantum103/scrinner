package dashboard

import (
	"context"
	"database/sql"
	"embed"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"time"

	"github.com/example/crypto-arbitrage-scanner/internal/storage"
	"github.com/shopspring/decimal"
)

const (
	DefaultAddress = "127.0.0.1:8080"
	DefaultURL     = "http://127.0.0.1:8080"
	maxOpportunities = 500
)

//go:embed index.html
var assets embed.FS

// Server обслуживает локальную панель только на loopback-интерфейсе.
type Server struct {
	httpServer *http.Server
	logger     *slog.Logger
}

// CycleSummary — итог одного цикла сканирования для панели.
type CycleSummary struct {
	ID                    string `json:"id"`
	StartedAt             string `json:"startedAt"`
	FinishedAt            string `json:"finishedAt"`
	Status                string `json:"status"`
	EnabledExchanges      int    `json:"enabledExchanges"`
	SucceededExchanges    int    `json:"succeededExchanges"`
	FailedExchanges       int    `json:"failedExchanges"`
	MarketsDiscovered     int    `json:"marketsDiscovered"`
	TickersReceived       int    `json:"tickersReceived"`
	TickersAccepted       int    `json:"tickersAccepted"`
	TickersRejected       int    `json:"tickersRejected"`
	SnapshotsSaved        int    `json:"snapshotsSaved"`
	OpportunitiesDetected int    `json:"opportunitiesDetected"`
	OpportunitiesSaved    int    `json:"opportunitiesSaved"`
	PersistenceErrors     int    `json:"persistenceErrors"`
	MinSpreadPercent      string `json:"minSpreadPercent"`
	Error                 string `json:"error,omitempty"`
}

// OpportunityView содержит читаемое представление сигнала из одного завершённого цикла.
type OpportunityView struct {
	ID               int64  `json:"id"`
	Timestamp        string `json:"timestamp"`
	Symbol           string `json:"symbol"`
	BaseAsset        string `json:"baseAsset"`
	QuoteAsset       string `json:"quoteAsset"`
	BuyExchange      string `json:"buyExchange"`
	BuyMarketType    string `json:"buyMarketType"`
	BuyContractType  string `json:"buyContractType"`
	BuySymbol        string `json:"buySymbol"`
	BuyPrice         string `json:"buyPrice"`
	BuyVolume24h     string `json:"buyVolume24h,omitempty"`
	SellExchange     string `json:"sellExchange"`
	SellMarketType   string `json:"sellMarketType"`
	SellContractType string `json:"sellContractType"`
	SellSymbol       string `json:"sellSymbol"`
	SellPrice        string `json:"sellPrice"`
	SellVolume24h    string `json:"sellVolume24h,omitempty"`
	SpreadPercent    string `json:"spreadPercent"`
}

// Payload — данные, которые браузер обновляет после каждого запроса к API.
type Payload struct {
	GeneratedAt          time.Time         `json:"generatedAt"`
	CurrentCycle         *CycleSummary     `json:"currentCycle"`
	LatestCompletedCycle *CycleSummary     `json:"latestCompletedCycle"`
	Opportunities        []OpportunityView `json:"opportunities"`
}

// Start запускает веб-панель на 127.0.0.1:8080. Внешние интерфейсы не открываются.
func Start(db *storage.DB, logger *slog.Logger) (*Server, error) {
	if db == nil {
		return nil, errors.New("SQLite не открыт")
	}
	if logger == nil {
		logger = slog.Default()
	}

	listener, err := net.Listen("tcp", DefaultAddress)
	if err != nil {
		return nil, err
	}

	server := &Server{
		httpServer: &http.Server{
			Addr:              DefaultAddress,
			Handler:           NewHandler(db.SQLDB()),
			ReadHeaderTimeout: 5 * time.Second,
			IdleTimeout:       30 * time.Second,
			MaxHeaderBytes:    1 << 20,
		},
		logger: logger,
	}
	go func() {
		if err := server.httpServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			server.logger.Error("локальная панель остановлена с ошибкой", "error", err)
		}
	}()

	server.logger.Info("локальная панель запущена", "url", DefaultURL, "listen", DefaultAddress)
	return server, nil
}

// Close корректно останавливает HTTP-сервер перед закрытием SQLite.
func (s *Server) Close() error {
	if s == nil || s.httpServer == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return s.httpServer.Shutdown(ctx)
}

// NewHandler возвращает HTTP-обработчик панели. SQL-пул используется только для чтения.
func NewHandler(db *sql.DB) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		data, err := assets.ReadFile("index.html")
		if err != nil {
			http.Error(w, "dashboard assets unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	})
	mux.HandleFunc("/api/dashboard", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if db == nil {
			writeJSONError(w, http.StatusServiceUnavailable, "SQLite is not available")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		data, err := readPayload(ctx, db)
		if err != nil {
			writeJSONError(w, http.StatusServiceUnavailable, "Не удалось прочитать данные SQLite: "+err.Error())
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(data)
	})
	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func readPayload(ctx context.Context, db *sql.DB) (Payload, error) {
	current, err := readCycle(ctx, db, `SELECT id, started_at, COALESCE(finished_at, ''), status,
		enabled_exchanges, succeeded_exchanges, failed_exchanges, markets_discovered,
		tickers_received, tickers_accepted, tickers_rejected, snapshots_saved,
		opportunities_detected, opportunities_saved, persistence_errors,
		COALESCE(min_spread_percent, ''), COALESCE(error, '')
		FROM scan_cycles ORDER BY julianday(started_at) DESC LIMIT 1`)
	if err != nil {
		return Payload{}, err
	}
	completed, err := readCycle(ctx, db, `SELECT id, started_at, COALESCE(finished_at, ''), status,
		enabled_exchanges, succeeded_exchanges, failed_exchanges, markets_discovered,
		tickers_received, tickers_accepted, tickers_rejected, snapshots_saved,
		opportunities_detected, opportunities_saved, persistence_errors,
		COALESCE(min_spread_percent, ''), COALESCE(error, '')
		FROM scan_cycles WHERE finished_at IS NOT NULL
		ORDER BY julianday(finished_at) DESC LIMIT 1`)
	if err != nil {
		return Payload{}, err
	}
	opportunities := make([]OpportunityView, 0)
	if completed != nil {
		opportunities, err = readOpportunities(ctx, db, completed.ID)
		if err != nil {
			return Payload{}, err
		}
	}
	return Payload{
		GeneratedAt: time.Now().UTC(),
		CurrentCycle: current,
		LatestCompletedCycle: completed,
		Opportunities: opportunities,
	}, nil
}

func readCycle(ctx context.Context, db *sql.DB, query string) (*CycleSummary, error) {
	var cycle CycleSummary
	err := db.QueryRowContext(ctx, query).Scan(
		&cycle.ID, &cycle.StartedAt, &cycle.FinishedAt, &cycle.Status,
		&cycle.EnabledExchanges, &cycle.SucceededExchanges, &cycle.FailedExchanges,
		&cycle.MarketsDiscovered, &cycle.TickersReceived, &cycle.TickersAccepted,
		&cycle.TickersRejected, &cycle.SnapshotsSaved, &cycle.OpportunitiesDetected,
		&cycle.OpportunitiesSaved, &cycle.PersistenceErrors, &cycle.MinSpreadPercent,
		&cycle.Error,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &cycle, nil
}

func readOpportunities(ctx context.Context, db *sql.DB, cycleID string) ([]OpportunityView, error) {
	rows, err := db.QueryContext(ctx, `SELECT
		o.id, o.timestamp, o.symbol, o.base_asset, o.quote_asset,
		o.buy_exchange, o.buy_market_type, o.buy_contract_type,
		COALESCE(NULLIF(o.buy_symbol, ''), o.symbol), o.buy_price,
		COALESCE(buy_snapshot.volume_24h, ''),
		o.sell_exchange, o.sell_market_type, o.sell_contract_type,
		COALESCE(NULLIF(o.sell_symbol, ''), o.symbol), o.sell_price,
		COALESCE(sell_snapshot.volume_24h, ''), o.spread_percent
		FROM opportunities o
		LEFT JOIN price_snapshots buy_snapshot ON buy_snapshot.id = o.buy_snapshot_id
		LEFT JOIN price_snapshots sell_snapshot ON sell_snapshot.id = o.sell_snapshot_id
		WHERE o.cycle_id = ?
		ORDER BY CAST(o.spread_percent AS REAL) DESC, julianday(o.timestamp) DESC, o.id DESC
		LIMIT ?`, cycleID, maxOpportunities)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]OpportunityView, 0)
	for rows.Next() {
		var item OpportunityView
		if err := rows.Scan(
			&item.ID, &item.Timestamp, &item.Symbol, &item.BaseAsset, &item.QuoteAsset,
			&item.BuyExchange, &item.BuyMarketType, &item.BuyContractType,
			&item.BuySymbol, &item.BuyPrice, &item.BuyVolume24h,
			&item.SellExchange, &item.SellMarketType, &item.SellContractType,
			&item.SellSymbol, &item.SellPrice, &item.SellVolume24h, &item.SpreadPercent,
		); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// SQLite хранит decimal-значения как текст; сортируем уже выбранные строки точно.
	sort.SliceStable(result, func(i, j int) bool {
		left, leftErr := decimal.NewFromString(result[i].SpreadPercent)
		right, rightErr := decimal.NewFromString(result[j].SpreadPercent)
		if leftErr == nil && rightErr == nil && !left.Equal(right) {
			return left.GreaterThan(right)
		}
		if result[i].Timestamp != result[j].Timestamp {
			return result[i].Timestamp > result[j].Timestamp
		}
		return result[i].ID > result[j].ID
	})
	return result, nil
}
