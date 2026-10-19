package repositories

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/sdq-codes/withdrawal-service/database"
	auditModels "github.com/sdq-codes/withdrawal-service/internal/apps/audit/models"
	auditRepositories "github.com/sdq-codes/withdrawal-service/internal/apps/audit/repositories"
	"github.com/sdq-codes/withdrawal-service/internal/apps/withdrawal/models"
	"github.com/sdq-codes/withdrawal-service/pkg/money"
)

var tracer = otel.Tracer("github.com/sdq-codes/withdrawal-service/internal/apps/withdrawal/repositories")

type WithdrawalRepository struct {
	db  *sql.DB
	now func() time.Time
}

func New(db *sql.DB) *WithdrawalRepository {
	return &WithdrawalRepository{db: db, now: func() time.Time { return time.Now().UTC() }}
}

// Debit, withdrawal, ledger entry and audit event commit in one transaction.
func (r *WithdrawalRepository) AuthoriseWithdrawal(ctx context.Context, wd models.Withdrawal) (err error) {
	ctx, span := tracer.Start(ctx, "storage.authorise_withdrawal")
	defer func() {
		if err != nil {
			span.SetStatus(codes.Error, err.Error())
		}
		span.End()
	}()

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := database.Timestamp(r.now())
	var balanceAfter int64
	err = tx.QueryRowContext(ctx, `UPDATE accounts
SET balance_usd_cents = balance_usd_cents - ?
WHERE id = ? AND balance_usd_cents >= ?
RETURNING balance_usd_cents`, wd.AmountUSDCents, wd.AccountID, wd.AmountUSDCents).Scan(&balanceAfter)
	if errors.Is(err, sql.ErrNoRows) {
		// Nothing was debited, so the decline can be audited and committed.
		declineErr := models.ErrInsufficientFunds
		var exists int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM accounts WHERE id = ?`, wd.AccountID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
			declineErr = models.ErrAccountNotFound
		} else if err != nil {
			return err
		}
		detail := fmt.Sprintf("reason=%s amount_usd=%s target_currency=%s", declineReason(declineErr), wd.AmountUSD, wd.TargetCurrency)
		if err := auditRepositories.Record(ctx, tx, auditModels.Event{AccountID: wd.AccountID, EventType: "withdrawal.declined", Subject: wd.ID, Detail: detail, CreatedAt: now}); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		return declineErr
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO withdrawals
(id, account_id, status, authorisation_code, amount_usd_cents, target_currency, converted_amount, exchange_rate, exchange_rate_date, idempotency_key, request_hash, created_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, wd.ID, wd.AccountID, wd.Status, wd.AuthorisationCode, wd.AmountUSDCents, wd.TargetCurrency, wd.ConvertedAmount, wd.ExchangeRate,
		database.NullIfEmpty(wd.ExchangeRateDate), database.NullIfEmpty(wd.IdempotencyKey), database.NullIfEmpty(wd.RequestHash), database.Timestamp(wd.CreatedAt))
	if err != nil {
		if isIdempotencyConflict(err) {
			return models.ErrDuplicateIdempotencyKey
		}
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO ledger_entries(account_id, entry_type, amount_usd_cents, balance_after_usd_cents, withdrawal_id, created_at) VALUES(?,?,?,?,?,?)`,
		wd.AccountID, "withdrawal", -wd.AmountUSDCents, balanceAfter, wd.ID, now); err != nil {
		return err
	}
	detail := fmt.Sprintf("amount_usd=%s target_currency=%s converted_amount=%s exchange_rate=%s", wd.AmountUSD, wd.TargetCurrency, wd.ConvertedAmount, wd.ExchangeRate)
	if err := auditRepositories.Record(ctx, tx, auditModels.Event{AccountID: wd.AccountID, EventType: "withdrawal.authorised", Subject: wd.ID, Detail: detail, CreatedAt: now}); err != nil {
		return err
	}
	return tx.Commit()
}

func declineReason(err error) string {
	if errors.Is(err, models.ErrAccountNotFound) {
		return "account_not_found"
	}
	return "insufficient_funds"
}

func isIdempotencyConflict(err error) bool {
	var sqliteErr *sqlite.Error
	return errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE &&
		strings.Contains(sqliteErr.Error(), "withdrawals.idempotency_key")
}

func (r *WithdrawalRepository) FindByIdempotencyKey(ctx context.Context, accountID, key string) (models.Withdrawal, error) {
	var wd models.Withdrawal
	var rateDate, requestHash sql.NullString
	var createdAt string
	err := r.db.QueryRowContext(ctx, `SELECT id, account_id, status, authorisation_code, amount_usd_cents, target_currency, converted_amount, exchange_rate, exchange_rate_date, request_hash, created_at
FROM withdrawals WHERE account_id = ? AND idempotency_key = ?`, accountID, key).Scan(
		&wd.ID, &wd.AccountID, &wd.Status, &wd.AuthorisationCode, &wd.AmountUSDCents, &wd.TargetCurrency,
		&wd.ConvertedAmount, &wd.ExchangeRate, &rateDate, &requestHash, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return models.Withdrawal{}, models.ErrNotFound
	}
	if err != nil {
		return models.Withdrawal{}, err
	}
	wd.CreatedAt, err = time.Parse(database.TimeLayout, createdAt)
	if err != nil {
		return models.Withdrawal{}, fmt.Errorf("parse withdrawal created_at: %w", err)
	}
	wd.AmountUSD = money.FormatCents(wd.AmountUSDCents)
	wd.ExchangeRateDate = rateDate.String
	wd.IdempotencyKey = key
	wd.RequestHash = requestHash.String
	return wd, nil
}

func (r *WithdrawalRepository) CountByAccount(ctx context.Context, accountID string) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM withdrawals WHERE account_id = ?`, accountID).Scan(&count)
	return count, err
}
