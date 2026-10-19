package models

import (
	"errors"
	"time"
)

var (
	ErrAccountNotFound = errors.New("account not found")
	ErrInvalidToken    = errors.New("invalid token")
	ErrTokenExpired    = errors.New("token expired")
)

type Reconciliation struct {
	AccountID             string
	BalanceUSDCents       int64
	LedgerBalanceUSDCents int64
	WithdrawnUSDCents     int64
	LedgerWithdrawnCents  int64
	Withdrawals           int64
	UnledgeredWithdrawals int64
	Consistent            bool
}

type IssuedToken struct {
	Token     string
	ExpiresAt time.Time
}
