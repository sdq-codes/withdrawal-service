package repositories

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/sdq-codes/withdrawal-service/database"
	accountRepositories "github.com/sdq-codes/withdrawal-service/internal/apps/account/repositories"
	"github.com/sdq-codes/withdrawal-service/internal/apps/withdrawal/models"
)

func testWithdrawal(id, code, account string, cents int64) models.Withdrawal {
	return models.Withdrawal{
		ID: id, AccountID: account, Status: "authorised", AuthorisationCode: code,
		AmountUSDCents: cents, AmountUSD: "20.00", TargetCurrency: "EUR",
		ConvertedAmount: "18.40", ExchangeRate: "0.92", CreatedAt: time.Now().UTC(),
	}
}

func openSeeded(t *testing.T, balance int64) (*sql.DB, *WithdrawalRepository, *accountRepositories.AccountRepository) {
	t.Helper()
	db, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := database.Migrate(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	accounts := accountRepositories.NewAccountRepository(db)
	if err := accounts.SeedDemo(context.Background(), "acc_1", "token", balance); err != nil {
		t.Fatal(err)
	}
	return db, New(db), accounts
}

func assertBalanceAndCount(t *testing.T, repo *WithdrawalRepository, accounts *accountRepositories.AccountRepository, wantBalance int64, wantCount int) {
	t.Helper()
	balance, err := accounts.BalanceCents(context.Background(), "acc_1")
	if err != nil {
		t.Fatal(err)
	}
	if balance != wantBalance {
		t.Fatalf("balance = %d, want %d", balance, wantBalance)
	}
	count, err := repo.CountByAccount(context.Background(), "acc_1")
	if err != nil {
		t.Fatal(err)
	}
	if count != wantCount {
		t.Fatalf("withdrawal count = %d, want %d", count, wantCount)
	}
}

func TestAuthoriseWithdrawalIsAtomic(t *testing.T) {
	ctx := context.Background()
	_, repo, accounts := openSeeded(t, 10000)
	if err := repo.AuthoriseWithdrawal(ctx, testWithdrawal("wd_1", "AAAA-1111", "acc_1", 7500)); err != nil {
		t.Fatal(err)
	}
	if err := repo.AuthoriseWithdrawal(ctx, testWithdrawal("wd_2", "BBBB-2222", "acc_1", 3000)); !errors.Is(err, models.ErrInsufficientFunds) {
		t.Fatalf("error = %v, want insufficient funds", err)
	}
	assertBalanceAndCount(t, repo, accounts, 2500, 1)
}

func TestAuthoriseWithdrawalRollsBackDebitWhenInsertFails(t *testing.T) {
	ctx := context.Background()
	_, repo, accounts := openSeeded(t, 10000)
	if err := repo.AuthoriseWithdrawal(ctx, testWithdrawal("wd_1", "SAME-1111", "acc_1", 1000)); err != nil {
		t.Fatal(err)
	}
	if err := repo.AuthoriseWithdrawal(ctx, testWithdrawal("wd_2", "SAME-1111", "acc_1", 2000)); err == nil {
		t.Fatal("expected duplicate authorisation code insertion to fail")
	}
	assertBalanceAndCount(t, repo, accounts, 9000, 1)
}

func TestAuthoriseWithdrawalPreventsConcurrentOverdraft(t *testing.T) {
	ctx := context.Background()
	_, repo, accounts := openSeeded(t, 10000)

	const workers = 10
	var wg sync.WaitGroup
	var mu sync.Mutex
	succeeded, insufficient, unexpected := 0, 0, []error{}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := repo.AuthoriseWithdrawal(ctx, testWithdrawal(fmt.Sprintf("wd_%02d", i), fmt.Sprintf("CODE-%04d", i), "acc_1", 2000))
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				succeeded++
			} else if errors.Is(err, models.ErrInsufficientFunds) {
				insufficient++
			} else {
				unexpected = append(unexpected, err)
			}
		}(i)
	}
	wg.Wait()
	if len(unexpected) != 0 {
		t.Fatalf("unexpected errors: %v", unexpected)
	}
	if succeeded != 5 || insufficient != 5 {
		t.Fatalf("success=%d insufficient=%d, want 5 each", succeeded, insufficient)
	}
	assertBalanceAndCount(t, repo, accounts, 0, 5)
}

func TestDuplicateIdempotencyKeyRollsBackDebit(t *testing.T) {
	ctx := context.Background()
	_, repo, accounts := openSeeded(t, 10000)
	first := testWithdrawal("wd_1", "CODE-0001", "acc_1", 1000)
	first.IdempotencyKey, first.RequestHash = "key-1", "hash"
	if err := repo.AuthoriseWithdrawal(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := testWithdrawal("wd_2", "CODE-0002", "acc_1", 1000)
	second.IdempotencyKey, second.RequestHash = "key-1", "hash"
	if err := repo.AuthoriseWithdrawal(ctx, second); !errors.Is(err, models.ErrDuplicateIdempotencyKey) {
		t.Fatalf("error = %v, want ErrDuplicateIdempotencyKey", err)
	}
	assertBalanceAndCount(t, repo, accounts, 9000, 1)
	found, err := repo.FindByIdempotencyKey(ctx, "acc_1", "key-1")
	if err != nil {
		t.Fatal(err)
	}
	if found.ID != "wd_1" || found.AmountUSD != "10.00" || found.RequestHash != "hash" {
		t.Fatalf("found = %+v", found)
	}
	if _, err := repo.FindByIdempotencyKey(ctx, "acc_other", "key-1"); !errors.Is(err, models.ErrNotFound) {
		t.Fatalf("idempotency keys must be scoped per account, error = %v", err)
	}
}
