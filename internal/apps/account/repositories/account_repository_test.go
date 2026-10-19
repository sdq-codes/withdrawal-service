package repositories

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/sdq-codes/withdrawal-service/database"
	"github.com/sdq-codes/withdrawal-service/internal/apps/account/models"
	auditRepositories "github.com/sdq-codes/withdrawal-service/internal/apps/audit/repositories"
	withdrawalModels "github.com/sdq-codes/withdrawal-service/internal/apps/withdrawal/models"
	withdrawalRepositories "github.com/sdq-codes/withdrawal-service/internal/apps/withdrawal/repositories"
	"github.com/sdq-codes/withdrawal-service/internal/helper/auth"
)

func openSeeded(t *testing.T, balance int64) (*sql.DB, *AccountRepository) {
	t.Helper()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := database.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	accounts := NewAccountRepository(db)
	if err := accounts.SeedDemo(context.Background(), "acc_1", "token", balance); err != nil {
		t.Fatal(err)
	}
	return db, accounts
}

func testWithdrawal(id, code string, cents int64) withdrawalModels.Withdrawal {
	return withdrawalModels.Withdrawal{
		ID: id, AccountID: "acc_1", Status: "authorised", AuthorisationCode: code,
		AmountUSDCents: cents, AmountUSD: "20.00", TargetCurrency: "EUR",
		ConvertedAmount: "18.40", ExchangeRate: "0.92", CreatedAt: time.Now().UTC(),
	}
}

func TestLedgerReconcilesWithBalance(t *testing.T) {
	ctx := context.Background()
	db, accounts := openSeeded(t, 10000)
	withdrawals := withdrawalRepositories.New(db)
	for i, cents := range []int64{2500, 1250} {
		if err := withdrawals.AuthoriseWithdrawal(ctx, testWithdrawal(fmt.Sprintf("wd_%d", i), fmt.Sprintf("CODE-%04d", i), cents)); err != nil {
			t.Fatal(err)
		}
	}
	if err := withdrawals.AuthoriseWithdrawal(ctx, testWithdrawal("wd_big", "CODE-9999", 99999)); !errors.Is(err, withdrawalModels.ErrInsufficientFunds) {
		t.Fatalf("error = %v, want insufficient funds", err)
	}
	r, err := accounts.Reconcile(ctx, "acc_1")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Consistent || r.BalanceUSDCents != 6250 || r.LedgerBalanceUSDCents != 6250 || r.WithdrawnUSDCents != 3750 || r.Withdrawals != 2 {
		t.Fatalf("unexpected reconciliation: %+v", r)
	}

	events, err := auditRepositories.New(db).ListByAccount(ctx, "acc_1")
	if err != nil {
		t.Fatal(err)
	}
	var types []string
	for _, e := range events {
		types = append(types, e.EventType)
	}
	want := []string{"account.created", "token.issued", "withdrawal.authorised", "withdrawal.authorised", "withdrawal.declined"}
	if fmt.Sprint(types) != fmt.Sprint(want) {
		t.Fatalf("audit events = %v, want %v", types, want)
	}
}

func TestReconcileDetectsTampering(t *testing.T) {
	ctx := context.Background()
	db, accounts := openSeeded(t, 10000)
	if _, err := db.ExecContext(ctx, `UPDATE accounts SET balance_usd_cents = 50000 WHERE id = 'acc_1'`); err != nil {
		t.Fatal(err)
	}
	r, err := accounts.Reconcile(ctx, "acc_1")
	if err != nil {
		t.Fatal(err)
	}
	if r.Consistent {
		t.Fatalf("reconciliation should flag a balance that disagrees with the ledger: %+v", r)
	}
}

func TestReconcileUnknownAccount(t *testing.T) {
	_, accounts := openSeeded(t, 100)
	if _, err := accounts.Reconcile(context.Background(), "acc_missing"); !errors.Is(err, models.ErrAccountNotFound) {
		t.Fatalf("error = %v, want ErrAccountNotFound", err)
	}
}

func TestAccountIDForTokenRejectsUnknownToken(t *testing.T) {
	db, _ := openSeeded(t, 100)
	if _, err := NewTokenRepository(db).AccountIDForToken(context.Background(), auth.HashToken("wrong-token")); !errors.Is(err, models.ErrInvalidToken) {
		t.Fatalf("error = %v, want ErrInvalidToken", err)
	}
}

func TestTokenRotationExpiryAndRevocation(t *testing.T) {
	ctx := context.Background()
	db, _ := openSeeded(t, 100)
	tokens := NewTokenRepository(db)
	issued, err := tokens.RotateToken(ctx, auth.HashToken("token"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tokens.AccountIDForToken(ctx, auth.HashToken("token")); !errors.Is(err, models.ErrInvalidToken) {
		t.Fatalf("rotated-out token error = %v, want ErrInvalidToken", err)
	}
	if account, err := tokens.AccountIDForToken(ctx, auth.HashToken(issued.Token)); err != nil || account != "acc_1" {
		t.Fatalf("new token = %q, %v", account, err)
	}

	tokens.now = func() time.Time { return issued.ExpiresAt }
	if _, err := tokens.AccountIDForToken(ctx, auth.HashToken(issued.Token)); !errors.Is(err, models.ErrTokenExpired) {
		t.Fatalf("expired token error = %v, want ErrTokenExpired", err)
	}
	tokens.now = func() time.Time { return time.Now().UTC() }

	if err := tokens.RevokeToken(ctx, auth.HashToken(issued.Token)); err != nil {
		t.Fatal(err)
	}
	if _, err := tokens.AccountIDForToken(ctx, auth.HashToken(issued.Token)); !errors.Is(err, models.ErrInvalidToken) {
		t.Fatalf("revoked token error = %v, want ErrInvalidToken", err)
	}
	if err := tokens.RevokeToken(ctx, auth.HashToken(issued.Token)); !errors.Is(err, models.ErrInvalidToken) {
		t.Fatalf("second revoke error = %v, want ErrInvalidToken", err)
	}
}
