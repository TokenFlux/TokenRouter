package repository

import (
	"context"
	"testing"

	infraerrors "github.com/BrandonVee/TokenRouter/internal/pkg/errors"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyRepository_CreateEnforcesUserLimitAcrossStatuses(t *testing.T) {
	repo, client := newAPIKeyRepoSQLite(t)
	ctx := context.Background()
	user := mustCreateAPIKeyRepoUser(t, ctx, client, "api-key-limit-status@test.com")
	require.NoError(t, client.User.UpdateOneID(user.ID).SetAPIKeyLimit(2).Exec(ctx))

	require.NoError(t, repo.Create(ctx, newLimitedAPIKey(user.ID, "sk-limit-active", service.StatusAPIKeyActive)))
	require.NoError(t, repo.Create(ctx, newLimitedAPIKey(user.ID, "sk-limit-disabled", service.StatusAPIKeyDisabled)))

	err := repo.Create(ctx, newLimitedAPIKey(user.ID, "sk-limit-rejected", service.StatusAPIKeyActive))
	require.ErrorIs(t, err, service.ErrAPIKeyLimitReached)
	require.Equal(t, 409, infraerrors.Code(err))
	appErr := infraerrors.FromError(err)
	require.Equal(t, "2", appErr.Metadata["current"])
	require.Equal(t, "2", appErr.Metadata["limit"])
}

func TestAPIKeyRepository_CreateReleasesSlotAfterSoftDelete(t *testing.T) {
	repo, client := newAPIKeyRepoSQLite(t)
	ctx := context.Background()
	user := mustCreateAPIKeyRepoUser(t, ctx, client, "api-key-limit-delete@test.com")
	require.NoError(t, client.User.UpdateOneID(user.ID).SetAPIKeyLimit(1).Exec(ctx))

	first := newLimitedAPIKey(user.ID, "sk-limit-delete-first", service.StatusAPIKeyActive)
	require.NoError(t, repo.Create(ctx, first))
	require.NoError(t, repo.Delete(ctx, first.ID))
	require.NoError(t, repo.Create(ctx, newLimitedAPIKey(user.ID, "sk-limit-delete-second", service.StatusAPIKeyActive)))
}

func TestAPIKeyRepository_CreateAllowsUnlimitedAndPreservesExistingKeysAfterLowering(t *testing.T) {
	repo, client := newAPIKeyRepoSQLite(t)
	ctx := context.Background()
	user := mustCreateAPIKeyRepoUser(t, ctx, client, "api-key-limit-unlimited@test.com")
	require.NoError(t, client.User.UpdateOneID(user.ID).SetAPIKeyLimit(0).Exec(ctx))

	for _, key := range []string{"sk-limit-unlimited-1", "sk-limit-unlimited-2", "sk-limit-unlimited-3"} {
		require.NoError(t, repo.Create(ctx, newLimitedAPIKey(user.ID, key, service.StatusAPIKeyActive)))
	}
	require.NoError(t, client.User.UpdateOneID(user.ID).SetAPIKeyLimit(2).Exec(ctx))

	err := repo.Create(ctx, newLimitedAPIKey(user.ID, "sk-limit-after-lowering", service.StatusAPIKeyActive))
	require.ErrorIs(t, err, service.ErrAPIKeyLimitReached)
	count, countErr := repo.CountByUserID(ctx, user.ID)
	require.NoError(t, countErr)
	require.Equal(t, int64(3), count)
}

func newLimitedAPIKey(userID int64, key, status string) *service.APIKey {
	return &service.APIKey{
		UserID: userID,
		Key:    key,
		Name:   key,
		Status: status,
	}
}
