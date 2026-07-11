//go:build unit

package service

import (
	"context"
	"testing"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

func TestRedeemRejectsInvitationCodeBeforeGrantingBenefits(t *testing.T) {
	ctx := context.Background()
	// 使用 nil 用户仓储，确保邀请码在进入权益发放和用户查询前就被拒绝。
	redeemRepo := &paymentOrderLifecycleRedeemRepo{
		codesByCode: map[string]*RedeemCode{
			"INVITE-001": {
				ID:        1,
				Code:      "INVITE-001",
				Type:      RedeemTypeInvitation,
				Status:    StatusUnused,
				MaxUses:   1,
				UsedCount: 0,
			},
		},
	}
	redeemService := NewRedeemService(redeemRepo, nil, nil, nil, nil, newPaymentOrderLifecycleTestClient(t), nil, nil)

	got, err := redeemService.Redeem(ctx, 2, "INVITE-001")

	require.Nil(t, got)
	require.Error(t, err)
	require.True(t, infraerrors.IsBadRequest(err))
	require.Equal(t, "REDEEM_CODE_UNSUPPORTED_TYPE", infraerrors.Reason(err))
	require.Equal(t, "invitation codes can only be used during registration", infraerrors.Message(err))
	require.Empty(t, redeemRepo.useCalls)
	require.Equal(t, StatusUnused, redeemRepo.codesByCode["INVITE-001"].Status)
	require.Nil(t, redeemRepo.codesByCode["INVITE-001"].UsedBy)
}
