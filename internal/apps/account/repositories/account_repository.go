package repositories

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sdq-codes/withdrawal-service/database"
	"github.com/sdq-codes/withdrawal-service/internal/apps/account/models"
	auditModels "github.com/sdq-codes/withdrawal-service/internal/apps/audit/models"
	auditRepositories "github.com/sdq-codes/withdrawal-service/internal/apps/audit/repositories"
	"github.com/sdq-codes/withdrawal-service/internal/helper/auth"
)

type AccountRepository struct {
	db  *sql.DB
	now func() time.Time
}

func NewAccountRepository(db *sql.DB) *AccountRepository {
	return &AccountRepository{db: db, now: func() time.Time { return time.Now().UTC() }}
}

func (r *AccountRepository) SeedDemo(ctx context.Context, accountID, token string, balanceCents int64) error {
	if strings.TrimSpace(accountID) == "" || strings.TrimSpace(token) == "" {
		return fmt.Errorf("demo account id and token must not be empty")
	}
	now := database.Timestamp(r.now())
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO accounts(id, balance_usd_cents, created_at) VALUES(?,?,?)`, accountID, balanceCents, now)
	if err != nil {
		return err
	}
	if created, err := result.RowsAffected(); err != nil {
		return err
	} else if created == 1 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO ledger_entries(account_id, entry_type, amount_usd_cents, balance_after_usd_cents, created_at) VALUES(?,?,?,?,?)`,
			accountID, "opening_balance", balanceCents, balanceCents, now); err != nil {
			return err
		}
		if err := auditRepositories.Record(ctx, tx, auditModels.Event{AccountID: accountID, EventType: "account.created", Subject: accountID, Detail: "seeded demo account", CreatedAt: now}); err != nil {
			return err
		}
	}
	hash := auth.HashToken(token)
	result, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO api_tokens(token_hash, account_id, created_at) VALUES(?,?,?)`, hash, accountID, now)
	if err != nil {
		return err
	}
	if created, err := result.RowsAffected(); err != nil {
		return err
	} else if created == 1 {
		if err := auditRepositories.Record(ctx, tx, auditModels.Event{AccountID: accountID, EventType: "token.issued", Subject: auth.TokenRef(hash), Detail: "seeded demo token without expiry", CreatedAt: now}); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *AccountRepository) Reconcile(ctx context.Context, accountID string) (models.Reconciliation, error) {
	rec := models.Reconciliation{AccountID: accountID}
	err := r.db.QueryRowContext(ctx, `SELECT a.balance_usd_cents,
  (SELECT COALESCE(SUM(amount_usd_cents), 0) FROM ledger_entries WHERE account_id = a.id),
  (SELECT COALESCE(-SUM(amount_usd_cents), 0) FROM ledger_entries WHERE account_id = a.id AND entry_type = 'withdrawal'),
  (SELECT COALESCE(SUM(amount_usd_cents), 0) FROM withdrawals WHERE account_id = a.id),
  (SELECT COUNT(*) FROM withdrawals WHERE account_id = a.id),
  (SELECT COUNT(*) FROM withdrawals w WHERE w.account_id = a.id
     AND NOT EXISTS (SELECT 1 FROM ledger_entries l WHERE l.withdrawal_id = w.id AND l.amount_usd_cents = -w.amount_usd_cents))
FROM accounts a WHERE a.id = ?`, accountID).Scan(
		&rec.BalanceUSDCents, &rec.LedgerBalanceUSDCents, &rec.LedgerWithdrawnCents, &rec.WithdrawnUSDCents, &rec.Withdrawals, &rec.UnledgeredWithdrawals)
	if errors.Is(err, sql.ErrNoRows) {
		return models.Reconciliation{}, models.ErrAccountNotFound
	}
	if err != nil {
		return models.Reconciliation{}, err
	}
	rec.Consistent = rec.BalanceUSDCents == rec.LedgerBalanceUSDCents &&
		rec.WithdrawnUSDCents == rec.LedgerWithdrawnCents &&
		rec.UnledgeredWithdrawals == 0
	return rec, nil
}

func (r *AccountRepository) ListIDs(ctx context.Context) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id FROM accounts ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (r *AccountRepository) BalanceCents(ctx context.Context, accountID string) (int64, error) {
	var balance int64
	err := r.db.QueryRowContext(ctx, `SELECT balance_usd_cents FROM accounts WHERE id = ?`, accountID).Scan(&balance)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, models.ErrAccountNotFound
	}
	return balance, err
}
