package middleware

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	accountModels "github.com/sdq-codes/withdrawal-service/internal/apps/account/models"
	"github.com/sdq-codes/withdrawal-service/internal/helper/auth"
	"github.com/sdq-codes/withdrawal-service/internal/interface/response"
)

type Middleware func(http.HandlerFunc) http.HandlerFunc

type TokenVerifier interface {
	AccountIDForToken(ctx context.Context, tokenHash string) (string, error)
}

type contextKey int

const (
	accountIDKey contextKey = iota
	tokenHashKey
)

func RequireAccountToken(tokens TokenVerifier, logger *slog.Logger) Middleware {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			token, ok := auth.BearerToken(r.Header.Get("Authorization"))
			if !ok {
				response.Error(w, http.StatusUnauthorized, "UNAUTHENTICATED", "A valid Bearer token is required.")
				return
			}
			tokenHash := auth.HashToken(token)
			accountID, err := tokens.AccountIDForToken(r.Context(), tokenHash)
			switch {
			case errors.Is(err, accountModels.ErrInvalidToken):
				response.Error(w, http.StatusUnauthorized, "INVALID_TOKEN", "The supplied token is invalid or revoked.")
				return
			case errors.Is(err, accountModels.ErrTokenExpired):
				response.Error(w, http.StatusUnauthorized, "TOKEN_EXPIRED", "The supplied token has expired.")
				return
			case err != nil:
				response.InternalError(w, r, logger, err)
				return
			}
			if accountID != r.PathValue("accountID") {
				response.Error(w, http.StatusForbidden, "ACCOUNT_ACCESS_DENIED", "The token does not grant access to this account.")
				return
			}
			ctx := context.WithValue(r.Context(), accountIDKey, accountID)
			ctx = context.WithValue(ctx, tokenHashKey, tokenHash)
			next(w, r.WithContext(ctx))
		}
	}
}

func AccountID(ctx context.Context) string {
	id, _ := ctx.Value(accountIDKey).(string)
	return id
}

func TokenHash(ctx context.Context) string {
	hash, _ := ctx.Value(tokenHashKey).(string)
	return hash
}
