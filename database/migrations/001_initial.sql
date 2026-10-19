-- Original schema. IF NOT EXISTS lets databases created before migrations
-- were versioned (user_version 0) upgrade in place.

CREATE TABLE IF NOT EXISTS accounts (
  id TEXT PRIMARY KEY,
  balance_usd_cents INTEGER NOT NULL CHECK (balance_usd_cents >= 0),
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS api_tokens (
  token_hash TEXT PRIMARY KEY,
  account_id TEXT NOT NULL REFERENCES accounts(id),
  revoked_at TEXT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS withdrawals (
  id TEXT PRIMARY KEY,
  account_id TEXT NOT NULL REFERENCES accounts(id),
  status TEXT NOT NULL CHECK (status = 'authorised'),
  authorisation_code TEXT NOT NULL UNIQUE,
  amount_usd_cents INTEGER NOT NULL CHECK (amount_usd_cents > 0),
  target_currency TEXT NOT NULL CHECK (length(target_currency) = 3),
  converted_amount TEXT NOT NULL,
  exchange_rate TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_withdrawals_account_created ON withdrawals(account_id, created_at);
