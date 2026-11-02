package app

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/identity"
)

// routing_marketplace_test.go verifies the identity boundary for personal catalogs.
// Mutable repository fixtures model revoked grants and disabled accounts while
// checking that administrator roles do not expand grants or share mutable slices
// with routing consumers.

// marketplaceUserRepository supplies current identity state for marketplace permission reads.
type marketplaceUserRepository struct {
	identity.UserRepository                // unused repository method contract
	user                    *identity.User // current stored account snapshot
	err                     error          // injected identity read failure
}

// GetByID returns the current fixture account or its configured read failure.
func (r *marketplaceUserRepository) GetByID(context.Context, int64) (*identity.User, error) {
	return r.user, r.err
}

// TestMarketplaceCurrentGrants reads an administrator account before and after
// permission removal. Only explicit grants are returned, copied independently,
// and missing, disabled, or unreadable accounts reject personal catalog access.
func TestMarketplaceCurrentGrants(t *testing.T) {
	repository := &marketplaceUserRepository{user: &identity.User{ID: 7, Status: "active", Role: "admin", AllowedGroups: []int64{2}}}
	access := marketplaceGroupAccess{users: repository}
	grants, err := access.AllowedGroupIDs(context.Background(), 7)
	require.NoError(t, err)
	require.Equal(t, []int64{2}, grants)
	grants[0] = 99
	require.Equal(t, []int64{2}, repository.user.AllowedGroups)
	repository.user.AllowedGroups = nil
	grants, err = access.AllowedGroupIDs(context.Background(), 7)
	require.NoError(t, err)
	require.Empty(t, grants)
	repository.user.Status = "disabled"
	_, err = access.AllowedGroupIDs(context.Background(), 7)
	require.Error(t, err)
	repository.user = nil
	_, err = access.AllowedGroupIDs(context.Background(), 7)
	require.Error(t, err)
	repository.err = errors.New("identity unavailable")
	_, err = access.AllowedGroupIDs(context.Background(), 7)
	require.ErrorIs(t, err, repository.err)
}
