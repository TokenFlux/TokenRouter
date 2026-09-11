//go:build unit

package billing

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRedeemService_InvalidateRedeemCaches_AuthCache(t *testing.T) {
	invalidator := &redeemAuthInvalidatorStub{}
	svc := &RedeemService{authCacheInvalidator: invalidator}

	svc.invalidateRedeemCaches(context.Background(), 11, &RedeemCode{Type: RedeemTypeBalance})
	svc.invalidateRedeemCaches(context.Background(), 11, &RedeemCode{Type: RedeemTypeConcurrency})
	planID := int64(3)
	svc.invalidateRedeemCaches(context.Background(), 11, &RedeemCode{Type: RedeemTypeSubscription, PlanID: &planID})

	require.Equal(t, []int64{11, 11, 11}, invalidator.userIDs)
}

// redeemAuthInvalidatorStub 只记录权益变更后的用户认证失效。
type redeemAuthInvalidatorStub struct{ userIDs []int64 }

func (s *redeemAuthInvalidatorStub) InvalidateAuthCacheByUserID(_ context.Context, id int64) {
	s.userIDs = append(s.userIDs, id)
}
