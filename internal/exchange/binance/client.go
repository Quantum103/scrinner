package binance

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
	defaultSpotBaseURL        = "https://api.binance.com"
	defaultFuturesBaseURL     = "https://fapi.binance.com"
	defaultHTTPTimeout        = 15 * time.Second
	defaultMaxRetries         = 2
	maxConfiguredRetries      = 5
	defaultRetryBaseDelay     = 250 * time.Millisecond
	maxRetryDelay             = 5 * time.Second
	defaultMaxResponseSize    = 16 << 20
)

// ClientConfig содержит настройки публичного HTTP-клиента Binance.
type ClientConfig struct {
	SpotBaseURL     string
	FuturesBaseURL  string
	HTTPClient      *http.Client
	MaxRetries      int
	RetryBaseDelay  time.Duration
	MaxResponseSize int64
}

// APIError содержит контекст ошибки публичного API Binance.
type APIError struct {
	Exchange   string
	Operation  string
	Endpoint   string
	HTTPStatus int
	Code       int
	Message    string
	RetryAfter time.Duration
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

// Client выполняет только публичные GET-запросы к REST API Binance.
type Client struct {
	spotBaseURL     *url.URL
	futuresBaseURL  *url.URL
	httpClient      *http.Client
	maxRetries      int
	retryBaseDelay  time.Duration
	maxResponseSize int64
}

// DefaultClientConfig возвращает настройки клиента для обычного запуска.
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

// NewClient создаёт HTTP-клиент с проверенными адресами и ограниченными повторами.
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

// NewDefaultClient создаёт клиента для официальных публичных API Binance.
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
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("неподдерживаемая схема URL %q", raw)
	}
	if parsed.Host == "" {
		return nil, fmt.Errorf("URL %q не содержит имя узла", raw)
	}
	return parsed, nil
}

// getJSON выполняет ограниченный GET-запрос и возвращает время получения HTTP-ответа.
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
			return time.Time{}, fmt.Errorf("создать запрос Binance %s: %w", operation, err)
		}
		req.Header.Set("Accept", "application/json")

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
			return time.Time{}, fmt.Errorf(
				"выполнить запрос Binance %s: %w",
				operation,
				requestErr,
			)
		}

		receivedAt := time.Now().UTC()
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, c.maxResponseSize+1))
		closeErr := resp.Body.Close()
		if readErr != nil {
			if attempt < c.maxRetries && ctx.Err() == nil {
				if err := waitRetry(ctx, c.retryDelay(attempt)); err != nil {
					return time.Time{}, err
				}
				continue
			}
			return time.Time{}, fmt.Errorf("прочитать ответ Binance %s: %w", operation, readErr)
		}
		if closeErr != nil {
			// Ошибка Close не меняет уже прочитанное тело ответа.
		}
		if int64(len(body)) > c.maxResponseSize {
			return time.Time{}, fmt.Errorf(
				"ответ Binance %s превышает лимит %d байт",
				operation,
				c.maxResponseSize,
			)
		}

		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			apiErr := buildAPIError(operation, endpoint, resp.StatusCode, resp.Header, body)
			if attempt < c.maxRetries && apiErr.IsRetryable() {
				delay := apiErr.RetryAfter
				if delay <= 0 {
					delay = c.retryDelay(attempt)
				}
				if err := waitRetry(ctx, delay); err != nil {
					return time.Time{}, err
				}
				continue
			}
			return time.Time{}, apiErr
		}

		if err := json.Unmarshal(body, target); err != nil {
			return time.Time{}, fmt.Errorf(
				"разобрать JSON Binance %s: %w",
				operation,
				err,
			)
		}
		return receivedAt, nil
	}
}

// IsRetryable сообщает, допускает ли HTTP/API-ошибка ограниченный повтор запроса.
func (e *APIError) IsRetryable() bool {
	// HTTP 418 означает временный бан IP: повторные запросы не выполняются.
	return e.HTTPStatus == http.StatusTooManyRequests ||
		(e.HTTPStatus >= http.StatusInternalServerError && e.HTTPStatus <= 599)
}

func buildAPIError(operation, endpoint string, status int, headers http.Header, body []byte) *APIError {
	apiErr := &APIError{
		Exchange:   "binance",
		Operation:  operation,
		Endpoint:   endpoint,
		HTTPStatus: status,
		Message:    responseMessage(body),
		RetryAfter: parseRetryAfter(headers.Get("Retry-After"), time.Now()),
	}

	var payload apiErrorResponse
	if json.Unmarshal(body, &payload) == nil {
		apiErr.Code = payload.Code
		if strings.TrimSpace(payload.Msg) != "" {
			apiErr.Message = payload.Msg
		}
	}
	return apiErr
}

func responseMessage(body []byte) string {
	message := strings.Join(strings.Fields(string(body)), " ")
	if len(message) > 512 {
		message = message[:512]
	}
	if message == "" {
		return "пустой HTTP-ответ"
	}
	return message
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	if retryAt, err := http.ParseTime(value); err == nil {
		delay := retryAt.Sub(now)
		if delay > 0 {
			return delay
		}
	}
	return 0
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
