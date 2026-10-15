package mexc

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
	defaultSpotBaseURL     = "https://api.mexc.com"
	defaultFuturesBaseURL  = "https://api.mexc.com"
	defaultHTTPTimeout     = 15 * time.Second
	defaultMaxRetries      = 2
	maxConfiguredRetries   = 5
	defaultRetryBaseDelay  = 250 * time.Millisecond
	maxRetryDelay          = 5 * time.Second
	defaultMaxResponseSize = 16 << 20
	maxRetryAfterWait      = 30 * time.Second
)

// ClientConfig содержит настройки публичного HTTP-клиента MEXC.
type ClientConfig struct {
	SpotBaseURL     string
	FuturesBaseURL  string
	HTTPClient      *http.Client
	MaxRetries      int
	RetryBaseDelay  time.Duration
	MaxResponseSize int64
}

// APIError содержит диагностическую информацию об ошибке HTTP или MEXC API.
type APIError struct {
	Exchange      string
	Operation     string
	Endpoint      string
	HTTPStatus    int
	Code          int64
	Message       string
	RetryAfter    time.Duration
	retryAfterSet bool
}

// Error возвращает диагностическое описание ошибки API.
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

// IsRetryable сообщает, допускает ли ошибка ограниченный повтор запроса.
func (e *APIError) IsRetryable() bool {
	return e.HTTPStatus == http.StatusTooManyRequests ||
		(e.HTTPStatus >= http.StatusInternalServerError && e.HTTPStatus <= 599)
}

// Client выполняет публичные GET-запросы к REST API MEXC Spot и Futures.
type Client struct {
	spotBaseURL     *url.URL
	futuresBaseURL  *url.URL
	httpClient      *http.Client
	maxRetries      int
	retryBaseDelay  time.Duration
	maxResponseSize int64
}

// DefaultClientConfig возвращает настройки для официального API MEXC.
func DefaultClientConfig() ClientConfig {
	return ClientConfig{
		SpotBaseURL:     defaultSpotBaseURL,
		FuturesBaseURL:  defaultFuturesBaseURL,
		HTTPClient:      &http.Client{Timeout: defaultHTTPTimeout},
		MaxRetries:      defaultMaxRetries,
		RetryBaseDelay:  defaultRetryBaseDelay,
		MaxResponseSize: defaultMaxResponseSize,
	}
}

// NewClient создаёт клиент с проверенными адресами и ограниченным числом повторов.
func NewClient(cfg ClientConfig) (*Client, error) {
	if cfg.SpotBaseURL == "" {
		cfg.SpotBaseURL = defaultSpotBaseURL
	}
	if cfg.FuturesBaseURL == "" {
		cfg.FuturesBaseURL = defaultFuturesBaseURL
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: defaultHTTPTimeout}
	}
	if cfg.MaxRetries < 0 || cfg.MaxRetries > maxConfiguredRetries {
		return nil, fmt.Errorf("MaxRetries должен быть в диапазоне от 0 до %d", maxConfiguredRetries)
	}
	if cfg.RetryBaseDelay <= 0 {
		cfg.RetryBaseDelay = defaultRetryBaseDelay
	}
	if cfg.MaxResponseSize <= 0 {
		cfg.MaxResponseSize = defaultMaxResponseSize
	}

	spotURL, err := validateBaseURL(cfg.SpotBaseURL)
	if err != nil {
		return nil, fmt.Errorf("Spot BaseURL: %w", err)
	}
	futuresURL, err := validateBaseURL(cfg.FuturesBaseURL)
	if err != nil {
		return nil, fmt.Errorf("Futures BaseURL: %w", err)
	}

	return &Client{
		spotBaseURL:     spotURL,
		futuresBaseURL:  futuresURL,
		httpClient:      cfg.HTTPClient,
		maxRetries:      cfg.MaxRetries,
		retryBaseDelay:  cfg.RetryBaseDelay,
		maxResponseSize: cfg.MaxResponseSize,
	}, nil
}

// NewDefaultClient создаёт клиент для официальных публичных endpoints MEXC.
func NewDefaultClient() *Client {
	client, err := NewClient(DefaultClientConfig())
	if err != nil {
		panic(err)
	}
	return client
}

func validateBaseURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("разобрать URL %q: %w", raw, err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("некорректный базовый URL %q", raw)
	}
	return parsed, nil
}

// getJSON выполняет GET-запрос, проверяет ответ API и возвращает локальное время получения.
func (c *Client) getJSON(
	ctx context.Context,
	futures bool,
	operation string,
	endpoint string,
	query url.Values,
	target any,
) (time.Time, error) {
	baseURL := c.spotBaseURL
	if futures {
		baseURL = c.futuresBaseURL
	}
	requestURL := *baseURL
	requestURL.Path = strings.TrimRight(baseURL.Path, "/") + endpoint
	requestURL.RawPath = ""
	requestURL.Fragment = ""
	if query != nil {
		requestURL.RawQuery = query.Encode()
	} else {
		requestURL.RawQuery = ""
	}

	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return time.Time{}, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
		if err != nil {
			return time.Time{}, fmt.Errorf("создать запрос MEXC %s: %w", operation, err)
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", "scrinner-market-scanner/1.0")

		resp, requestErr := c.httpClient.Do(req)
		if requestErr != nil {
			if ctx.Err() != nil {
				return time.Time{}, ctx.Err()
			}
			if attempt < c.maxRetries && isRetryableNetworkError(requestErr) {
				if err := waitRetry(ctx, c.retryDelay(attempt)); err != nil {
					return time.Time{}, err
				}
				continue
			}
			return time.Time{}, fmt.Errorf("выполнить запрос MEXC %s: %w", operation, requestErr)
		}

		body, readErr := io.ReadAll(io.LimitReader(resp.Body, c.maxResponseSize+1))
		_ = resp.Body.Close()
		if readErr != nil {
			if attempt < c.maxRetries && ctx.Err() == nil {
				if err := waitRetry(ctx, c.retryDelay(attempt)); err != nil {
					return time.Time{}, err
				}
				continue
			}
			return time.Time{}, fmt.Errorf("прочитать ответ MEXC %s: %w", operation, readErr)
		}
		receivedAt := time.Now().UTC()
		if int64(len(body)) > c.maxResponseSize {
			return time.Time{}, fmt.Errorf("ответ MEXC %s превышает лимит %d байт", operation, c.maxResponseSize)
		}

		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			apiErr := buildHTTPAPIError(operation, endpoint, resp.StatusCode, resp.Header, body)
			if attempt < c.maxRetries && apiErr.IsRetryable() {
				delay := c.retryDelay(attempt)
				if apiErr.retryAfterSet {
					delay = apiErr.RetryAfter
					// Не отправляем повтор раньше Retry-After; слишком большой срок означает,
					// что текущий проход завершается без новой попытки.
					if delay > maxRetryAfterWait {
						return time.Time{}, apiErr
					}
				}
				if err := waitRetry(ctx, delay); err != nil {
					return time.Time{}, err
				}
				continue
			}
			return time.Time{}, apiErr
		}

		if futures {
			if err := decodeFuturesEnvelope(body, target, operation, endpoint); err != nil {
				return time.Time{}, err
			}
			return receivedAt, nil
		}
		if err := checkSpotAPIError(body, operation, endpoint); err != nil {
			return time.Time{}, err
		}
		if err := json.Unmarshal(body, target); err != nil {
			return time.Time{}, fmt.Errorf("разобрать JSON MEXC %s: %w", operation, err)
		}
		return receivedAt, nil
	}
}

func decodeFuturesEnvelope(body []byte, target any, operation, endpoint string) error {
	var envelope futuresEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return fmt.Errorf("разобрать envelope MEXC Futures %s: %w", operation, err)
	}
	if envelope.Success == nil {
		return fmt.Errorf("MEXC Futures %s: ответ не содержит success", operation)
	}
	code, codePresent, err := parseIntegerRaw(envelope.Code)
	if err != nil {
		return fmt.Errorf("MEXC Futures %s: некорректный code: %w", operation, err)
	}
	message := strings.TrimSpace(envelope.Message)
	if message == "" {
		message = strings.TrimSpace(envelope.Msg)
	}
	if !*envelope.Success || (codePresent && code != 0) {
		return &APIError{
			Exchange:  "mexc",
			Operation: operation,
			Endpoint:  endpoint,
			Code:      code,
			Message:   nonEmptyMessage(message, "API вернул success=false"),
		}
	}
	if len(envelope.Data) == 0 || strings.TrimSpace(string(envelope.Data)) == "null" {
		return fmt.Errorf("MEXC Futures %s: ответ не содержит data", operation)
	}
	if err := json.Unmarshal(envelope.Data, target); err != nil {
		return fmt.Errorf("разобрать data MEXC Futures %s: %w", operation, err)
	}
	return nil
}

func checkSpotAPIError(body []byte, operation, endpoint string) error {
	var response spotAPIError
	if err := json.Unmarshal(body, &response); err != nil {
		return nil
	}
	code, codePresent, err := parseIntegerRaw(response.Code)
	if err != nil {
		return fmt.Errorf("MEXC Spot %s: некорректный code: %w", operation, err)
	}
	message := strings.TrimSpace(response.Message)
	if message == "" {
		message = strings.TrimSpace(response.Msg)
	}
	if (response.Success != nil && !*response.Success) ||
		(codePresent && code != 0) {
		return &APIError{
			Exchange:  "mexc",
			Operation: operation,
			Endpoint:  endpoint,
			Code:      code,
			Message:   nonEmptyMessage(message, "API вернул ошибку в HTTP 200"),
		}
	}
	return nil
}

func buildHTTPAPIError(operation, endpoint string, status int, headers http.Header, body []byte) *APIError {
	retryAfter, retryAfterSet := parseRetryAfter(headers.Get("Retry-After"), time.Now())
	result := &APIError{
		Exchange:      "mexc",
		Operation:     operation,
		Endpoint:      endpoint,
		HTTPStatus:    status,
		Message:       responseMessage(body),
		RetryAfter:    retryAfter,
		retryAfterSet: retryAfterSet,
	}

	var envelope futuresEnvelope
	if json.Unmarshal(body, &envelope) == nil {
		if code, ok, err := parseIntegerRaw(envelope.Code); err == nil && ok {
			result.Code = code
		}
		if strings.TrimSpace(envelope.Message) != "" {
			result.Message = strings.TrimSpace(envelope.Message)
		} else if strings.TrimSpace(envelope.Msg) != "" {
			result.Message = strings.TrimSpace(envelope.Msg)
		}
	}
	if result.Message == "" {
		result.Message = "пустой HTTP-ответ"
	}
	return result
}

func responseMessage(body []byte) string {
	message := strings.Join(strings.Fields(string(body)), " ")
	if len(message) > 512 {
		message = message[:512]
	}
	return message
}

func parseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds <= 0 {
			return 0, true
		}
		return time.Duration(seconds) * time.Second, true
	}
	if retryAt, err := http.ParseTime(value); err == nil {
		delay := retryAt.Sub(now)
		if delay > 0 {
			return delay, true
		}
		return 0, true
	}
	return 0, false
}

func (c *Client) retryDelay(attempt int) time.Duration {
	delay := c.retryBaseDelay
	for i := 0; i < attempt && delay < maxRetryDelay; i++ {
		delay *= 2
	}
	if delay > maxRetryDelay {
		return maxRetryDelay
	}
	return delay
}

func isRetryableNetworkError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var netErr net.Error
	return errors.As(err, &netErr) && (netErr.Timeout() || netErr.Temporary())
}

func waitRetry(ctx context.Context, delay time.Duration) error {
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

func parseIntegerRaw(raw json.RawMessage) (int64, bool, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return 0, false, nil
	}
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var value string
		if err := json.Unmarshal(raw, &value); err != nil {
			return 0, true, err
		}
		result, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		return result, true, err
	}
	result, err := strconv.ParseInt(trimmed, 10, 64)
	return result, true, err
}

func nonEmptyMessage(message, fallback string) string {
	if strings.TrimSpace(message) == "" {
		return fallback
	}
	return strings.TrimSpace(message)
}
