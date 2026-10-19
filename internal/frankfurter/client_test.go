package frankfurter

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sdq-codes/withdrawal-service/internal/apps/withdrawal/models"
)

func TestFrankfurterParsesRate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/rate/usd/eur" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"date":"2026-10-16","base":"USD","quote":"EUR","rate":0.9234}`))
	}))
	defer server.Close()
	client := NewClient(server.URL, time.Second)
	rate, err := client.GetRate(context.Background(), "USD", "EUR")
	if err != nil {
		t.Fatal(err)
	}
	if rate.Value != "0.9234" || rate.Date.Format(time.DateOnly) != "2026-10-16" {
		t.Fatalf("rate = %+v", rate)
	}
}

func TestFrankfurterRejectsResponseWithoutDate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"base":"USD","quote":"EUR","rate":0.9234}`))
	}))
	defer server.Close()
	_, err := NewClient(server.URL, time.Second).GetRate(context.Background(), "USD", "EUR")
	if !errors.Is(err, models.ErrExchangeRateUnavailable) {
		t.Fatalf("error = %v, want ErrExchangeRateUnavailable", err)
	}
}

func TestFrankfurterMapsUnsupportedCurrency(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"invalid currency"}`, http.StatusNotFound)
	}))
	defer server.Close()
	_, err := NewClient(server.URL, time.Second).GetRate(context.Background(), "USD", "ZZZ")
	if !errors.Is(err, models.ErrCurrencyNotSupported) {
		t.Fatalf("error = %v", err)
	}
}

func TestFrankfurterMapsServerFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "down", http.StatusServiceUnavailable) }))
	defer server.Close()
	_, err := NewClient(server.URL, time.Second).GetRate(context.Background(), "USD", "EUR")
	if !errors.Is(err, models.ErrExchangeRateUnavailable) {
		t.Fatalf("error = %v", err)
	}
}

func TestFrankfurterRetriesTransientFailureOnce(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"date":"2026-10-16","rate":0.9234}`))
	}))
	defer server.Close()
	rate, err := NewClient(server.URL, time.Second).GetRate(context.Background(), "USD", "EUR")
	if err != nil {
		t.Fatal(err)
	}
	if rate.Value != "0.9234" || calls.Load() != 2 {
		t.Fatalf("rate = %+v, calls = %d", rate, calls.Load())
	}
}

func TestFrankfurterTimesOutSlowProviderAndReportsCause(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer close(release)

	start := time.Now()
	_, err := NewClient(server.URL, 100*time.Millisecond).GetRate(context.Background(), "USD", "EUR")
	if !errors.Is(err, models.ErrExchangeRateUnavailable) {
		t.Fatalf("error = %v, want ErrExchangeRateUnavailable", err)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "timeout") || !strings.Contains(err.Error(), "after 2 attempts") {
		t.Fatalf("error %q does not report the timeout and attempts", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", calls.Load())
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("lookup took %v; per-attempt timeout not applied", elapsed)
	}
}

func TestFrankfurterDoesNotRetryUnsupportedCurrency(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, `{"message":"invalid currency"}`, http.StatusNotFound)
	}))
	defer server.Close()
	_, err := NewClient(server.URL, time.Second).GetRate(context.Background(), "USD", "ZZZ")
	if !errors.Is(err, models.ErrCurrencyNotSupported) || calls.Load() != 1 {
		t.Fatalf("error = %v, calls = %d", err, calls.Load())
	}
}
