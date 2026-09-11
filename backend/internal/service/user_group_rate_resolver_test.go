package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

type userGroupRateResolverRepoStub struct {
	UserGroupRateRepository
	rate  *float64
	calls int
}

func (s *userGroupRateResolverRepoStub) GetByUserAndGroup(context.Context, int64, int64) (*float64, error) {
	s.calls++
	return s.rate, nil
}
func TestGatewayServiceGetUserGroupRateMultiplier_FallbacksAndUsesExistingResolver(t *testing.T) {
	var nilSvc *GatewayService
	require.Equal(t, 1.3, nilSvc.getUserGroupRateMultiplier(context.Background(), 101, 202, 1.3))

	rate := 1.9
	repo := &userGroupRateResolverRepoStub{rate: &rate}
	resolver := newUserGroupRateResolver(repo, nil, time.Minute, nil, "service.gateway")
	svc := &GatewayService{userGroupRateResolver: resolver}

	got := svc.getUserGroupRateMultiplier(context.Background(), 101, 202, 1.2)
	require.Equal(t, rate, got)
	require.Equal(t, 1, repo.calls)
}
