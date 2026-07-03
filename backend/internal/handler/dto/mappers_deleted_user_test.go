package dto

import (
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUserFromServiceShallow_MapsDeletedAt(t *testing.T) {
	ts := time.Date(2026, 5, 28, 10, 0, 0, 0, time.UTC)

	deleted := UserFromServiceShallow(&service.User{ID: 1, Email: "d@test.com", DeletedAt: &ts})
	require.NotNil(t, deleted.DeletedAt)
	require.Equal(t, ts, *deleted.DeletedAt)

	active := UserFromServiceShallow(&service.User{ID: 2, Email: "a@test.com"})
	require.Nil(t, active.DeletedAt, "active user must have nil DeletedAt")
}

func TestUserSubscriptionFromService_MapsRevokedAt(t *testing.T) {
	ts := time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC)

	sub := UserSubscriptionFromService(&service.UserSubscription{
		ID:        1,
		UserID:    2,
		PlanID:    3,
		Status:    service.SubscriptionStatusRevoked,
		DeletedAt: &ts,
	})

	require.NotNil(t, sub.RevokedAt)
	require.Equal(t, ts, *sub.RevokedAt)
}
