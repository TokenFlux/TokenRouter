package billing_test

import (
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/stretchr/testify/require"
)

func TestRedeemCodeExpirySemantics(t *testing.T) {
	now := time.Now().UTC()
	past := now.Add(-time.Hour)
	future := now.Add(time.Hour)

	tests := []struct {
		name        string
		code        billing.RedeemCode
		wantStatus  string
		wantExpired bool
		wantCanUse  bool
	}{
		{
			name:        "未过期的未使用兑换码可用",
			code:        billing.RedeemCode{Status: billing.StatusUnused, MaxUses: 1, ExpiresAt: &future},
			wantStatus:  billing.StatusUnused,
			wantExpired: false,
			wantCanUse:  true,
		},
		{
			name:        "普通兑换码到期后不可用",
			code:        billing.RedeemCode{Status: billing.StatusUnused, MaxUses: 1, ExpiresAt: &past},
			wantStatus:  billing.StatusExpired,
			wantExpired: true,
			wantCanUse:  false,
		},
		{
			name:        "邀请码到期后也不可用",
			code:        billing.RedeemCode{Type: billing.RedeemTypeInvitation, Status: billing.StatusUnused, MaxUses: 1, ExpiresAt: &past},
			wantStatus:  billing.StatusExpired,
			wantExpired: true,
			wantCanUse:  false,
		},
		{
			name:        "已部分兑换但过期的多次兑换码不可继续使用",
			code:        billing.RedeemCode{Status: billing.StatusActive, MaxUses: 3, UsedCount: 1, ExpiresAt: &past},
			wantStatus:  billing.StatusExpired,
			wantExpired: true,
			wantCanUse:  false,
		},
		{
			name:        "明确标记过期优先于剩余次数",
			code:        billing.RedeemCode{Status: billing.StatusExpired, MaxUses: 0},
			wantStatus:  billing.StatusExpired,
			wantExpired: true,
			wantCanUse:  false,
		},
		{
			name:        "禁用兑换码即使未过期也不可用",
			code:        billing.RedeemCode{Status: billing.StatusDisabled, MaxUses: 1, ExpiresAt: &future},
			wantStatus:  billing.StatusDisabled,
			wantExpired: false,
			wantCanUse:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.wantExpired, tt.code.IsExpired())
			require.Equal(t, tt.wantStatus, tt.code.EffectiveStatus())
			require.Equal(t, tt.wantCanUse, tt.code.CanUse())
		})
	}
}
