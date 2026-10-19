-- Idempotency keys and FX rate dates on withdrawals.
ALTER TABLE withdrawals ADD COLUMN idempotency_key TEXT NULL;

ALTER TABLE withdrawals ADD COLUMN request_hash TEXT NULL;

ALTER TABLE withdrawals ADD COLUMN exchange_rate_date TEXT NULL;

CREATE UNIQUE INDEX idx_withdrawals_idempotency ON withdrawals(account_id, idempotency_key) WHERE idempotency_key IS NOT NULL;

-- Token lifecycle: optional expiry. NULL means the token does not expire.
ALTER TABLE api_tokens ADD COLUMN expires_at TEXT NULL;

CREATE INDEX idx_api_tokens_account ON api_tokens(account_id);

-- Append-only ledger: every balance change is recorded with the
-- resulting balance, so balances can be reconciled against history.
CREATE TABLE ledger_entries (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  account_id TEXT NOT NULL REFERENCES accounts(id),
  entry_type TEXT NOT NULL CHECK (entry_type IN ('opening_balance', 'withdrawal')),
  amount_usd_cents INTEGER NOT NULL,
  balance_after_usd_cents INTEGER NOT NULL CHECK (balance_after_usd_cents >= 0),
  withdrawal_id TEXT NULL UNIQUE REFERENCES withdrawals(id),
  created_at TEXT NOT NULL
);

CREATE INDEX idx_ledger_entries_account ON ledger_entries(account_id, id);

CREATE TRIGGER ledger_entries_no_update BEFORE UPDATE ON ledger_entries
BEGIN SELECT RAISE(ABORT, 'ledger_entries is append-only'); END;

CREATE TRIGGER ledger_entries_no_delete BEFORE DELETE ON ledger_entries
BEGIN SELECT RAISE(ABORT, 'ledger_entries is append-only'); END;

-- Append-only audit trail of security- and money-relevant events.
CREATE TABLE audit_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  account_id TEXT NULL,
  event_type TEXT NOT NULL,
  subject TEXT NULL,
  detail TEXT NULL,
  created_at TEXT NOT NULL
);

CREATE INDEX idx_audit_events_account ON audit_events(account_id, id);

CREATE TRIGGER audit_events_no_update BEFORE UPDATE ON audit_events
BEGIN SELECT RAISE(ABORT, 'audit_events is append-only'); END;

CREATE TRIGGER audit_events_no_delete BEFORE DELETE ON audit_events
BEGIN SELECT RAISE(ABORT, 'audit_events is append-only'); END;

-- Backfill the ledger for data written before it existed: an opening
-- balance equal to today's balance plus everything withdrawn, then one
-- entry per historical withdrawal in insertion order.
INSERT INTO ledger_entries(account_id, entry_type, amount_usd_cents, balance_after_usd_cents, created_at)
SELECT a.id, 'opening_balance', a.balance_usd_cents + COALESCE(SUM(w.amount_usd_cents), 0),
       a.balance_usd_cents + COALESCE(SUM(w.amount_usd_cents), 0), a.created_at
FROM accounts a LEFT JOIN withdrawals w ON w.account_id = a.id
GROUP BY a.id;

INSERT INTO ledger_entries(account_id, entry_type, amount_usd_cents, balance_after_usd_cents, withdrawal_id, created_at)
SELECT w.account_id, 'withdrawal', -w.amount_usd_cents,
       o.amount_usd_cents - SUM(w.amount_usd_cents) OVER (PARTITION BY w.account_id ORDER BY w.rowid),
       w.id, w.created_at
FROM withdrawals w JOIN ledger_entries o ON o.account_id = w.account_id AND o.entry_type = 'opening_balance'
ORDER BY w.account_id, w.rowid;
