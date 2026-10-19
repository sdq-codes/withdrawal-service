package router

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sdq-codes/withdrawal-service/database"
	accountRepositories "github.com/sdq-codes/withdrawal-service/internal/apps/account/repositories"
	withdrawalModels "github.com/sdq-codes/withdrawal-service/internal/apps/withdrawal/models"
	withdrawalServices "github.com/sdq-codes/withdrawal-service/internal/apps/withdrawal/services"
	httpapi "github.com/sdq-codes/withdrawal-service/internal/interface/http"
)

type fixedRates struct {
	rate string
	date time.Time
	err  error
}

func (f fixedRates) GetRate(context.Context, string, string) (withdrawalModels.Rate, error) {
	date := f.date
	if date.IsZero() {
		date = time.Now().UTC().Truncate(24 * time.Hour)
	}
	return withdrawalModels.Rate{Value: f.rate, Date: date}, f.err
}

type testDB struct {
	accounts *accountRepositories.AccountRepository
}

func (d testDB) BalanceCents(ctx context.Context, accountID string) (int64, error) {
	return d.accounts.BalanceCents(ctx, accountID)
}

func newTestServer(t *testing.T, balance int64) (http.Handler, testDB) {
	t.Helper()
	return newTestServerWithRates(t, balance, fixedRates{rate: "0.92"})
}

func newTestServerWithRates(t *testing.T, balance int64, rates withdrawalServices.RateProvider) (http.Handler, testDB) {
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
	if err := accounts.SeedDemo(context.Background(), "acc_1", "valid-token", balance); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := New(httpapi.Dependencies{DB: db, Rates: rates, Logger: logger, TokenTTL: time.Hour, MaxRateAge: withdrawalServices.DefaultMaxRateAge})
	return handler, testDB{accounts: accounts}
}

func doRequest(s http.Handler, account, token, body string) *httptest.ResponseRecorder {
	return doRequestWithKey(s, account, token, "", body)
}

func doRequestWithKey(s http.Handler, account, token, idempotencyKey, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/v1/accounts/"+account+"/withdrawals", strings.NewReader(body))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	if idempotencyKey != "" {
		r.Header.Set("Idempotency-Key", idempotencyKey)
	}
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func doAuthed(s http.Handler, method, path, token string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	return w
}

func TestCreateWithdrawalReturns201(t *testing.T) {
	s, _ := newTestServer(t, 100000)
	w := doRequest(s, "acc_1", "valid-token", `{"amount":"125.50","target_currency":"EUR"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var result withdrawalModels.Withdrawal
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.ConvertedAmount != "115.46" || result.Status != "authorised" {
		t.Fatalf("unexpected response: %+v", result)
	}
}

func TestCreateWithdrawalAuthFailures(t *testing.T) {
	t.Run("missing token", func(t *testing.T) {
		s, _ := newTestServer(t, 10000)
		if got := doRequest(s, "acc_1", "", `{"amount":"1.00","target_currency":"EUR"}`).Code; got != http.StatusUnauthorized {
			t.Fatalf("status = %d", got)
		}
	})
	t.Run("invalid token", func(t *testing.T) {
		s, _ := newTestServer(t, 10000)
		if got := doRequest(s, "acc_1", "wrong-token", `{"amount":"1.00","target_currency":"EUR"}`).Code; got != http.StatusUnauthorized {
			t.Fatalf("status = %d", got)
		}
	})
	t.Run("wrong account", func(t *testing.T) {
		s, _ := newTestServer(t, 10000)
		if got := doRequest(s, "acc_other", "valid-token", `{"amount":"1.00","target_currency":"EUR"}`).Code; got != http.StatusForbidden {
			t.Fatalf("status = %d", got)
		}
	})
}

func TestCreateWithdrawalInsufficientFunds(t *testing.T) {
	s, db := newTestServer(t, 500)
	w := doRequest(s, "acc_1", "valid-token", `{"amount":"10.00","target_currency":"EUR"}`)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	balance, err := db.BalanceCents(context.Background(), "acc_1")
	if err != nil {
		t.Fatal(err)
	}
	if balance != 500 {
		t.Fatalf("balance = %d, want unchanged 500", balance)
	}
}

func TestCreateWithdrawalRejectsUnknownFields(t *testing.T) {
	s, _ := newTestServer(t, 10000)
	w := doRequest(s, "acc_1", "valid-token", `{"amount":"1.00","target_currency":"EUR","admin":true}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestCreateWithdrawalMalformedJSON(t *testing.T) {
	s, _ := newTestServer(t, 10000)
	w := doRequest(s, "acc_1", "valid-token", `{"amount":`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestIdempotentRetriesDebitOnce(t *testing.T) {
	s, db := newTestServer(t, 100000)
	const retries = 8
	responses := make([]*httptest.ResponseRecorder, retries)
	var wg sync.WaitGroup
	for i := range retries {
		wg.Add(1)
		go func() {
			defer wg.Done()
			responses[i] = doRequestWithKey(s, "acc_1", "valid-token", "order-42", `{"amount":"125.50","target_currency":"EUR"}`)
		}()
	}
	wg.Wait()

	ids, replays := map[string]bool{}, 0
	for _, w := range responses {
		if w.Code != http.StatusCreated {
			t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
		}
		var result withdrawalModels.Withdrawal
		if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		ids[result.ID] = true
		if w.Header().Get("Idempotent-Replayed") == "true" {
			replays++
		}
	}
	if len(ids) != 1 || replays != retries-1 {
		t.Fatalf("got %d distinct withdrawals and %d replays, want 1 and %d", len(ids), replays, retries-1)
	}
	if balance, _ := db.BalanceCents(context.Background(), "acc_1"); balance != 100000-12550 {
		t.Fatalf("balance = %d, want a single debit", balance)
	}

	w := doRequestWithKey(s, "acc_1", "valid-token", "order-42", `{"amount":"1.00","target_currency":"EUR"}`)
	if w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "IDEMPOTENCY_KEY_REUSED") {
		t.Fatalf("reused key with different body: status = %d body=%s", w.Code, w.Body.String())
	}
}

func TestStaleRateReturns502WithoutDebit(t *testing.T) {
	s, db := newTestServerWithRates(t, 10000, fixedRates{rate: "0.92", date: time.Now().UTC().AddDate(0, 0, -10)})
	w := doRequest(s, "acc_1", "valid-token", `{"amount":"10.00","target_currency":"EUR"}`)
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "EXCHANGE_RATE_STALE") {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	if balance, _ := db.BalanceCents(context.Background(), "acc_1"); balance != 10000 {
		t.Fatalf("balance = %d, want unchanged", balance)
	}
}

func TestReconciliationEndpoint(t *testing.T) {
	s, _ := newTestServer(t, 100000)
	if w := doRequest(s, "acc_1", "valid-token", `{"amount":"125.50","target_currency":"EUR"}`); w.Code != http.StatusCreated {
		t.Fatalf("status = %d", w.Code)
	}
	w := doAuthed(s, http.MethodGet, "/v1/accounts/acc_1/reconciliation", "valid-token")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["consistent"] != true || body["balance_usd"] != "874.50" || body["ledger_balance_usd"] != "874.50" || body["withdrawn_usd"] != "125.50" {
		t.Fatalf("unexpected reconciliation: %v", body)
	}
	if got := doAuthed(s, http.MethodGet, "/v1/accounts/acc_other/reconciliation", "valid-token").Code; got != http.StatusForbidden {
		t.Fatalf("cross-account reconciliation status = %d, want 403", got)
	}
}

func TestTokenRotationAndRevocation(t *testing.T) {
	s, _ := newTestServer(t, 10000)
	w := doAuthed(s, http.MethodPost, "/v1/accounts/acc_1/tokens/rotate", "valid-token")
	if w.Code != http.StatusCreated {
		t.Fatalf("rotate status = %d body=%s", w.Code, w.Body.String())
	}
	var rotated struct {
		Token     string `json:"token"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &rotated); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(rotated.Token, "wds_") || rotated.ExpiresAt == "" {
		t.Fatalf("unexpected rotation response: %s", w.Body.String())
	}
	if got := doRequest(s, "acc_1", "valid-token", `{"amount":"1.00","target_currency":"USD"}`).Code; got != http.StatusUnauthorized {
		t.Fatalf("old token status = %d, want 401", got)
	}
	if got := doRequest(s, "acc_1", rotated.Token, `{"amount":"1.00","target_currency":"USD"}`).Code; got != http.StatusCreated {
		t.Fatalf("new token status = %d, want 201", got)
	}
	if got := doAuthed(s, http.MethodDelete, "/v1/accounts/acc_1/tokens/current", rotated.Token).Code; got != http.StatusNoContent {
		t.Fatalf("revoke status = %d, want 204", got)
	}
	if got := doRequest(s, "acc_1", rotated.Token, `{"amount":"1.00","target_currency":"USD"}`).Code; got != http.StatusUnauthorized {
		t.Fatalf("revoked token status = %d, want 401", got)
	}
}
