package repositories

import (
	"context"
	"database/sql"

	"github.com/sdq-codes/withdrawal-service/database"
	"github.com/sdq-codes/withdrawal-service/internal/apps/audit/models"
)

func Record(ctx context.Context, tx *sql.Tx, e models.Event) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO audit_events(account_id, event_type, subject, detail, created_at) VALUES(?,?,?,?,?)`,
		e.AccountID, e.EventType, database.NullIfEmpty(e.Subject), database.NullIfEmpty(e.Detail), e.CreatedAt)
	return err
}

type AuditRepository struct{ db *sql.DB }

func New(db *sql.DB) *AuditRepository { return &AuditRepository{db: db} }

func (r *AuditRepository) ListByAccount(ctx context.Context, accountID string) ([]models.Event, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT account_id, event_type, COALESCE(subject, ''), COALESCE(detail, ''), created_at FROM audit_events WHERE account_id = ? ORDER BY id`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []models.Event
	for rows.Next() {
		var e models.Event
		if err := rows.Scan(&e.AccountID, &e.EventType, &e.Subject, &e.Detail, &e.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}
