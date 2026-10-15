package okx

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
	defaultBaseURL        = "https://openapi.okx.com"
	defaultHTTPTimeout    = 15 * time.Second
	defaultMaxRetries     = 2
	maxConfiguredRetries  = 5
	defaultRetryBaseDelay = 250 * time.Millisecond
	maxRetryDelay         = 5 * time.Second
	maxRetryAfterWait     = 30 * time.Second
	defaultMaxResponse    = 16 << 20
)

// ClientConfig содержит настройки публичного HTTP-клиента OKX.
type ClientConfig struct {
	BaseURL         string
	HTTPClient      *http.Client
	MaxRetries      int
	RetryBaseDelay  time.Duration
	MaxResponseSize int64
}

// APIError содержит диагностические сведения об ошибке HTTP или API OKX.
type APIError struct {
	Operation      string
	Endpoint       string
	HTTPStatus     int
	Code           string
	Message        string
	RetryAfter     time.Duration
	retryAfterSet  bool
}

// Error возвращает диагностическое описание ошибки.
func (e *APIError) Error() string {
	return fmt.Sprintf(
		"биржа=okx операция=%s endpoint=%s HTTP=%d code=%s сообщение=%s",
		e.Operation,
		e.Endpoint,
		e.HTTPStatus,
		e.Code,
		e.Message,
	)
}

// IsRetryable сообщает, можно ли повторить запрос после временного HTTP-сбоя.
func (e *APIError) IsRetryable() bool {
	return e.HTTPStatus == http.StatusTooManyRequests ||
		(e.HTTPStatus >= http.StatusInternalServerError && e.HTTPStatus <= 599)
}

// Client выполняет публичные GET-запросы к REST API OKX.
type Client struct {
	baseURL         *url.URL
	httpClient      *http.Client
	maxRetries      int
	retryBaseDelay  time.Duration
	maxResponseSize int64
}

// DefaultClientConfig возвращает настройки официального глобального API OKX.
func DefaultClientConfig() ClientConfig {
	return ClientConfig{
		BaseURL:         defaultBaseURL,
		HTTPClient:      &http.Client{Timeout: defaultHTTPTimeout},
		MaxRetries:      defaultMaxRetries,
		RetryBaseDelay:  defaultRetryBaseDelay,
		MaxResponseSize: defaultMaxResponse,
	}
}

// NewClient создаёт клиент с проверенным базовым адресом и ограниченными повторами.
func NewClient(cfg ClientConfig) (*Client, error) {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = defaultBaseURL
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
		cfg.MaxResponseSize = defaultMaxResponse
	}

	baseURL, err := validateBaseURL(cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	return &Client{
		baseURL:         baseURL,
		httpClient:      cfg.HTTPClient,
		maxRetries:      cfg.MaxRetries,
		retryBaseDelay:  cfg.RetryBaseDelay,
		maxResponseSize: cfg.MaxResponseSize,
	}, nil
}

// NewDefaultClient создаёт клиент для официального публичного API OKX.
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
		return nil, fmt.Errorf("разобрать базовый URL OKX %q: %w", raw, err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("некорректный базовый URL OKX %q", raw)
	}
	return parsed, nil
}

// getJSON выполняет GET-запрос и проверяет HTTP-статус и прикладной code OKX.
func (c *Client) getJSON(
	ctx context.Context,
	operation string,
	endpoint string,
	query url.Values,
	target any,
) (time.Time, error) {
	requestURL := *c.baseURL
	requestURL.Path = strings.TrimRight(c.baseURL.Path, "/") + endpoint
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
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
		if err != nil {
			return time.Time{}, fmt.Errorf("создать запрос OKX %s: %w", operation, err)
		}
		request.Header.Set("Accept", "application/json")
		request.Header.Set("User-Agent", "scrinner-market-scanner/1.0")

		response, requestErr := c.httpClient.Do(request)
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
			return time.Time{}, fmt.Errorf("выполнить запрос OKX %s: %w", operation, requestErr)
		}

		body, readErr := io.ReadAll(io.LimitReader(response.Body, c.maxResponseSize+1))
		_ = response.Body.Close()
		if readErr != nil {
			if ctx.Err() != nil {
				return time.Time{}, ctx.Err()
			}
			return time.Time{}, fmt.Errorf("прочитать ответ OKX %s: %w", operation, readErr)
		}
		if int64(len(body)) > c.maxResponseSize {
			return time.Time{}, fmt.Errorf("ответ OKX %s превышает лимит %d байт", operation, c.maxResponseSize)
		}
		receivedAt := time.Now().UTC()

		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			apiErr := buildHTTPAPIError(operation, endpoint, response.StatusCode, response.Header, body)
			if attempt < c.maxRetries && apiErr.IsRetryable() {
				delay := c.retryDelay(attempt)
				if apiErr.retryAfterSet {
					delay = apiErr.RetryAfter
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

		var envelope apiResponse
		if err := json.Unmarshal(body, &envelope); err != nil {
			return time.Time{}, fmt.Errorf("разобрать JSON OKX %s: %w", operation, err)
		}
		code, present, err := parseRawString(envelope.Code)
		if err != nil || !present {
			return time.Time{}, fmt.Errorf("ответ OKX %s не содержит корректное поле code", operation)
		}
		if code != "0" {
			return time.Time{}, &APIError{
				Operation:  operation,
				Endpoint:   endpoint,
				HTTPStatus: response.StatusCode,
				Code:       code,
				Message:    nonEmptyMessage(envelope.Msg, "OKX вернул ошибку без описания"),
			}
		}
		if len(envelope.Data) == 0 || strings.TrimSpace(string(envelope.Data)) == "" ||
			strings.TrimSpace(string(envelope.Data)) == "null" {
			return time.Time{}, fmt.Errorf("успешный ответ OKX %s не содержит data", operation)
		}
		if err := json.Unmarshal(envelope.Data, target); err != nil {
			return time.Time{}, fmt.Errorf("разобрать data OKX %s: %w", operation, err)
		}
		return receivedAt, nil
	}
}

func buildHTTPAPIError(operation, endpoint string, status int, headers http.Header, body []byte) *APIError {
	retryAfter, retryAfterSet := parseRetryAfter(headers.Get("Retry-After"), time.Now())
	result := &APIError{
		Operation:     operation,
		Endpoint:      endpoint,
		HTTPStatus:    status,
		Message:       responseMessage(body),
		RetryAfter:    retryAfter,
		retryAfterSet: retryAfterSet,
	}
	var envelope apiResponse
	if json.Unmarshal(body, &envelope) == nil {
		if code, present, err := parseRawString(envelope.Code); err == nil && present {
			result.Code = code
		}
		if strings.TrimSpace(envelope.Msg) != "" {
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
		if seconds > int64(maxRetryAfterWait/time.Second) {
			return maxRetryAfterWait + time.Second, true
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
	var networkError net.Error
	return errors.As(err, &networkError) && (networkError.Timeout() || networkError.Temporary())
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

func parseRawString(raw json.RawMessage) (string, bool, error) {
	value := strings.TrimSpace(string(raw))
	if value == "" || value == "null" {
		return "", false, nil
	}
	if strings.HasPrefix(value, """) {
		var decoded string
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return "", true, err
		}
		return strings.TrimSpace(decoded), true, nil
	}
	return value, true, nil
}

func nonEmptyMessage(message, fallback string) string {
	if strings.TrimSpace(message) == "" {
		return fallback
	}
	return strings.TrimSpace(message)
}
