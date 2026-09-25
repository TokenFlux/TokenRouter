package dto_test

import (
	"testing"
	"time"

	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/stretchr/testify/require"
)

func TestUserFromServiceShallow_MapsDeletedAt(t *testing.T) {
	ts := time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)

	deleted := UserFromServiceShallow(&identity.User{ID: 1, Email: "d@test.com", DeletedAt: &ts})
	require.NotNil(t, deleted.DeletedAt)
	require.Equal(t, ts, *deleted.DeletedAt)

	active := UserFromServiceShallow(&identity.User{ID: 2, Email: "a@test.com"})
	require.Nil(t, active.DeletedAt, "active user must have nil DeletedAt")
}
