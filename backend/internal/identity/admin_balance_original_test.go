//go:build unit

package identity_test

import (
	"context"
	"errors"
	"testing"
	"time"

	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/promotion"
	"github.com/stretchr/testify/require"
)

type balanceUserRepoStub struct {
	*userRepoStub
	adjustErr error
	// changes 记录每次原子余额变更，顺序与调用顺序一致。
	changes []identity.BalanceChange
}

func (s *balanceUserRepoStub) AdjustBalance(ctx context.Context, id int64, delta float64) (identity.BalanceChange, error) {
	return s.apply(func(current float64) float64 { return current + delta })
}

func (s *balanceUserRepoStub) SetBalance(ctx context.Context, id int64, value float64) (identity.BalanceChange, error) {
	return s.apply(func(float64) float64 { return value })
}

func (s *balanceUserRepoStub) apply(next func(current float64) float64) (identity.BalanceChange, error) {
	if s.adjustErr != nil {
		return identity.BalanceChange{}, s.adjustErr
	}
	if s.userRepoStub == nil || s.user == nil {
		return identity.BalanceChange{}, identity.ErrUserNotFound
	}
	change := identity.BalanceChange{Old: s.user.Balance}
	change.New = next(change.Old)
	if change.New < 0 {
		return change, identity.ErrBalanceNegative
	}
	s.user.Balance = change.New
	s.changes = append(s.changes, change)
	return change, nil
}

type balanceRedeemRepoStub struct {
	billing.RedeemCodeRepository
	created []*billing.RedeemCode
}

func (s *balanceRedeemRepoStub) Create(ctx context.Context, code *billing.RedeemCode) error {
	if code == nil {
		return nil
	}
	clone := *code
	s.created = append(s.created, &clone)
	return nil
}

type authCacheInvalidatorStub struct {
	userIDs  []int64
	groupIDs []int64
	keys     []string
}

type adminRechargeAffiliateAccruerStub struct {
	calls  []adminRechargeAffiliateAccrual
	rebate float64
	err    error
}

// adminRechargeAffiliateAccrual 记录测试中收到的返利计提参数。
type adminRechargeAffiliateAccrual struct {
	userID int64
	amount float64
}

func (s *adminRechargeAffiliateAccruerStub) AccrueInviteRebate(_ context.Context, userID int64, amount float64) (float64, error) {
	s.calls = append(s.calls, adminRechargeAffiliateAccrual{userID: userID, amount: amount})
	return s.rebate, s.err
}

func adminRechargeSettingService(enabled bool) identity.AdminUserSettings {
	values := map[string]string{}
	if enabled {
		values[promotion.SettingKeyAffiliateAdminRechargeEnabled] = "true"
	}
	return originalRechargeSettings{runtime: promotion.NewRuntimeSettings(&adminCreationSettingsStore{authSourceDefaultsRepoStub: authSourceDefaultsRepoStub{values: values}})}
}

func (s *authCacheInvalidatorStub) InvalidateAuthCacheByKey(ctx context.Context, key string) {
	s.keys = append(s.keys, key)
}

func (s *authCacheInvalidatorStub) InvalidateAuthCacheByUserID(ctx context.Context, userID int64) {
	s.userIDs = append(s.userIDs, userID)
}

func (s *authCacheInvalidatorStub) InvalidateAuthCacheByGroupID(ctx context.Context, groupID int64) {
	s.groupIDs = append(s.groupIDs, groupID)
}

// 管理员调账必须走原子的 AdjustBalance/SetBalance，而不是"读余额→算新值→整行写回"，
// 后者会把并发的计费扣款覆盖掉。userRepoStub.Update 对未预期的调用会 panic，
// 因此这里同时证明它没被走到。
func TestAdminService_UpdateUserBalance_UsesAtomicPrimitives(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		amount    float64
		want      identity.BalanceChange
	}{
		{name: "add", operation: "add", amount: 5, want: identity.BalanceChange{Old: 10, New: 15}},
		{name: "subtract", operation: "subtract", amount: 4, want: identity.BalanceChange{Old: 10, New: 6}},
		{name: "set", operation: "set", amount: 2, want: identity.BalanceChange{Old: 10, New: 2}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &balanceUserRepoStub{userRepoStub: &userRepoStub{user: &identity.User{ID: 7, Balance: 10}}}
			svc := newOriginalBalanceAdmin(repo, &balanceRedeemRepoStub{}, nil, nil, nil)

			user, err := svc.UpdateUserBalance(context.Background(), 7, tt.amount, tt.operation, "")
			require.NoError(t, err)
			require.Equal(t, []identity.BalanceChange{tt.want}, repo.changes)
			require.Equal(t, tt.want.New, user.Balance)
		})
	}
}

func TestAdminService_UpdateUserBalance_RejectsNegativeResult(t *testing.T) {
	repo := &balanceUserRepoStub{userRepoStub: &userRepoStub{user: &identity.User{ID: 7, Balance: 3}}}
	svc := newOriginalBalanceAdmin(repo, &balanceRedeemRepoStub{}, nil, nil, nil)

	_, err := svc.UpdateUserBalance(context.Background(), 7, 4, "subtract", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "balance cannot be negative")
	require.Empty(t, repo.changes, "refused adjustment must not be applied")
	require.Equal(t, 3.0, repo.user.Balance)
}

func TestAdminService_UpdateUserBalance_RejectsUnknownOperation(t *testing.T) {
	repo := &balanceUserRepoStub{userRepoStub: &userRepoStub{user: &identity.User{ID: 7, Balance: 10}}}
	svc := newOriginalBalanceAdmin(repo, &balanceRedeemRepoStub{}, nil, nil, nil)

	_, err := svc.UpdateUserBalance(context.Background(), 7, 1, "multiply", "")
	require.Error(t, err)
	require.Empty(t, repo.changes)
}

func TestAdminService_UpdateUserBalance_InvalidatesAuthCache(t *testing.T) {
	baseRepo := &userRepoStub{user: &identity.User{ID: 7, Balance: 10}}
	repo := &balanceUserRepoStub{userRepoStub: baseRepo}
	redeemRepo := &balanceRedeemRepoStub{}
	invalidator := &authCacheInvalidatorStub{}
	svc := newOriginalBalanceAdmin(repo, redeemRepo, invalidator, nil, nil)

	_, err := svc.UpdateUserBalance(context.Background(), 7, 5, "add", "")
	require.NoError(t, err)
	require.Equal(t, []int64{7}, invalidator.userIDs)
	require.Len(t, redeemRepo.created, 1)
}

func TestAdminService_UpdateUserBalance_NoChangeNoInvalidate(t *testing.T) {
	baseRepo := &userRepoStub{user: &identity.User{ID: 7, Balance: 10}}
	repo := &balanceUserRepoStub{userRepoStub: baseRepo}
	redeemRepo := &balanceRedeemRepoStub{}
	invalidator := &authCacheInvalidatorStub{}
	svc := newOriginalBalanceAdmin(repo, redeemRepo, invalidator, nil, nil)

	_, err := svc.UpdateUserBalance(context.Background(), 7, 10, "set", "")
	require.NoError(t, err)
	require.Empty(t, invalidator.userIDs)
	require.Empty(t, redeemRepo.created)
}

func TestAdminService_UpdateUserBalance_AdminRechargeAffiliateRebate(t *testing.T) {
	tests := []struct {
		name      string
		enabled   bool
		operation string
		amount    float64
		wantCalls []adminRechargeAffiliateAccrual
	}{
		{
			name:      "disabled by default",
			operation: "add",
			amount:    5,
		},
		{
			name:      "enabled add",
			enabled:   true,
			operation: "add",
			amount:    0.1,
			wantCalls: []adminRechargeAffiliateAccrual{{userID: 7, amount: 0.1}},
		},
		{
			name:      "enabled set increase",
			enabled:   true,
			operation: "set",
			amount:    15,
		},
		{
			name:      "enabled subtract",
			enabled:   true,
			operation: "subtract",
			amount:    5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			baseRepo := &userRepoStub{user: &identity.User{ID: 7, Balance: 10}}
			repo := &balanceUserRepoStub{userRepoStub: baseRepo}
			redeemRepo := &balanceRedeemRepoStub{}
			affiliate := &adminRechargeAffiliateAccruerStub{}
			svc := newOriginalBalanceAdmin(repo, redeemRepo, nil, adminRechargeSettingService(tt.enabled), affiliate)

			_, err := svc.UpdateUserBalance(context.Background(), 7, tt.amount, tt.operation, "")
			require.NoError(t, err)
			require.Equal(t, tt.wantCalls, affiliate.calls)
		})
	}
}

func TestAdminService_UpdateUserBalance_AffiliateFailureDoesNotRollbackRecharge(t *testing.T) {
	baseRepo := &userRepoStub{user: &identity.User{ID: 7, Balance: 10}}
	repo := &balanceUserRepoStub{userRepoStub: baseRepo}
	redeemRepo := &balanceRedeemRepoStub{}
	affiliate := &adminRechargeAffiliateAccruerStub{err: errors.New("affiliate unavailable")}
	svc := newOriginalBalanceAdmin(repo, redeemRepo, nil, adminRechargeSettingService(true), affiliate)

	user, err := svc.UpdateUserBalance(context.Background(), 7, 5, "add", "")
	require.NoError(t, err)
	require.Equal(t, 15.0, user.Balance)
	require.Equal(t, []adminRechargeAffiliateAccrual{{userID: 7, amount: 5}}, affiliate.calls)
	require.Len(t, redeemRepo.created, 1)
}

// CreateUsage 延续原调整记录替身的成功写入语义。
func (*balanceRedeemRepoStub) CreateUsage(context.Context, *billing.RedeemCodeUsage) error {
	return nil
}

// originalRechargeSettings 只组合推广开关读取，不复制设置解释规则。
type originalRechargeSettings struct {
	identity.AdminUserSettings
	runtime *promotion.RuntimeSettings
}

func (s originalRechargeSettings) IsAffiliateAdminRechargeEnabled(ctx context.Context) bool {
	return s.runtime.IsAffiliateAdminRechargeEnabled(ctx)
}

// newOriginalBalanceAdmin 固定唯一余额写入端口和原调整记录事务适配。
func newOriginalBalanceAdmin(users *balanceUserRepoStub, records *balanceRedeemRepoStub, invalidator *authCacheInvalidatorStub, settings identity.AdminUserSettings, affiliate *adminRechargeAffiliateAccruerStub) *identity.UserAdmin {
	d := identity.AdminDependencies{Users: users, Balances: users, Settings: settings, Records: billing.NewRedeemAdmin(records, billingpostgres.NewRedeemAdministrationMutations(nil), time.Now)}
	if invalidator != nil {
		d.Invalidator = invalidator
	}
	if affiliate != nil {
		d.Affiliates = affiliate
	}
	return identity.NewUserAdmin(d)
}
