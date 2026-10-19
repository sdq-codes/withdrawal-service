package services

import (
	"context"
	"time"

	"github.com/sdq-codes/withdrawal-service/internal/apps/account/models"
	"github.com/sdq-codes/withdrawal-service/internal/apps/account/repositories"
)

type AccountService struct {
	accounts *repositories.AccountRepository
	tokens   *repositories.TokenRepository
	tokenTTL time.Duration
}

func New(accounts *repositories.AccountRepository, tokens *repositories.TokenRepository, tokenTTL time.Duration) *AccountService {
	return &AccountService{accounts: accounts, tokens: tokens, tokenTTL: tokenTTL}
}

func (s *AccountService) Reconcile(ctx context.Context, accountID string) (models.Reconciliation, error) {
	return s.accounts.Reconcile(ctx, accountID)
}

func (s *AccountService) ReconcileAll(ctx context.Context) ([]models.Reconciliation, error) {
	ids, err := s.accounts.ListIDs(ctx)
	if err != nil {
		return nil, err
	}
	results := make([]models.Reconciliation, 0, len(ids))
	for _, id := range ids {
		rec, err := s.accounts.Reconcile(ctx, id)
		if err != nil {
			return results, err
		}
		results = append(results, rec)
	}
	return results, nil
}

func (s *AccountService) RotateToken(ctx context.Context, tokenHash string) (models.IssuedToken, error) {
	return s.tokens.RotateToken(ctx, tokenHash, s.tokenTTL)
}

func (s *AccountService) RevokeToken(ctx context.Context, tokenHash string) error {
	return s.tokens.RevokeToken(ctx, tokenHash)
}
