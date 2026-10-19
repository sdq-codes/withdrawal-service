package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	HTTPRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "withdrawal_http_requests_total",
		Help: "HTTP requests by method, route pattern and status code.",
	}, []string{"method", "route", "status"})

	HTTPDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "withdrawal_http_request_duration_seconds",
		Help:    "HTTP request latency by method and route pattern.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "route"})

	WithdrawalOutcomes = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "withdrawal_outcomes_total",
		Help: "Withdrawal requests by outcome: authorised, replayed, or the API error code.",
	}, []string{"outcome"})

	FXRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "withdrawal_fx_request_duration_seconds",
		Help:    "Exchange-rate provider latency by outcome.",
		Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
	}, []string{"outcome"})
)
