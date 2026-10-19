package bitget

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultBaseURL        = "https://api.bitget.com"
	defaultHTTPTimeout    = 15 * time.Second
	defaultMaxRetries     = 2
	maxConfiguredRetries  = 5
	defaultRetryBaseDelay = 250 * time.Millisecond
	maxRetryDelay         = 5 * time.Second
	maxRetryAfterWait     = 30 * time.Second
	defaultMaxResponse    = 16 << 20

	instrumentsEndpoint = "/api/v3/market/instruments"
	tickersEndpoint     = "/api/v3/market/tickers"
	spotCategory        = "SPOT"
	futuresCategory     = "USDT-FUTURES"
	usdtAsset           = "USDT"
)

// ClientConfig задаёт параметры HTTP-клиента публичных рыночных данных Bitget.
type ClientConfig struct {
	BaseURL         string
	HTTPClient      *http.Client
	MaxRetries      int
	RetryBaseDelay  time.Duration
	MaxResponseSize int64
}

// APIError описывает HTTP- или прикладную ошибку Bitget API.
type APIError struct {
	Operation  string
	Endpoint   string
	HTTPStatus int
	Code       string
	Message    string
	RetryAfter time.Duration
}

// Error формирует сообщение, пригодное для журналирования ошибки Bitget.
func (e *APIError) Error() string {
	return fmt.Sprintf("биржа=bitget операция=%s endpoint=%s HTTP=%d code=%s msg=%s", e.Operation, e.Endpoint, e.HTTPStatus, e.Code, e.Message)
}

// IsRetryable показывает, допустим ли ограниченный повтор запроса после этой ошибки.
func (e *APIError) IsRetryable() bool {
	return e.HTTPStatus == http.StatusTooManyRequests || e.HTTPStatus >= 500 && e.HTTPStatus <= 599
}

// Client выполняет только публичные GET-запросы без ключей и подписи.
type Client struct {
	baseURL         *url.URL
	httpClient      *http.Client
	maxRetries      int
	retryBaseDelay  time.Duration
	maxResponseSize int64
}

// DefaultClientConfig возвращает безопасные настройки публичного клиента Bitget.
func DefaultClientConfig() ClientConfig {
	return ClientConfig{
		BaseURL:         defaultBaseURL,
		HTTPClient:      &http.Client{Timeout: defaultHTTPTimeout},
		MaxRetries:      defaultMaxRetries,
		RetryBaseDelay:  defaultRetryBaseDelay,
		MaxResponseSize: defaultMaxResponse,
	}
}

// NewClient создаёт HTTP-клиент и проверяет базовый URL до первого запроса.
func NewClient(cfg ClientConfig) (*Client, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = defaultBaseURL
	}
	parsed, err := url.Parse(strings.TrimSpace(cfg.BaseURL))
	if err != nil || parsed == nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" && parsed.Path != "/" {
		return nil, fmt.Errorf("некорректный базовый URL Bitget: %q", cfg.BaseURL)
	}
	parsed.Path = ""
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: defaultHTTPTimeout}
	}
	if cfg.MaxRetries < 0 {
		return nil, fmt.Errorf("число повторов Bitget не может быть отрицательным")
	}
	if cfg.MaxRetries > maxConfiguredRetries {
		return nil, fmt.Errorf("число повторов Bitget не должно превышать %d", maxConfiguredRetries)
	}
	if cfg.RetryBaseDelay <= 0 {
		cfg.RetryBaseDelay = defaultRetryBaseDelay
	}
	if cfg.MaxResponseSize <= 0 {
		cfg.MaxResponseSize = defaultMaxResponse
	}
	return &Client{
		baseURL: parsed, httpClient: cfg.HTTPClient, maxRetries: cfg.MaxRetries,
		retryBaseDelay: cfg.RetryBaseDelay, maxResponseSize: cfg.MaxResponseSize,
	}, nil
}

// NewDefaultClient создаёт клиент Bitget с настройками по умолчанию.
func NewDefaultClient() *Client {
	client, err := NewClient(DefaultClientConfig())
	if err != nil {
		panic(err)
	}
	return client
}

// getJSON получает категорийный ответ V3, проверяет code и сохраняет сырые данные без float64.
func (c *Client) getJSON(ctx context.Context, operation, endpoint, category string, target any) (time.Time, error) {
	requestURL := *c.baseURL
	requestURL.Path = endpoint
	query := requestURL.Query()
	query.Set("category", category)
	requestURL.RawQuery = query.Encode()

	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return time.Time{}, err
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
		if err != nil {
			return time.Time{}, fmt.Errorf("создать запрос Bitget %s: %w", endpoint, err)
		}
		request.Header.Set("Accept", "application/json")
		response, requestErr := c.httpClient.Do(request)
		if requestErr != nil {
			lastErr = fmt.Errorf("HTTP-запрос Bitget %s: %w", endpoint, requestErr)
			if ctx.Err() != nil {
				return time.Time{}, ctx.Err()
			}
			if attempt >= c.maxRetries || !isRetryableNetworkError(requestErr) {
				return time.Time{}, lastErr
			}
			if err := c.waitRetry(ctx, retryDelay(attempt, c.retryBaseDelay, 0)); err != nil {
				return time.Time{}, fmt.Errorf("ожидание повтора Bitget %s: %w", endpoint, err)
			}
			continue
		}

		body, readErr := io.ReadAll(io.LimitReader(response.Body, c.maxResponseSize+1))
		_ = response.Body.Close()
		if readErr != nil {
			lastErr = fmt.Errorf("прочитать ответ Bitget %s: %w", endpoint, readErr)
			if ctx.Err() != nil {
				return time.Time{}, ctx.Err()
			}
			if attempt >= c.maxRetries || !isRetryableNetworkError(readErr) {
				return time.Time{}, lastErr
			}
			if err := c.waitRetry(ctx, retryDelay(attempt, c.retryBaseDelay, 0)); err != nil {
				return time.Time{}, fmt.Errorf("ожидание повтора Bitget %s: %w", endpoint, err)
			}
			continue
		}
		if int64(len(body)) > c.maxResponseSize {
			return time.Time{}, fmt.Errorf("ответ Bitget %s превышает лимит %d байт", endpoint, c.maxResponseSize)
		}

		if response.StatusCode < 200 || response.StatusCode > 299 {
			apiErr := decodeAPIError(operation, endpoint, response.StatusCode, response.Header.Get("Retry-After"), body)
			lastErr = apiErr
			if !apiErr.IsRetryable() || attempt >= c.maxRetries {
				return time.Time{}, apiErr
			}
			if apiErr.RetryAfter > maxRetryAfterWait {
				return time.Time{}, fmt.Errorf("Bitget вернул Retry-After=%s, превышающий разрешённое ожидание: %w", apiErr.RetryAfter, apiErr)
			}
			if err := c.waitRetry(ctx, retryDelay(attempt, c.retryBaseDelay, apiErr.RetryAfter)); err != nil {
				return time.Time{}, fmt.Errorf("ожидание повтора Bitget %s: %w", endpoint, err)
			}
			continue
		}

		var envelope apiEnvelope
		if err := json.Unmarshal(body, &envelope); err != nil {
			return time.Time{}, fmt.Errorf("разобрать JSON-оболочку Bitget %s: %w", endpoint, err)
		}
		if envelope.Code != "00000" {
			return time.Time{}, &APIError{
				Operation: operation, Endpoint: endpoint, HTTPStatus: response.StatusCode,
				Code: envelope.Code, Message: envelope.Msg,
			}
		}
		if len(envelope.Data) == 0 || strings.EqualFold(strings.TrimSpace(string(envelope.Data)), "null") {
			return time.Time{}, fmt.Errorf("Bitget %s вернул пустое поле data", endpoint)
		}
		if err := json.Unmarshal(envelope.Data, target); err != nil {
			return time.Time{}, fmt.Errorf("разобрать data Bitget %s: %w", endpoint, err)
		}
		return time.Now().UTC(), nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("запрос Bitget %s завершился без ответа", endpoint)
	}
	return time.Time{}, lastErr
}

func decodeAPIError(operation, endpoint string, status int, retryAfter string, body []byte) *APIError {
	result := &APIError{Operation: operation, Endpoint: endpoint, HTTPStatus: status}
	var envelope apiEnvelope
	if json.Unmarshal(body, &envelope) == nil {
		result.Code = envelope.Code
		result.Message = envelope.Msg
	}
	if result.Message == "" {
		text := strings.TrimSpace(string(body))
		if len(text) > 512 {
			text = text[:512]
		}
		result.Message = text
	}
	if status == http.StatusTooManyRequests {
		result.RetryAfter = parseRetryAfter(retryAfter, time.Now())
	}
	return result
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if date, err := http.ParseTime(value); err == nil && date.After(now) {
		return date.Sub(now)
	}
	return 0
}

func retryDelay(attempt int, base, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		return retryAfter
	}
	delay := base
	for i := 0; i < attempt && delay < maxRetryDelay; i++ {
		delay *= 2
	}
	if delay > maxRetryDelay {
		return maxRetryDelay
	}
	return delay
}

func isRetryableNetworkError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	// Ошибки соединения, DNS и сброса сокета могут не реализовывать net.Error.
	return true
}

func (c *Client) waitRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
