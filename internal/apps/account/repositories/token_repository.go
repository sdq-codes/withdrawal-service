package repositories

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/sdq-codes/withdrawal-service/database"
	"github.com/sdq-codes/withdrawal-service/internal/apps/account/models"
	auditModels "github.com/sdq-codes/withdrawal-service/internal/apps/audit/models"
	auditRepositories "github.com/sdq-codes/withdrawal-service/internal/apps/audit/repositories"
	"github.com/sdq-codes/withdrawal-service/internal/helper/auth"
)

type TokenRepository struct {
	db  *sql.DB
	now func() time.Time
}

func NewTokenRepository(db *sql.DB) *TokenRepository {
	return &TokenRepository{db: db, now: func() time.Time { return time.Now().UTC() }}
}

func (r *TokenRepository) AccountIDForToken(ctx context.Context, tokenHash string) (string, error) {
	var accountID string
	var expiresAt sql.NullString
	err := r.db.QueryRowContext(ctx, `SELECT account_id, expires_at FROM api_tokens WHERE token_hash = ? AND revoked_at IS NULL`, tokenHash).Scan(&accountID, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return "", models.ErrInvalidToken
	}
	if err != nil {
		return "", err
	}
	if expiresAt.Valid {
		expiry, err := time.Parse(database.TimeLayout, expiresAt.String)
		if err != nil {
			return "", fmt.Errorf("parse token expiry: %w", err)
		}
		if !r.now().Before(expiry) {
			return "", models.ErrTokenExpired
		}
	}
	return accountID, nil
}

// Only the hash is stored; the plaintext is returned once.
func (r *TokenRepository) IssueToken(ctx context.Context, accountID string, ttl time.Duration) (models.IssuedToken, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return models.IssuedToken{}, err
	}
	defer tx.Rollback()
	issued, err := r.issueToken(ctx, tx, accountID, ttl, "token.issued")
	if err != nil {
		return models.IssuedToken{}, err
	}
	return issued, tx.Commit()
}

func (r *TokenRepository) RotateToken(ctx context.Context, currentHash string, ttl time.Duration) (models.IssuedToken, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return models.IssuedToken{}, err
	}
	defer tx.Rollback()
	accountID, err := revokeToken(ctx, tx, currentHash, database.Timestamp(r.now()))
	if err != nil {
		return models.IssuedToken{}, err
	}
	issued, err := r.issueToken(ctx, tx, accountID, ttl, "token.rotated")
	if err != nil {
		return models.IssuedToken{}, err
	}
	return issued, tx.Commit()
}

func (r *TokenRepository) RevokeToken(ctx context.Context, tokenHash string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := database.Timestamp(r.now())
	accountID, err := revokeToken(ctx, tx, tokenHash, now)
	if err != nil {
		return err
	}
	if err := auditRepositories.Record(ctx, tx, auditModels.Event{AccountID: accountID, EventType: "token.revoked", Subject: auth.TokenRef(tokenHash), CreatedAt: now}); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *TokenRepository) issueToken(ctx context.Context, tx *sql.Tx, accountID string, ttl time.Duration, event string) (models.IssuedToken, error) {
	if ttl <= 0 {
		return models.IssuedToken{}, errors.New("token ttl must be positive")
	}
	token, err := auth.GenerateToken()
	if err != nil {
		return models.IssuedToken{}, err
	}
	now := r.now()
	expiresAt := now.Add(ttl)
	hash := auth.HashToken(token)
	if _, err := tx.ExecContext(ctx, `INSERT INTO api_tokens(token_hash, account_id, expires_at, created_at) VALUES(?,?,?,?)`,
		hash, accountID, database.Timestamp(expiresAt), database.Timestamp(now)); err != nil {
		return models.IssuedToken{}, err
	}
	if err := auditRepositories.Record(ctx, tx, auditModels.Event{AccountID: accountID, EventType: event, Subject: auth.TokenRef(hash),
		Detail: "expires_at=" + database.Timestamp(expiresAt), CreatedAt: database.Timestamp(now)}); err != nil {
		return models.IssuedToken{}, err
	}
	return models.IssuedToken{Token: token, ExpiresAt: expiresAt}, nil
}

func revokeToken(ctx context.Context, tx *sql.Tx, tokenHash, now string) (string, error) {
	var accountID string
	err := tx.QueryRowContext(ctx, `UPDATE api_tokens SET revoked_at = ? WHERE token_hash = ? AND revoked_at IS NULL RETURNING account_id`, now, tokenHash).Scan(&accountID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", models.ErrInvalidToken
	}
	return accountID, err
}
