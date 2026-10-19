package services

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/sdq-codes/withdrawal-service/internal/apps/withdrawal/dto"
	"github.com/sdq-codes/withdrawal-service/internal/apps/withdrawal/models"
	"github.com/sdq-codes/withdrawal-service/pkg/money"
)

var (
	amountPattern         = regexp.MustCompile(`^[0-9]{1,12}(\.[0-9]{1,2})?$`)
	idempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)
	tracer                = otel.Tracer("github.com/sdq-codes/withdrawal-service/internal/apps/withdrawal/services")
)

// Covers weekends and single-day holidays in daily rate publication.
const DefaultMaxRateAge = 96 * time.Hour

type RateProvider interface {
	GetRate(ctx context.Context, baseCurrency, targetCurrency string) (models.Rate, error)
}

type Repository interface {
	AuthoriseWithdrawal(ctx context.Context, withdrawal models.Withdrawal) error
	FindByIdempotencyKey(ctx context.Context, accountID, key string) (models.Withdrawal, error)
}

type Service struct {
	repo       Repository
	rates      RateProvider
	now        func() time.Time
	maxRateAge time.Duration
}

type Option func(*Service)

// Age is measured from the start (UTC) of the rate's effective date.
func WithMaxRateAge(age time.Duration) Option {
	return func(s *Service) { s.maxRateAge = age }
}

func NewService(repo Repository, rates RateProvider, opts ...Option) *Service {
	s := &Service{repo: repo, rates: rates, now: func() time.Time { return time.Now().UTC() }, maxRateAge: DefaultMaxRateAge}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Only authorised withdrawals are stored against an idempotency key, so a
// declined request can be retried with the same key.
func (s *Service) Create(ctx context.Context, accountID, idempotencyKey string, req dto.CreateWithdrawalRequest) (wd models.Withdrawal, replayed bool, err error) {
	ctx, span := tracer.Start(ctx, "withdrawal.create")
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		}
		span.SetAttributes(attribute.Bool("withdrawal.idempotent_replay", replayed))
		span.End()
	}()

	cents, err := parseUSDCents(req.Amount)
	if err != nil {
		return models.Withdrawal{}, false, models.ErrInvalidAmount
	}
	currency := req.TargetCurrency
	units, ok := MinorUnits(currency)
	if !ok {
		return models.Withdrawal{}, false, models.ErrInvalidCurrency
	}
	span.SetAttributes(attribute.String("withdrawal.target_currency", currency))
	if idempotencyKey != "" && !idempotencyKeyPattern.MatchString(idempotencyKey) {
		return models.Withdrawal{}, false, models.ErrInvalidIdempotencyKey
	}
	requestHash := hashRequest(accountID, cents, currency)
	if idempotencyKey != "" {
		if existing, found, err := s.replay(ctx, accountID, idempotencyKey, requestHash); found || err != nil {
			return existing, found, err
		}
	}

	now := s.now().UTC()
	rate := models.Rate{Value: "1", Date: now}
	if currency != "USD" {
		rate, err = s.rates.GetRate(ctx, "USD", currency)
		if err != nil {
			if errors.Is(err, models.ErrCurrencyNotSupported) || errors.Is(err, models.ErrExchangeRateUnavailable) {
				return models.Withdrawal{}, false, err
			}
			return models.Withdrawal{}, false, fmt.Errorf("%w: %v", models.ErrExchangeRateUnavailable, err)
		}
		if rate.Date.IsZero() {
			return models.Withdrawal{}, false, fmt.Errorf("%w: rate has no effective date", models.ErrExchangeRateUnavailable)
		}
		if now.Sub(rate.Date) > s.maxRateAge {
			return models.Withdrawal{}, false, models.ErrExchangeRateStale
		}
	}
	rateRat, ok := new(big.Rat).SetString(rate.Value)
	if !ok || rateRat.Sign() <= 0 {
		return models.Withdrawal{}, false, models.ErrExchangeRateUnavailable
	}

	id, err := randomHexID("wd_", 6)
	if err != nil {
		return models.Withdrawal{}, false, fmt.Errorf("generate withdrawal id: %w", err)
	}
	code, err := randomAuthorisationCode()
	if err != nil {
		return models.Withdrawal{}, false, fmt.Errorf("generate authorisation code: %w", err)
	}

	wd = models.Withdrawal{
		ID: id, AccountID: accountID, Status: "authorised",
		AuthorisationCode: code, AmountUSDCents: cents,
		AmountUSD: money.FormatCents(cents), TargetCurrency: currency,
		ConvertedAmount:  convertAndRound(cents, rateRat, units),
		ExchangeRate:     normalizeRate(rate.Value),
		ExchangeRateDate: rate.Date.UTC().Format(time.DateOnly),
		CreatedAt:        now,
		IdempotencyKey:   idempotencyKey, RequestHash: requestHash,
	}
	if err := s.repo.AuthoriseWithdrawal(ctx, wd); err != nil {
		if errors.Is(err, models.ErrDuplicateIdempotencyKey) {
			// A concurrent retry committed first; this attempt was rolled back.
			existing, found, replayErr := s.replay(ctx, accountID, idempotencyKey, requestHash)
			if replayErr != nil {
				return models.Withdrawal{}, false, replayErr
			}
			if found {
				return existing, true, nil
			}
		}
		return models.Withdrawal{}, false, err
	}
	return wd, false, nil
}

func (s *Service) replay(ctx context.Context, accountID, key, requestHash string) (models.Withdrawal, bool, error) {
	existing, err := s.repo.FindByIdempotencyKey(ctx, accountID, key)
	if errors.Is(err, models.ErrNotFound) {
		return models.Withdrawal{}, false, nil
	}
	if err != nil {
		return models.Withdrawal{}, false, err
	}
	if existing.RequestHash != requestHash {
		return models.Withdrawal{}, false, models.ErrIdempotencyKeyReused
	}
	return existing, true, nil
}

// Hashes the normalised request, so "10" and "10.00" are the same request.
func hashRequest(accountID string, cents int64, currency string) string {
	sum := sha256.Sum256([]byte(accountID + "\x00" + strconv.FormatInt(cents, 10) + "\x00" + currency))
	return hex.EncodeToString(sum[:])
}

func parseUSDCents(raw string) (int64, error) {
	if !amountPattern.MatchString(raw) {
		return 0, models.ErrInvalidAmount
	}
	parts := strings.SplitN(raw, ".", 2)
	dollars, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, models.ErrInvalidAmount
	}
	fraction := "00"
	if len(parts) == 2 {
		fraction = parts[1]
		if len(fraction) == 1 {
			fraction += "0"
		}
	}
	fractionCents, err := strconv.ParseInt(fraction, 10, 64)
	if err != nil {
		return 0, models.ErrInvalidAmount
	}
	cents := dollars*100 + fractionCents
	if cents <= 0 {
		return 0, models.ErrInvalidAmount
	}
	return cents, nil
}

// Exact rational arithmetic, rounded half-up.
func convertAndRound(usdCents int64, rate *big.Rat, targetMinorUnits int) string {
	factor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(targetMinorUnits)), nil)
	numerator := new(big.Int).Mul(big.NewInt(usdCents), factor)
	numerator.Mul(numerator, rate.Num())
	denominator := new(big.Int).Mul(big.NewInt(100), rate.Denom())
	q, rem := new(big.Int), new(big.Int)
	q.QuoRem(numerator, denominator, rem)
	if new(big.Int).Mul(rem, big.NewInt(2)).Cmp(denominator) >= 0 {
		q.Add(q, big.NewInt(1))
	}
	value := q.String()
	if targetMinorUnits == 0 {
		return value
	}
	for len(value) <= targetMinorUnits {
		value = "0" + value
	}
	cut := len(value) - targetMinorUnits
	return value[:cut] + "." + value[cut:]
}

func normalizeRate(raw string) string {
	if strings.ContainsAny(raw, "eE") {
		return raw
	}
	if strings.Contains(raw, ".") {
		raw = strings.TrimRight(raw, "0")
		raw = strings.TrimRight(raw, ".")
	}
	if raw == "" {
		return "0"
	}
	return raw
}

func randomHexID(prefix string, bytes int) (string, error) {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(b), nil
}

func randomAuthorisationCode() (string, error) {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b[:4]) + "-" + string(b[4:]), nil
}
