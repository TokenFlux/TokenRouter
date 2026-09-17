package httpapi_test

import (
	"testing"
	"time"

	service "github.com/TokenFlux/TokenRouter/internal/billing"
	native "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	"github.com/stretchr/testify/require"
)

func TestUserSubscriptionFromService_MapsRevokedAt(t *testing.T) {
	ts := time.Date(2026, 7, 2, 10, 0, 0, 0, time.UTC)

	sub := native.UserSubscriptionFromService(&service.UserSubscription{
		ID:        1,
		UserID:    2,
		PlanID:    3,
		Status:    service.SubscriptionStatusRevoked,
		DeletedAt: &ts,
	})

	require.NotNil(t, sub.RevokedAt)
	require.Equal(t, ts, *sub.RevokedAt)
}
