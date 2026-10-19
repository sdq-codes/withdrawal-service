package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

const DefaultDemoBearerToken = "demo-token-please-change"

var (
	cfg *Config
	mu  sync.Mutex
)

type Config struct {
	Env         string
	HTTPServer  HTTPServer
	Log         Log
	Database    Database
	Demo        Demo
	Frankfurter Frankfurter
	Token       Token
}

type HTTPServer struct {
	Port        string
	MetricsPort string
}

type Log struct {
	Level string
}

type Database struct {
	Path string
}

type Demo struct {
	AccountID    string
	BearerToken  string
	BalanceCents int64
}

type Frankfurter struct {
	BaseURL    string
	Timeout    time.Duration
	MaxRateAge time.Duration
}

type Token struct {
	TTL time.Duration
}

func GetConfig() *Config {
	mu.Lock()
	defer mu.Unlock()
	return cfg
}

func Load() (*Config, error) {
	balanceCents, err := parseBalance(env("DEMO_BALANCE_USD", "1000.00"))
	if err != nil {
		return nil, err
	}
	tokenTTL, err := durationEnv("TOKEN_TTL", 30*24*time.Hour)
	if err != nil {
		return nil, err
	}
	maxRateAge, err := durationEnv("FX_MAX_RATE_AGE", 96*time.Hour)
	if err != nil {
		return nil, err
	}
	loaded := &Config{
		Env:        env("APP_ENV", "development"),
		HTTPServer: HTTPServer{Port: env("PORT", "8080"), MetricsPort: env("METRICS_PORT", "9090")},
		Log:        Log{Level: env("LOG_LEVEL", "info")},
		Database:   Database{Path: env("DATABASE_PATH", "withdrawals.db")},
		Demo: Demo{
			AccountID:    env("DEMO_ACCOUNT_ID", "acc_demo"),
			BearerToken:  env("DEMO_BEARER_TOKEN", DefaultDemoBearerToken),
			BalanceCents: balanceCents,
		},
		Frankfurter: Frankfurter{
			BaseURL: env("FRANKFURTER_BASE_URL", "https://api.frankfurter.dev/v2"),
			// Per attempt. The client retries once and must finish within the 5s request timeout.
			Timeout:    2 * time.Second,
			MaxRateAge: maxRateAge,
		},
		Token: Token{TTL: tokenTTL},
	}
	mu.Lock()
	cfg = loaded
	mu.Unlock()
	return loaded, nil
}

// The default demo token is public, so it must not be seeded outside development.
func (c *Config) ValidateForServe() error {
	if c.Env != "development" && c.Demo.BearerToken == DefaultDemoBearerToken {
		return fmt.Errorf("DEMO_BEARER_TOKEN must be set when APP_ENV is %q: the default demo token is public", c.Env)
	}
	return nil
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func durationEnv(key string, fallback time.Duration) (time.Duration, error) {
	raw := env(key, "")
	if raw == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%s must be a positive Go duration such as 96h", key)
	}
	return d, nil
}

func parseBalance(raw string) (int64, error) {
	// Parsed by hand to avoid floating point.
	if raw == "" {
		return 0, errors.New("DEMO_BALANCE_USD must not be empty")
	}
	var whole, fraction string
	parts := strings.Split(raw, ".")
	if len(parts) > 2 || parts[0] == "" {
		return 0, errors.New("DEMO_BALANCE_USD must be a decimal USD amount")
	}
	whole = parts[0]
	if len(parts) == 2 {
		fraction = parts[1]
	}
	if len(fraction) > 2 {
		return 0, errors.New("DEMO_BALANCE_USD must have at most two decimal places")
	}
	for _, r := range whole + fraction {
		if r < '0' || r > '9' {
			return 0, errors.New("DEMO_BALANCE_USD must be a non-negative decimal USD amount")
		}
	}
	for len(fraction) < 2 {
		fraction += "0"
	}
	var dollars int64
	for _, r := range whole {
		dollars = dollars*10 + int64(r-'0')
		if dollars > 9_000_000_000_000_000 {
			return 0, errors.New("DEMO_BALANCE_USD is too large")
		}
	}
	var cents int64
	if fraction != "" {
		cents = int64(fraction[0]-'0')*10 + int64(fraction[1]-'0')
	}
	return dollars*100 + cents, nil
}
