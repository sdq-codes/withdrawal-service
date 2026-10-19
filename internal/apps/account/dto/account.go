package dto

import (
	"time"

	"github.com/sdq-codes/withdrawal-service/internal/apps/account/models"
	"github.com/sdq-codes/withdrawal-service/pkg/money"
)

type ReconciliationResponse struct {
	AccountID             string `json:"account_id"`
	Withdrawals           int64  `json:"withdrawal_count"`
	UnledgeredWithdrawals int64  `json:"unledgered_withdrawal_count"`
	Consistent            bool   `json:"consistent"`
	BalanceUSD            string `json:"balance_usd"`
	LedgerBalanceUSD      string `json:"ledger_balance_usd"`
	WithdrawnUSD          string `json:"withdrawn_usd"`
	LedgerWithdrawnUSD    string `json:"ledger_withdrawn_usd"`
}

func NewReconciliationResponse(rec models.Reconciliation) ReconciliationResponse {
	return ReconciliationResponse{
		AccountID:             rec.AccountID,
		Withdrawals:           rec.Withdrawals,
		UnledgeredWithdrawals: rec.UnledgeredWithdrawals,
		Consistent:            rec.Consistent,
		BalanceUSD:            money.FormatSignedCents(rec.BalanceUSDCents),
		LedgerBalanceUSD:      money.FormatSignedCents(rec.LedgerBalanceUSDCents),
		WithdrawnUSD:          money.FormatSignedCents(rec.WithdrawnUSDCents),
		LedgerWithdrawnUSD:    money.FormatSignedCents(rec.LedgerWithdrawnCents),
	}
}

type TokenResponse struct {
	Token     string `json:"token"`
	TokenType string `json:"token_type"`
	ExpiresAt string `json:"expires_at"`
}

func NewTokenResponse(issued models.IssuedToken) TokenResponse {
	return TokenResponse{Token: issued.Token, TokenType: "Bearer", ExpiresAt: issued.ExpiresAt.UTC().Format(time.RFC3339)}
}
