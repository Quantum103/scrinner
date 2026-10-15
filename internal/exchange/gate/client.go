package gate

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
	defaultBaseURL        = "https://api.gateio.ws"
	defaultHTTPTimeout    = 15 * time.Second
	defaultMaxRetries     = 2
	maxConfiguredRetries  = 5
	defaultRetryBaseDelay = 250 * time.Millisecond
	maxRetryDelay         = 5 * time.Second
	maxRetryAfterWait     = 30 * time.Second
	defaultMaxResponse    = 16 << 20
)

// ClientConfig задаёт параметры публичного HTTP-клиента Gate.
type ClientConfig struct {
	BaseURL         string
	HTTPClient      *http.Client
	MaxRetries      int
	RetryBaseDelay  time.Duration
	MaxResponseSize int64
}

// APIError описывает HTTP- или прикладную ошибку Gate API.
type APIError struct {
	Operation     string
	Endpoint      string
	HTTPStatus    int
	Label         string
	Message       string
	RetryAfter    time.Duration
	retryAfterSet bool
}

// Error формирует диагностическое сообщение об ошибке Gate.
func (e *APIError) Error() string {
	return fmt.Sprintf(
		"биржа=gate операция=%s endpoint=%s HTTP=%d label=%s сообщение=%s",
		e.Operation, e.Endpoint, e.HTTPStatus, e.Label, e.Message,
	)
}

// IsRetryable определяет, допускает ли ошибка ограниченный повтор запроса.
func (e *APIError) IsRetryable() bool {
	return e.HTTPStatus == http.StatusTooManyRequests ||
		(e.HTTPStatus >= http.StatusInternalServerError && e.HTTPStatus <= 599)
}

// Client выполняет публичные GET-запросы к Gate API v4.
type Client struct {
	baseURL         *url.URL
	httpClient      *http.Client
	maxRetries      int
	retryBaseDelay  time.Duration
	maxResponseSize int64
}

// DefaultClientConfig возвращает параметры официального публичного API Gate.
func DefaultClientConfig() ClientConfig {
	return ClientConfig{
		BaseURL:         defaultBaseURL,
		HTTPClient:      &http.Client{Timeout: defaultHTTPTimeout},
		MaxRetries:      defaultMaxRetries,
		RetryBaseDelay:  defaultRetryBaseDelay,
		MaxResponseSize: defaultMaxResponse,
	}
}

// NewClient создаёт HTTP-клиент с проверенным адресом и ограниченными повторами.
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

// NewDefaultClient создаёт клиент официального публичного API Gate.
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
		return nil, fmt.Errorf("разобрать базовый URL Gate %q: %w", raw, err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("некорректный базовый URL Gate %q", raw)
	}
	return parsed, nil
}

// getJSON выполняет публичный GET и не принимает HTTP-ошибки за успешный JSON-массив.
func (c *Client) getJSON(
	ctx context.Context,
	operation string,
	endpoint string,
	target any,
) (time.Time, error) {
	requestURL := *c.baseURL
	requestURL.Path = strings.TrimRight(c.baseURL.Path, "/") + endpoint
	requestURL.RawPath = ""
	requestURL.RawQuery = ""
	requestURL.Fragment = ""

	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return time.Time{}, err
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
		if err != nil {
			return time.Time{}, fmt.Errorf("создать запрос Gate %s: %w", operation, err)
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
			return time.Time{}, fmt.Errorf("выполнить запрос Gate %s: %w", operation, requestErr)
		}

		body, readErr := io.ReadAll(io.LimitReader(response.Body, c.maxResponseSize+1))
		_ = response.Body.Close()
		if readErr != nil {
			if ctx.Err() != nil {
				return time.Time{}, ctx.Err()
			}
			return time.Time{}, fmt.Errorf("прочитать ответ Gate %s: %w", operation, readErr)
		}
		if int64(len(body)) > c.maxResponseSize {
			return time.Time{}, fmt.Errorf("ответ Gate %s превышает лимит %d байт", operation, c.maxResponseSize)
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

		if err := checkAPIErrorBody(operation, endpoint, response.StatusCode, body); err != nil {
			return time.Time{}, err
		}
		if len(strings.TrimSpace(string(body))) == 0 || strings.TrimSpace(string(body)) == "null" {
			return time.Time{}, fmt.Errorf("Gate %s вернул пустой JSON-ответ", operation)
		}
		if err := json.Unmarshal(body, target); err != nil {
			return time.Time{}, fmt.Errorf("разобрать JSON Gate %s: %w", operation, err)
		}
		return receivedAt, nil
	}
}

func checkAPIErrorBody(operation, endpoint string, status int, body []byte) error {
	trimmed := strings.TrimSpace(string(body))
	if !strings.HasPrefix(trimmed, "{") {
		return nil
	}
	var payload apiErrorResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return fmt.Errorf("разобрать объект Gate %s: %w", operation, err)
	}
	if strings.TrimSpace(payload.Label) != "" || strings.TrimSpace(payload.Message) != "" {
		return &APIError{
			Operation: operation, Endpoint: endpoint, HTTPStatus: status,
			Label:   strings.TrimSpace(payload.Label),
			Message: nonEmptyMessage(payload.Message, "Gate вернул ошибку без описания"),
		}
	}
	return fmt.Errorf("Gate %s вернул объект вместо ожидаемого массива", operation)
}

func buildHTTPAPIError(operation, endpoint string, status int, headers http.Header, body []byte) *APIError {
	delay, delaySet := parseRetryAfter(headers.Get("Retry-After"), time.Now())
	result := &APIError{
		Operation: operation, Endpoint: endpoint, HTTPStatus: status,
		Message: responseMessage(body), RetryAfter: delay, retryAfterSet: delaySet,
	}
	var payload apiErrorResponse
	if json.Unmarshal(body, &payload) == nil {
		result.Label = strings.TrimSpace(payload.Label)
		if strings.TrimSpace(payload.Message) != "" {
			result.Message = strings.TrimSpace(payload.Message)
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

func parseDecimal(raw json.RawMessage) (decimalValue string, present bool, err error) {
	value := strings.TrimSpace(string(raw))
	if value == "" || value == "null" {
		return "", false, nil
	}
	if strings.HasPrefix(value, "\"") {
		var decoded string
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return "", true, err
		}
		value = strings.TrimSpace(decoded)
	}
	if value == "" {
		return "", false, nil
	}
	return value, true, nil
}

func parseInt64Raw(raw json.RawMessage) (int64, bool, error) {
	value, present, err := parseDecimal(raw)
	if err != nil || !present {
		return 0, present, err
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, true, err
	}
	return parsed, true, nil
}

func nonEmptyMessage(message, fallback string) string {
	if strings.TrimSpace(message) == "" {
		return fallback
	}
	return strings.TrimSpace(message)
}
