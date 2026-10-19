# Withdrawal Authorisation Service

A Go service that simulates USD bank accounts and authorises withdrawals paid out in another currency. It converts the amount using Frankfurter, checks and debits the balance atomically, and returns an authorisation code. No real payout is made.

## Running it

Requires Go 1.26+. No C toolchain is needed.

```bash
go run . serve
```

The service listens on `:8080` with metrics on `:9090`. It creates `withdrawals.db` and seeds a demo account:

- account `acc_demo`
- token `demo-token-please-change`
- balance USD 1,000.00

Or run it with Docker:

```bash
make docker
docker run --rm -p 8080:8080 -p 9090:9090 -v withdrawal-data:/app/data withdrawal-service
```

Other commands: `go run . migrate`, `go run . reconcile` (exits non-zero if any balance disagrees with its ledger), `go run . version`.

## Using it

```bash
curl -i http://localhost:8080/v1/accounts/acc_demo/withdrawals \
  -H 'Authorization: Bearer demo-token-please-change' \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: order-42' \
  -d '{"amount":"125.50","target_currency":"EUR"}'
```

```json
{
  "id": "wd_4e8a7cbe6d10",
  "account_id": "acc_demo",
  "status": "authorised",
  "authorisation_code": "A7Q2-91XK",
  "amount_usd": "125.50",
  "target_currency": "EUR",
  "converted_amount": "111.68",
  "exchange_rate": "0.88989",
  "exchange_rate_date": "2026-10-20",
  "created_at": "2026-10-19T20:59:09Z"
}
```

`Idempotency-Key` is optional. Sending the same key and body again returns the original withdrawal with `Idempotent-Replayed: true`, without debiting again.

| Endpoint | |
|---|---|
| `POST /v1/accounts/{id}/withdrawals` | Authorise a withdrawal |
| `GET /v1/accounts/{id}/reconciliation` | Compare the balance with the ledger |
| `POST /v1/accounts/{id}/tokens/rotate` | Revoke the current token and issue a new, expiring one |
| `DELETE /v1/accounts/{id}/tokens/current` | Revoke the current token |
| `GET /healthz` | Liveness |

Errors look like `{"error":{"code":"INSUFFICIENT_FUNDS","message":"..."}}`:

| Status | Codes |
|---|---|
| 400 | `INVALID_REQUEST`, `UNSUPPORTED_CURRENCY` |
| 401 | `UNAUTHENTICATED`, `INVALID_TOKEN`, `TOKEN_EXPIRED` |
| 403 | `ACCOUNT_ACCESS_DENIED` |
| 404 | `ACCOUNT_NOT_FOUND` |
| 409 | `INSUFFICIENT_FUNDS` |
| 422 | `IDEMPOTENCY_KEY_REUSED` |
| 502 | `EXCHANGE_RATE_UNAVAILABLE`, `EXCHANGE_RATE_STALE` |

### Configuration

Everything is read from environment variables (see `.env.example`). The main ones:

| Variable | Default | |
|---|---|---|
| `PORT` / `METRICS_PORT` | `8080` / `9090` | |
| `DATABASE_PATH` | `withdrawals.db` | |
| `APP_ENV` | `development` | Outside `development`, `serve` refuses to start with the default demo token |
| `DEMO_ACCOUNT_ID`, `DEMO_BEARER_TOKEN`, `DEMO_BALANCE_USD` | see above | Seeded once; delete the database to reset |
| `FRANKFURTER_BASE_URL` | `https://api.frankfurter.dev/v2` | |
| `FX_MAX_RATE_AGE` | `96h` | Older rates are refused |
| `TOKEN_TTL` | `720h` | Lifetime of rotated tokens |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | unset | Export traces when set |

## How it works

A withdrawal request goes through these steps:

1. **Authenticate.** Hash the bearer token, look it up, and check it belongs to the account in the path.
2. **Validate.** The amount must be a USD decimal string with at most 2 decimal places. The currency must be an ISO 4217 code with a defined minor unit.
3. **If an `Idempotency-Key` was sent and already used,** return the stored withdrawal.
4. **Fetch the rate** from Frankfurter. USD to USD skips this step.
5. **Convert** with exact rational arithmetic, rounded half-up to the target currency's minor unit (0 decimals for JPY, 3 for KWD, and so on).
6. **In one transaction:** debit only if the balance is enough, insert the withdrawal, append a ledger entry and an audit event.

### Key decisions

**The funds check is the debit.** `UPDATE accounts SET balance_usd_cents = balance_usd_cents - ? WHERE id = ? AND balance_usd_cents >= ?`. If no row is updated, the funds were insufficient. Reading the balance first and then writing would let two concurrent requests both pass the check. A test fires concurrent withdrawals at one account to prove it can't be overdrawn.

**Money is never a float.** Balances are integer cents, rates are parsed as decimal strings, and conversion uses `big.Rat`.

**SQLite.** An in-memory map would be enough to demo the endpoint, but the core of this problem is a transactional invariant. I wanted real transactions and constraints enforcing it, not application locks. SQLite gives that with zero setup for whoever runs this, and the pure-Go driver keeps the binary static. Everything here (conditional update, unique indexes, triggers) maps directly to PostgreSQL once more than one instance is needed.

**Frankfurter is treated as unreliable.**
- **Before any write:** the rate is fetched before anything is written, so a provider failure can never leave a half-done withdrawal.
- **Timeouts and retry:** each attempt has a 2-second timeout, with separate limits for connecting, TLS and waiting for headers. Timeouts, connection errors, `429` and `5xx` are retried once, which keeps the worst case inside the 5-second request timeout.
- **Warm-up:** a connection is opened at startup, so the first request isn't slowed by connection setup.
- **No fallback rate:** if the rate still can't be fetched, or it's older than `FX_MAX_RATE_AGE`, the request fails with a 502 and nothing is debited. I didn't add a fallback or cached rate. Authorising at a wrong rate is worse than refusing.
- **Logging:** the actual cause is logged with the request's trace ID. The client only sees a generic message.

**Authorisation.**
- **Token storage:** tokens are stored as SHA-256 hashes, never in plaintext.
- **Scope:** each token is scoped to one account. A valid token used on another account's path gets 403.
- **Lifecycle:** rotation revokes the old token and issues a random one that expires, in one transaction. Revocation is immediate.

How accounts and their first tokens get created was left open in the brief. Here they come from a development seed.

**Authorisation codes** are 8 characters, `XXXX-XXXX`, from `crypto/rand`, using an alphabet without look-alike characters (no 0/O or 1/I/L). A unique constraint backs them.

**Idempotency.** Withdrawals are exactly the kind of request clients retry on a timeout.
- **Scope:** a key is scoped to the account and stored with a hash of the normalised request.
- **Concurrent retries:** a unique index guarantees only one of them debits.
- **Reusing a key:** reusing a key with a different body is rejected.
- **Declines:** they aren't stored against the key, so the client can retry after topping up.

**Ledger and audit.**
- **Ledger:** every balance change is appended with the resulting balance.
- **Audit:** token events and authorised and declined withdrawals are recorded.
- **Append-only:** both tables reject updates and deletes using triggers.
- **Reconciliation:** checks that the balance equals the ledger sum. It's available as an endpoint and as the `reconcile` command.

**Structure.** One binary and one database, split by domain under `internal/apps`:
- `withdrawal`
- `account`, which covers tokens and reconciliation
- `audit`

Each module separates handlers, business rules and SQL into `controllers`, `services` and `repositories`. That lets me test the money logic without HTTP and the transactions without the FX provider. Audit writes take the caller's transaction, so an event can't be committed without the change it describes.

```text
cmd/              serve, migrate, reconcile, version
config/           environment configuration
database/         SQLite connection and embedded SQL migrations
internal/apps/    withdrawal, account, audit modules
internal/frankfurter/  FX client
internal/router/  middleware (auth, access log, security headers, timeout)
```

**Observability.**
- **Metrics:** Prometheus metrics on a separate port: requests, latency, withdrawal outcomes and FX latency.
- **Tracing:** OpenTelemetry spans, including the Frankfurter call.
- **Correlation:** every response carries `X-Trace-Id`, and the same ID is in the logs.

## Tests

```bash
go test -race ./...
```

I focused tests where a bug would lose money or leak access:
- concurrent withdrawals that must not overdraw;
- rollback when a write fails after the debit;
- exact conversion and rounding;
- FX failures, timeouts and stale rates that must not debit;
- token scope across accounts;
- token rotation, expiry and revocation;
- concurrent retries with one idempotency key that must debit once;
- reconciliation catching a tampered balance.

The Frankfurter client is tested against a local stub server, so the suite never touches the network. HTTP-level tests run the full handler stack against an in-memory database.

## What I left out, and what's next

- **PostgreSQL** for running more than one instance. SQLite with one writer is fine for a single instance.
- **Account and credential issuance** through an identity provider or admin API, instead of a seed.
- **Holds instead of immediate debits.** "Authorised" currently means debited. A real payout flow would reserve funds, then capture or release them when the payout settles or fails.
- **A second FX provider** and a short-lived rate cache.
- **Expiring idempotency keys** after a retention window.
- **Scheduling `reconcile`** with alerting, and keeping the metrics port internal-only.

## AI usage

