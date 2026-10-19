package repositories_test

import (
	"context"
	"testing"

	"github.com/sdq-codes/withdrawal-service/database"
	accountRepositories "github.com/sdq-codes/withdrawal-service/internal/apps/account/repositories"
)

func TestLedgerAndAuditAreAppendOnly(t *testing.T) {
	ctx := context.Background()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := database.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := accountRepositories.NewAccountRepository(db).SeedDemo(ctx, "acc_1", "token", 10000); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`UPDATE ledger_entries SET amount_usd_cents = 1`,
		`DELETE FROM ledger_entries`,
		`UPDATE audit_events SET detail = 'x'`,
		`DELETE FROM audit_events`,
	} {
		if _, err := db.ExecContext(ctx, statement); err == nil {
			t.Errorf("%q succeeded, want append-only rejection", statement)
		}
	}
}
