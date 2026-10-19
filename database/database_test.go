package database_test

import (
	"context"
	"io/fs"
	"path/filepath"
	"testing"
	"time"

	"github.com/sdq-codes/withdrawal-service/database"
	"github.com/sdq-codes/withdrawal-service/database/migrations"
	accountRepositories "github.com/sdq-codes/withdrawal-service/internal/apps/account/repositories"
	withdrawalModels "github.com/sdq-codes/withdrawal-service/internal/apps/withdrawal/models"
	withdrawalRepositories "github.com/sdq-codes/withdrawal-service/internal/apps/withdrawal/repositories"
	"github.com/sdq-codes/withdrawal-service/internal/helper/auth"
)

func TestMigrateIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	applied, err := database.Migrate(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) == 0 {
		t.Fatal("expected migrations to be applied to an empty database")
	}
	again, err := database.Migrate(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if len(again) != 0 {
		t.Fatalf("second Migrate applied %v, want nothing", again)
	}
	if v, err := database.Version(ctx, db); err != nil || v != applied[len(applied)-1].Version {
		t.Fatalf("version = %d, %v", v, err)
	}
}

func TestMigrationBackfillsLedgerForLegacyDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.db")
	raw, err := database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	// Schema and data as written by the original, unversioned service.
	initial, err := fs.ReadFile(migrations.SQL, "001_initial.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		string(initial),
		`INSERT INTO accounts VALUES ('acc_1', 7000, '2026-10-01T00:00:00Z')`,
		`INSERT INTO api_tokens(token_hash, account_id, created_at) VALUES ('` + auth.HashToken("token") + `', 'acc_1', '2026-10-01T00:00:00Z')`,
		`INSERT INTO withdrawals VALUES ('wd_a', 'acc_1', 'authorised', 'AAAA-1111', 2000, 'EUR', '18.40', '0.92', '2026-10-02T00:00:00Z')`,
		`INSERT INTO withdrawals VALUES ('wd_b', 'acc_1', 'authorised', 'BBBB-2222', 1000, 'USD', '10.00', '1', '2026-10-03T00:00:00Z')`,
	} {
		if _, err := raw.ExecContext(ctx, statement); err != nil {
			t.Fatal(err)
		}
	}
	raw.Close()

	db, err := database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := database.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	r, err := accountRepositories.NewAccountRepository(db).Reconcile(ctx, "acc_1")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Consistent || r.LedgerBalanceUSDCents != 7000 || r.Withdrawals != 2 {
		t.Fatalf("unexpected reconciliation after migration: %+v", r)
	}
	var lastBalance int64
	if err := db.QueryRowContext(ctx, `SELECT balance_after_usd_cents FROM ledger_entries WHERE withdrawal_id = 'wd_b'`).Scan(&lastBalance); err != nil {
		t.Fatal(err)
	}
	if lastBalance != 7000 {
		t.Fatalf("balance after last backfilled withdrawal = %d, want 7000", lastBalance)
	}
	if account, err := accountRepositories.NewTokenRepository(db).AccountIDForToken(ctx, auth.HashToken("token")); err != nil || account != "acc_1" {
		t.Fatalf("legacy token = %q, %v", account, err)
	}
	wd := withdrawalModels.Withdrawal{
		ID: "wd_c", AccountID: "acc_1", Status: "authorised", AuthorisationCode: "CCCC-3333",
		AmountUSDCents: 500, AmountUSD: "5.00", TargetCurrency: "USD", ConvertedAmount: "5.00", ExchangeRate: "1", CreatedAt: time.Now().UTC(),
	}
	if err := withdrawalRepositories.New(db).AuthoriseWithdrawal(ctx, wd); err != nil {
		t.Fatal(err)
	}
}
