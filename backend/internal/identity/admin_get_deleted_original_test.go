//go:build unit

package identity_test

import (
	"context"
	"testing"
	"time"

	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/stretchr/testify/require"
)

func TestAdminService_GetUserIncludeDeleted(t *testing.T) {
	ts := time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)
	repo := &userRepoStub{user: &identity.User{ID: 7, Email: "del@test.com", DeletedAt: &ts}}
	svc := identity.NewUserAdmin(identity.AdminDependencies{Users: repo})

	got, err := svc.GetUserIncludeDeleted(context.Background(), 7)
	require.NoError(t, err)
	require.Equal(t, int64(7), got.ID)
	require.NotNil(t, got.DeletedAt)
}
