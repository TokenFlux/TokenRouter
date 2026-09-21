//go:build unit

package payment_test

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/stretchr/testify/require"
)

func TestRedeemRejectsInvitationCodeBeforeGrantingBenefits(t *testing.T) {
	ctx := context.Background()
	// 使用 nil 用户仓储，确保邀请码在进入权益发放和用户查询前就被拒绝。
	redeemRepo := &paymentOrderLifecycleRedeemRepo{
		codesByCode: map[string]*billing.RedeemCode{
			"INVITE-001": {
				ID:        1,
				Code:      "INVITE-001",
				Type:      billing.RedeemTypeInvitation,
				Status:    billing.StatusUnused,
				MaxUses:   1,
				UsedCount: 0,
			},
		},
	}
	redeemService := newFulfillmentRedeemService(redeemRepo, nil, newPaymentOrderLifecycleTestClient(t))

	got, err := redeemService.Redeem(ctx, 2, "INVITE-001")

	require.Nil(t, got)
	require.Error(t, err)
	require.True(t, apperror.IsBadRequest(err))
	require.Equal(t, "REDEEM_CODE_UNSUPPORTED_TYPE", apperror.Reason(err))
	require.Equal(t, "invitation codes can only be used during registration", apperror.Message(err))
	require.Empty(t, redeemRepo.useCalls)
	require.Equal(t, billing.StatusUnused, redeemRepo.codesByCode["INVITE-001"].Status)
	require.Nil(t, redeemRepo.codesByCode["INVITE-001"].UsedBy)
}
