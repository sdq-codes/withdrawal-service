package models

import (
	"errors"
	"time"
)

var (
	ErrInvalidAmount           = errors.New("amount must be a positive USD decimal with at most two fractional digits")
	ErrInvalidCurrency         = errors.New("target_currency must be a supported ISO 4217 currency code")
	ErrInvalidIdempotencyKey   = errors.New("Idempotency-Key must be 1-128 characters of letters, digits, '-', '_', '.' or ':'")
	ErrAccountNotFound         = errors.New("account not found")
	ErrInsufficientFunds       = errors.New("insufficient funds")
	ErrExchangeRateUnavailable = errors.New("exchange-rate provider unavailable")
	ErrExchangeRateStale       = errors.New("exchange rate is older than the configured maximum age")
	ErrCurrencyNotSupported    = errors.New("currency is not supported by the exchange-rate provider")
	ErrIdempotencyKeyReused    = errors.New("idempotency key was already used with a different request")
	// A concurrent request committed the same idempotency key first.
	ErrDuplicateIdempotencyKey = errors.New("idempotency key already recorded")
	ErrNotFound                = errors.New("withdrawal not found")
)

type Withdrawal struct {
	ID                string    `json:"id"`
	AccountID         string    `json:"account_id"`
	Status            string    `json:"status"`
	AuthorisationCode string    `json:"authorisation_code"`
	AmountUSDCents    int64     `json:"-"`
	AmountUSD         string    `json:"amount_usd"`
	TargetCurrency    string    `json:"target_currency"`
	ConvertedAmount   string    `json:"converted_amount"`
	ExchangeRate      string    `json:"exchange_rate"`
	ExchangeRateDate  string    `json:"exchange_rate_date"`
	CreatedAt         time.Time `json:"created_at"`
	IdempotencyKey    string    `json:"-"`
	RequestHash       string    `json:"-"`
}

type Rate struct {
	Value string
	Date  time.Time
}
