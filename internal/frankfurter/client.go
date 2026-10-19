package frankfurter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	"github.com/sdq-codes/withdrawal-service/internal/apps/withdrawal/models"
	"github.com/sdq-codes/withdrawal-service/internal/metrics"
)

// Two attempts at the per-attempt timeout fit inside the 5s request timeout.
const maxAttempts = 2

type Client struct {
	baseURL string
	client  *http.Client
	backoff time.Duration
}

// Each connection phase gets its own limit so a slow handshake fails fast and is retried.
func NewClient(baseURL string, timeout time.Duration) *Client {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = "https://api.frankfurter.dev/v2"
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}).DialContext
	transport.TLSHandshakeTimeout = timeout
	transport.ResponseHeaderTimeout = timeout
	transport.MaxIdleConnsPerHost = 16
	transport.IdleConnTimeout = 90 * time.Second
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: timeout, Transport: otelhttp.NewTransport(transport)},
		backoff: 100 * time.Millisecond,
	}
}

// Warm opens a connection at startup so the first withdrawal doesn't pay for DNS and TLS setup.
func (f *Client) Warm(ctx context.Context) error {
	_, err := f.GetRate(ctx, "USD", "EUR")
	return err
}

type rateResponse struct {
	Date string      `json:"date"`
	Rate json.Number `json:"rate"`
}

// Transport errors, 429 and 5xx are retried once; the request is a GET so retrying is safe.
func (f *Client) GetRate(ctx context.Context, baseCurrency, targetCurrency string) (rate models.Rate, err error) {
	start := time.Now()
	defer func() {
		outcome := "ok"
		switch {
		case errors.Is(err, models.ErrCurrencyNotSupported):
			outcome = "unsupported_currency"
		case err != nil:
			outcome = "error"
		}
		metrics.FXRequestDuration.WithLabelValues(outcome).Observe(time.Since(start).Seconds())
	}()

	endpoint := f.baseURL + "/rate/" + url.PathEscape(strings.ToLower(baseCurrency)) + "/" + url.PathEscape(strings.ToLower(targetCurrency))
	for attempt := 1; ; attempt++ {
		var retryable bool
		rate, retryable, err = f.getRate(ctx, endpoint)
		if err == nil || !retryable || attempt == maxAttempts || ctx.Err() != nil {
			if err != nil && attempt > 1 {
				err = fmt.Errorf("%w (after %d attempts)", err, attempt)
			}
			return rate, err
		}
		select {
		case <-ctx.Done():
			return models.Rate{}, fmt.Errorf("%w: %v", models.ErrExchangeRateUnavailable, ctx.Err())
		case <-time.After(f.backoff):
		}
	}
}

func (f *Client) getRate(ctx context.Context, endpoint string) (rate models.Rate, retryable bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return models.Rate{}, false, fmt.Errorf("%w: build request: %v", models.ErrExchangeRateUnavailable, err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := f.client.Do(req)
	if err != nil {
		return models.Rate{}, true, fmt.Errorf("%w: %v", models.ErrExchangeRateUnavailable, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusUnprocessableEntity || resp.StatusCode == http.StatusBadRequest:
		return models.Rate{}, false, fmt.Errorf("%w: provider returned status %d", models.ErrCurrencyNotSupported, resp.StatusCode)
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500:
		return models.Rate{}, true, fmt.Errorf("%w: provider returned status %d", models.ErrExchangeRateUnavailable, resp.StatusCode)
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return models.Rate{}, false, fmt.Errorf("%w: provider returned status %d", models.ErrExchangeRateUnavailable, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return models.Rate{}, true, fmt.Errorf("%w: read response: %v", models.ErrExchangeRateUnavailable, err)
	}
	var payload rateResponse
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.UseNumber()
	if err := dec.Decode(&payload); err != nil || payload.Rate == "" {
		return models.Rate{}, false, fmt.Errorf("%w: invalid response body", models.ErrExchangeRateUnavailable)
	}
	rateText := string(payload.Rate)
	value, ok := new(big.Rat).SetString(rateText)
	if !ok || value.Sign() <= 0 {
		return models.Rate{}, false, fmt.Errorf("%w: invalid rate %q", models.ErrExchangeRateUnavailable, rateText)
	}
	date, err := time.Parse(time.DateOnly, payload.Date)
	if err != nil {
		return models.Rate{}, false, fmt.Errorf("%w: invalid rate date %q", models.ErrExchangeRateUnavailable, payload.Date)
	}
	return models.Rate{Value: rateText, Date: date}, false, nil
}
