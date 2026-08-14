//go:build integration

package repository

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyRepository_CreateSerializesLastAvailableSlot(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	email := fmt.Sprintf("api-key-limit-concurrency-%d@example.com", time.Now().UnixNano())
	user, err := integrationEntClient.User.Create().
		SetEmail(email).
		SetPasswordHash("test-password-hash").
		SetRole(service.RoleUser).
		SetStatus(service.StatusActive).
		SetAPIKeyLimit(1).
		Save(ctx)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM api_keys WHERE user_id = $1", user.ID)
		_, _ = integrationDB.ExecContext(context.Background(), "DELETE FROM users WHERE id = $1", user.ID)
	})

	repo := newAPIKeyRepositoryWithSQL(integrationEntClient, integrationDB)
	start := make(chan struct{})
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for index := range errs {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			<-start
			errs[index] = repo.Create(ctx, newLimitedAPIKey(
				user.ID,
				fmt.Sprintf("sk-limit-concurrent-%d-%d", time.Now().UnixNano(), index),
				service.StatusAPIKeyActive,
			))
		}(index)
	}
	close(start)
	wg.Wait()

	var succeeded, limited int
	for _, createErr := range errs {
		switch {
		case createErr == nil:
			succeeded++
		case errors.Is(createErr, service.ErrAPIKeyLimitReached):
			limited++
		default:
			require.NoError(t, createErr)
		}
	}
	require.Equal(t, 1, succeeded)
	require.Equal(t, 1, limited)

	count, err := repo.CountByUserID(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, int64(1), count)
}
