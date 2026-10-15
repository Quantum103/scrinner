package bybit

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
	defaultBaseURL        = "https://api.bybit.com"
	defaultHTTPTimeout    = 15 * time.Second
	defaultMaxRetries     = 2
	defaultRetryBaseDelay = 250 * time.Millisecond
	defaultMaxResponse    = 8 << 20
)

// ClientConfig содержит настройки публичного HTTP-клиента Bybit.
type ClientConfig struct {
	BaseURL         string
	HTTPClient      *http.Client
	MaxRetries      int
	RetryBaseDelay  time.Duration
	MaxResponseSize int64
}

// DefaultClientConfig возвращает безопасные настройки клиента для обычного запуска.
func DefaultClientConfig() ClientConfig {
	return ClientConfig{
		BaseURL:         defaultBaseURL,
		HTTPClient:      &http.Client{Timeout: defaultHTTPTimeout},
		MaxRetries:      defaultMaxRetries,
		RetryBaseDelay:  defaultRetryBaseDelay,
		MaxResponseSize: defaultMaxResponse,
	}
}

// APIError содержит контекст ошибки публичного API Bybit.
type APIError struct {
	Exchange   string
	Operation  string
	Endpoint   string
	HTTPStatus int
	Code       int
	Message    string
	RetryAfter time.Duration
}

// Error возвращает понятное текстовое описание ошибки API.
func (e *APIError) Error() string {
	return fmt.Sprintf(
		"exchange=%s operation=%s endpoint=%s http_status=%d code=%d message=%s",
		e.Exchange,
		e.Operation,
		e.Endpoint,
		e.HTTPStatus,
		e.Code,
		e.Message,
	)
}

// IsRetryable определяет, имеет ли смысл повторить запрос.
func (e *APIError) IsRetryable() bool {
	return e.HTTPStatus == http.StatusTooManyRequests ||
		(e.HTTPStatus >= 500 && e.HTTPStatus <= 599) ||
		e.Code == 10006
}

// Client выполняет публичные REST-запросы Bybit V5.
type Client struct {
	baseURL         *url.URL
	httpClient      *http.Client
	maxRetries      int
	retryBaseDelay  time.Duration
	maxResponseSize int64
}

// NewClient создаёт клиент с указанными настройками.
func NewClient(cfg ClientConfig) (*Client, error) {
	if cfg.BaseURL == "" {
		cfg.BaseURL = defaultBaseURL
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: defaultHTTPTimeout}
	}
	if cfg.MaxRetries < 0 {
		return nil, errors.New("MaxRetries не может быть отрицательным")
	}
	if cfg.RetryBaseDelay <= 0 {
		cfg.RetryBaseDelay = defaultRetryBaseDelay
	}
	if cfg.MaxResponseSize <= 0 {
		cfg.MaxResponseSize = defaultMaxResponse
	}

	u, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("разобрать BaseURL %q: %w", cfg.BaseURL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("неподдерживаемая схема BaseURL %q", cfg.BaseURL)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("BaseURL %q не содержит host", cfg.BaseURL)
	}

	return &Client{
		baseURL:         u,
		httpClient:      cfg.HTTPClient,
		maxRetries:      cfg.MaxRetries,
		retryBaseDelay:  cfg.RetryBaseDelay,
		maxResponseSize: cfg.MaxResponseSize,
	}, nil
}

// NewDefaultClient создаёт клиент с настройками по умолчанию.
func NewDefaultClient() *Client {
	client, err := NewClient(DefaultClientConfig())
	if err != nil {
		panic(fmt.Sprintf("создать стандартный Bybit client: %v", err))
	}
	return client
}

// getJSON выполняет GET-запрос и декодирует успешный ответ в dst.
func (c *Client) getJSON(ctx context.Context, operation, endpoint string, query url.Values, dst any) (apiEnvelope, error) {
	var envelope apiEnvelope

	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		envelope = apiEnvelope{}
		err := c.getJSONAttempt(ctx, operation, endpoint, query, &envelope, dst)
		if err == nil {
			return envelope, nil
		}

		if ctx.Err() != nil {
			return apiEnvelope{}, ctx.Err()
		}
		if !isRetryableError(err) || attempt == c.maxRetries {
			return apiEnvelope{}, err
		}

		delay := c.retryDelay(err, attempt)
		if err := waitContext(ctx, delay); err != nil {
			return apiEnvelope{}, err
		}
	}

	return apiEnvelope{}, errors.New("внутренняя ошибка цикла retry")
}

// getJSONAttempt выполняет одну попытку HTTP-запроса.
func (c *Client) getJSONAttempt(ctx context.Context, operation, endpoint string, query url.Values, envelope *apiEnvelope, dst any) error {
	reqURL := *c.baseURL
	reqURL.Path = strings.TrimRight(c.baseURL.Path, "/") + endpoint
	reqURL.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return &APIError{
			Exchange:  "bybit",
			Operation: operation,
			Endpoint:  endpoint,
			Message:   err.Error(),
		}
	}
	request, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("exchange=bybit operation=%s endpoint=%s выполнить HTTP-запрос: %w", operation, endpoint, err)
	}
	defer request.Body.Close()

	body, err := io.ReadAll(io.LimitReader(request.Body, c.maxResponseSize+1))
	if err != nil {
		return fmt.Errorf("exchange=bybit operation=%s endpoint=%s прочитать ответ: %w", operation, endpoint, err)
	}
	if int64(len(body)) > c.maxResponseSize {
		return &APIError{
			Exchange:   "bybit",
			Operation:  operation,
			Endpoint:   endpoint,
			HTTPStatus: request.StatusCode,
			Message:    "размер HTTP-ответа превышает установленный лимит",
		}
	}

	retryAfter := retryAfterFromHeaders(request.Header)
	if request.StatusCode < 200 || request.StatusCode >= 300 {
		return &APIError{
			Exchange:   "bybit",
			Operation:  operation,
			Endpoint:   endpoint,
			HTTPStatus: request.StatusCode,
			Message:    compactBody(body),
			RetryAfter: retryAfter,
		}
	}

	if err := json.Unmarshal(body, envelope); err != nil {
		return fmt.Errorf("exchange=bybit operation=%s endpoint=%s разобрать JSON: %w", operation, endpoint, err)
	}

	if envelope.RetCode == nil {
		return &APIError{
			Exchange:   "bybit",
			Operation:  operation,
			Endpoint:   endpoint,
			HTTPStatus: request.StatusCode,
			Message:    "ответ не содержит retCode",
			RetryAfter: retryAfter,
		}
	}
	if strings.TrimSpace(envelope.RetMsg) == "" {
		return &APIError{
			Exchange:   "bybit",
			Operation:  operation,
			Endpoint:   endpoint,
			HTTPStatus: request.StatusCode,
			Code:       *envelope.RetCode,
			Message:    "ответ не содержит retMsg",
			RetryAfter: retryAfter,
		}
	}
	if *envelope.RetCode != 0 {
		return &APIError{
			Exchange:   "bybit",
			Operation:  operation,
			Endpoint:   endpoint,
			HTTPStatus: request.StatusCode,
			Code:       *envelope.RetCode,
			Message:    envelope.RetMsg,
			RetryAfter: retryAfter,
		}
	}

	if len(envelope.Result) == 0 || string(envelope.Result) == "null" {
		return &APIError{
			Exchange:   "bybit",
			Operation:  operation,
			Endpoint:   endpoint,
			HTTPStatus: request.StatusCode,
			Code:       *envelope.RetCode,
			Message:    "ответ не содержит result",
		}
	}

	if dst != nil {
		if err := json.Unmarshal(envelope.Result, dst); err != nil {
			return fmt.Errorf("exchange=bybit operation=%s endpoint=%s разобрать result: %w", operation, endpoint, err)
		}
	}

	return nil
}

// retryDelay вычисляет небольшую задержку между ограниченными попытками.
func (c *Client) retryDelay(err error, attempt int) time.Duration {
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.RetryAfter > 0 {
		if apiErr.RetryAfter > 5*time.Second {
			return 5 * time.Second
		}
		return apiErr.RetryAfter
	}

	delay := c.retryBaseDelay
	for i := 0; i < attempt; i++ {
		if delay >= 5*time.Second {
			return 5 * time.Second
		}
		delay *= 2
	}
	if delay > 5*time.Second {
		return 5 * time.Second
	}
	return delay
}

// retryAfterFromHeaders учитывает стандартный Retry-After и заголовок Bybit reset.
func retryAfterFromHeaders(header http.Header) time.Duration {
	if raw := strings.TrimSpace(header.Get("Retry-After")); raw != "" {
		if seconds, err := strconv.Atoi(raw); err == nil && seconds >= 0 {
			return time.Duration(seconds) * time.Second
		}
	}

	if raw := strings.TrimSpace(header.Get("X-Bapi-Limit-Reset-Timestamp")); raw != "" {
		if millis, err := strconv.ParseInt(raw, 10, 64); err == nil && millis > 0 {
			reset := time.UnixMilli(millis)
			delay := time.Until(reset)
			if delay > 0 {
				return delay
			}
		}
	}

	return 0
}

// isRetryableError разрешает повторять только временные ошибки сети/API.
func isRetryableError(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.IsRetryable()
	}

	var netErr net.Error
	return errors.As(err, &netErr)
}

// waitContext ждёт задержку, не игнорируя отмену контекста.
func waitContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// compactBody превращает небольшой текст ошибки API в одну строку.
func compactBody(body []byte) string {
	text := strings.TrimSpace(string(body))
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.ReplaceAll(text, "\r", " ")
	if len(text) > 512 {
		text = text[:512]
	}
	if text == "" {
		return "пустой HTTP-ответ"
	}
	return text
}
