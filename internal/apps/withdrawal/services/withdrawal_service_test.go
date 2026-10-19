package services

import (
	"context"
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/sdq-codes/withdrawal-service/internal/apps/withdrawal/dto"
	"github.com/sdq-codes/withdrawal-service/internal/apps/withdrawal/models"
)

type fakeRates struct {
	rate  string
	date  time.Time
	err   error
	calls int
}

func (f *fakeRates) GetRate(context.Context, string, string) (models.Rate, error) {
	f.calls++
	date := f.date
	if date.IsZero() {
		date = time.Now().UTC().Truncate(24 * time.Hour)
	}
	return models.Rate{Value: f.rate, Date: date}, f.err
}

type fakeRepository struct {
	withdrawals []models.Withdrawal
	err         error
}

func (f *fakeRepository) AuthoriseWithdrawal(_ context.Context, wd models.Withdrawal) error {
	if f.err != nil {
		return f.err
	}
	f.withdrawals = append(f.withdrawals, wd)
	return nil
}

func (f *fakeRepository) FindByIdempotencyKey(_ context.Context, accountID, key string) (models.Withdrawal, error) {
	for _, wd := range f.withdrawals {
		if wd.AccountID == accountID && wd.IdempotencyKey == key {
			return wd, nil
		}
	}
	return models.Withdrawal{}, models.ErrNotFound
}

func TestCreateWithdrawalConvertsAndPersists(t *testing.T) {
	repo := &fakeRepository{}
	rates := &fakeRates{rate: "0.92"}
	svc := NewService(repo, rates)
	got, _, err := svc.Create(context.Background(), "acc_123", "", dto.CreateWithdrawalRequest{Amount: "125.50", TargetCurrency: "EUR"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if got.AmountUSD != "125.50" {
		t.Errorf("amount_usd = %q, want 125.50", got.AmountUSD)
	}
	if got.ConvertedAmount != "115.46" {
		t.Errorf("converted_amount = %q, want 115.46", got.ConvertedAmount)
	}
	if got.ExchangeRate != "0.92" {
		t.Errorf("exchange_rate = %q, want 0.92", got.ExchangeRate)
	}
	if got.Status != "authorised" {
		t.Errorf("status = %q", got.Status)
	}
	if got.AccountID != "acc_123" {
		t.Errorf("account_id = %q", got.AccountID)
	}
	if got.AuthorisationCode == "" || len(got.AuthorisationCode) != 9 {
		t.Errorf("invalid authorisation code %q", got.AuthorisationCode)
	}
	if len(repo.withdrawals) != 1 {
		t.Fatalf("persisted %d withdrawals, want 1", len(repo.withdrawals))
	}
	if rates.calls != 1 {
		t.Errorf("rate provider called %d times, want 1", rates.calls)
	}
}

func TestCreateUSDDoesNotCallFXProvider(t *testing.T) {
	rates := &fakeRates{rate: "0"}
	repo := &fakeRepository{}
	got, _, err := NewService(repo, rates).Create(context.Background(), "acc_1", "", dto.CreateWithdrawalRequest{Amount: "3", TargetCurrency: "USD"})
	if err != nil {
		t.Fatal(err)
	}
	if got.AmountUSD != "3.00" || got.ConvertedAmount != "3.00" || got.ExchangeRate != "1" {
		t.Fatalf("unexpected USD conversion: %+v", got)
	}
	if rates.calls != 0 {
		t.Errorf("FX provider called %d times, want 0", rates.calls)
	}
}

func TestCreateRoundsZeroDecimalCurrency(t *testing.T) {
	svc := NewService(&fakeRepository{}, &fakeRates{rate: "100.5"})
	got, _, err := svc.Create(context.Background(), "acc_1", "", dto.CreateWithdrawalRequest{Amount: "123.45", TargetCurrency: "JPY"})
	if err != nil {
		t.Fatal(err)
	}
	if got.ConvertedAmount != "12407" {
		t.Errorf("JPY converted_amount = %q, want 12407", got.ConvertedAmount)
	}
}

func TestCreateRejectsInvalidAmounts(t *testing.T) {
	invalid := []string{"", "0", "0.00", "-1", "1.001", "1e2", " 1.00", ".50", "1000000000000.00"}
	for _, amount := range invalid {
		t.Run(amount, func(t *testing.T) {
			_, _, err := NewService(&fakeRepository{}, &fakeRates{rate: "1"}).Create(context.Background(), "acc_1", "", dto.CreateWithdrawalRequest{Amount: amount, TargetCurrency: "EUR"})
			if !errors.Is(err, models.ErrInvalidAmount) {
				t.Errorf("error = %v, want models.ErrInvalidAmount", err)
			}
		})
	}
}

func TestCreateRejectsInvalidCurrencyBeforeFX(t *testing.T) {
	rates := &fakeRates{rate: "1"}
	_, _, err := NewService(&fakeRepository{}, rates).Create(context.Background(), "acc_1", "", dto.CreateWithdrawalRequest{Amount: "1.00", TargetCurrency: "abc"})
	if !errors.Is(err, models.ErrInvalidCurrency) {
		t.Fatalf("error = %v, want models.ErrInvalidCurrency", err)
	}
	if rates.calls != 0 {
		t.Errorf("FX provider called for invalid currency")
	}
}

func TestCreatePropagatesProviderFailure(t *testing.T) {
	rates := &fakeRates{err: models.ErrExchangeRateUnavailable}
	_, _, err := NewService(&fakeRepository{}, rates).Create(context.Background(), "acc_1", "", dto.CreateWithdrawalRequest{Amount: "1.00", TargetCurrency: "EUR"})
	if !errors.Is(err, models.ErrExchangeRateUnavailable) {
		t.Fatalf("error = %v, want models.ErrExchangeRateUnavailable", err)
	}
}

func TestCreateDoesNotReturnAuthorisedWithdrawalWhenPersistenceFails(t *testing.T) {
	dbErr := errors.New("database offline")
	_, _, err := NewService(&fakeRepository{err: dbErr}, &fakeRates{rate: "1"}).Create(context.Background(), "acc_1", "", dto.CreateWithdrawalRequest{Amount: "1.00", TargetCurrency: "EUR"})
	if !errors.Is(err, dbErr) {
		t.Fatalf("error = %v, want persistence error", err)
	}
}

func TestRationalConversionUsesExactDecimalRate(t *testing.T) {
	rate, ok := new(big.Rat).SetString("0.92")
	if !ok {
		t.Fatal("failed to parse test rate")
	}
	if got := convertAndRound(12550, rate, 2); got != "115.46" {
		t.Fatalf("converted amount = %s", got)
	}
}

func TestCreateReplaysIdempotentRequestWithoutFXOrDebit(t *testing.T) {
	repo := &fakeRepository{}
	rates := &fakeRates{rate: "0.92"}
	svc := NewService(repo, rates)
	first, replayed, err := svc.Create(context.Background(), "acc_1", "key-1", dto.CreateWithdrawalRequest{Amount: "10", TargetCurrency: "EUR"})
	if err != nil || replayed {
		t.Fatalf("first Create() = replayed %v, err %v", replayed, err)
	}
	second, replayed, err := svc.Create(context.Background(), "acc_1", "key-1", dto.CreateWithdrawalRequest{Amount: "10.00", TargetCurrency: "EUR"})
	if err != nil {
		t.Fatal(err)
	}
	if !replayed || second.ID != first.ID || second.AuthorisationCode != first.AuthorisationCode {
		t.Fatalf("second Create() = %+v replayed=%v, want replay of %s", second, replayed, first.ID)
	}
	if len(repo.withdrawals) != 1 || rates.calls != 1 {
		t.Fatalf("persisted %d withdrawals with %d FX calls, want 1 and 1", len(repo.withdrawals), rates.calls)
	}
}

func TestCreateRejectsIdempotencyKeyReuseWithDifferentRequest(t *testing.T) {
	svc := NewService(&fakeRepository{}, &fakeRates{rate: "0.92"})
	if _, _, err := svc.Create(context.Background(), "acc_1", "key-1", dto.CreateWithdrawalRequest{Amount: "10.00", TargetCurrency: "EUR"}); err != nil {
		t.Fatal(err)
	}
	_, _, err := svc.Create(context.Background(), "acc_1", "key-1", dto.CreateWithdrawalRequest{Amount: "11.00", TargetCurrency: "EUR"})
	if !errors.Is(err, models.ErrIdempotencyKeyReused) {
		t.Fatalf("error = %v, want models.ErrIdempotencyKeyReused", err)
	}
}

func TestCreateRejectsMalformedIdempotencyKey(t *testing.T) {
	_, _, err := NewService(&fakeRepository{}, &fakeRates{rate: "1"}).Create(context.Background(), "acc_1", "has spaces", dto.CreateWithdrawalRequest{Amount: "1.00", TargetCurrency: "EUR"})
	if !errors.Is(err, models.ErrInvalidIdempotencyKey) {
		t.Fatalf("error = %v, want models.ErrInvalidIdempotencyKey", err)
	}
}

func TestCreateRejectsStaleRate(t *testing.T) {
	repo := &fakeRepository{}
	rates := &fakeRates{rate: "0.92", date: time.Now().UTC().Add(-5 * 24 * time.Hour)}
	_, _, err := NewService(repo, rates, WithMaxRateAge(96*time.Hour)).Create(context.Background(), "acc_1", "", dto.CreateWithdrawalRequest{Amount: "1.00", TargetCurrency: "EUR"})
	if !errors.Is(err, models.ErrExchangeRateStale) {
		t.Fatalf("error = %v, want models.ErrExchangeRateStale", err)
	}
	if len(repo.withdrawals) != 0 {
		t.Fatal("stale rate must not reach persistence")
	}
}

func TestCreateRecordsRateDate(t *testing.T) {
	date := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	svc := NewService(&fakeRepository{}, &fakeRates{rate: "0.92", date: date})
	svc.now = func() time.Time { return date.Add(30 * time.Hour) }
	got, _, err := svc.Create(context.Background(), "acc_1", "", dto.CreateWithdrawalRequest{Amount: "1.00", TargetCurrency: "EUR"})
	if err != nil {
		t.Fatal(err)
	}
	if got.ExchangeRateDate != "2026-10-16" {
		t.Fatalf("exchange_rate_date = %q", got.ExchangeRateDate)
	}
}

func TestCreateAppliesMinorUnitPolicy(t *testing.T) {
	got, _, err := NewService(&fakeRepository{}, &fakeRates{rate: "0.30712"}).Create(context.Background(), "acc_1", "", dto.CreateWithdrawalRequest{Amount: "100.00", TargetCurrency: "KWD"})
	if err != nil {
		t.Fatal(err)
	}
	if got.ConvertedAmount != "30.712" {
		t.Errorf("KWD converted_amount = %q, want 30.712", got.ConvertedAmount)
	}
	for _, code := range []string{"XAU", "XDR", "XTS", "XXX"} {
		_, _, err := NewService(&fakeRepository{}, &fakeRates{rate: "1"}).Create(context.Background(), "acc_1", "", dto.CreateWithdrawalRequest{Amount: "1.00", TargetCurrency: code})
		if !errors.Is(err, models.ErrInvalidCurrency) {
			t.Errorf("%s: error = %v, want models.ErrInvalidCurrency (no ISO minor unit)", code, err)
		}
	}
}
