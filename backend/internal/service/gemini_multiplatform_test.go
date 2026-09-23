//go:build unit

package service

import (
	"context"
	"errors"
	"time"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// mockAccountRepoForGemini Gemini 测试用的 mock
type mockAccountRepoForGemini struct {
	accounts           []gatewayprovider.ExecutionAccount
	accountsByID       map[int64]*gatewayprovider.ExecutionAccount
	listByGroupFunc    func(ctx context.Context, groupID int64, platforms []string) ([]gatewayprovider.ExecutionAccount, error)
	listByPlatformFunc func(ctx context.Context, platforms []string) ([]gatewayprovider.ExecutionAccount, error)
}

func (m *mockAccountRepoForGemini) GetByID(ctx context.Context, id int64) (*gatewayprovider.ExecutionAccount, error) {
	if acc, ok := m.accountsByID[id]; ok {
		return acc, nil
	}
	return nil, errors.New("account not found")
}

func (m *mockAccountRepoForGemini) GetByIDs(ctx context.Context, ids []int64) ([]*gatewayprovider.ExecutionAccount, error) {
	var result []*gatewayprovider.ExecutionAccount
	for _, id := range ids {
		if acc, ok := m.accountsByID[id]; ok {
			result = append(result, acc)
		}
	}
	return result, nil
}

func (m *mockAccountRepoForGemini) ExistsByID(ctx context.Context, id int64) (bool, error) {
	if m.accountsByID == nil {
		return false, nil
	}
	_, ok := m.accountsByID[id]
	return ok, nil
}

func (m *mockAccountRepoForGemini) ListSchedulableByPlatform(ctx context.Context, platform string) ([]gatewayprovider.ExecutionAccount, error) {
	var result []gatewayprovider.ExecutionAccount
	for _, acc := range m.accounts {
		if acc.Record.Platform == platform && acc.View().IsSchedulable() {
			result = append(result, acc)
		}
	}
	return result, nil
}

func (m *mockAccountRepoForGemini) ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]gatewayprovider.ExecutionAccount, error) {
	// 测试时不区分 groupID，直接按 platform 过滤
	return m.ListSchedulableByPlatform(ctx, platform)
}

// Stub methods to implement AccountRepository interface
func (m *mockAccountRepoForGemini) Create(ctx context.Context, account *gatewayprovider.ExecutionAccount) error {
	return nil
}
func (m *mockAccountRepoForGemini) GetByCRSAccountID(ctx context.Context, crsAccountID string) (*gatewayprovider.ExecutionAccount, error) {
	return nil, nil
}

func (m *mockAccountRepoForGemini) FindByExtraField(ctx context.Context, key string, value any) ([]gatewayprovider.ExecutionAccount, error) {
	return nil, nil
}

func (m *mockAccountRepoForGemini) ListCRSAccountIDs(ctx context.Context) (map[string]int64, error) {
	return nil, nil
}
func (m *mockAccountRepoForGemini) Update(ctx context.Context, account *gatewayprovider.ExecutionAccount) error {
	return nil
}
func (m *mockAccountRepoForGemini) Delete(ctx context.Context, id int64) error { return nil }
func (m *mockAccountRepoForGemini) List(ctx context.Context, params pagination.PaginationParams) ([]gatewayprovider.ExecutionAccount, *pagination.PaginationResult, error) {
	return nil, nil, nil
}
func (m *mockAccountRepoForGemini) ListWithFilters(ctx context.Context, params pagination.PaginationParams, platform, accountType, status, search string, groupID int64, privacyMode string) ([]gatewayprovider.ExecutionAccount, *pagination.PaginationResult, error) {
	return nil, nil, nil
}
func (m *mockAccountRepoForGemini) ListAllWithFilters(ctx context.Context, platform, accountType, status, search string, groupID int64, privacyMode string) ([]gatewayprovider.ExecutionAccount, error) {
	return nil, nil
}
func (m *mockAccountRepoForGemini) ListByGroup(ctx context.Context, groupID int64) ([]gatewayprovider.ExecutionAccount, error) {
	return nil, nil
}
func (m *mockAccountRepoForGemini) ListActive(ctx context.Context) ([]gatewayprovider.ExecutionAccount, error) {
	return nil, nil
}
func (m *mockAccountRepoForGemini) ListByPlatform(ctx context.Context, platform string) ([]gatewayprovider.ExecutionAccount, error) {
	return nil, nil
}
func (m *mockAccountRepoForGemini) UpdateLastUsed(ctx context.Context, id int64) error { return nil }
func (m *mockAccountRepoForGemini) BatchUpdateLastUsed(ctx context.Context, updates map[int64]time.Time) error {
	return nil
}
func (m *mockAccountRepoForGemini) SetError(ctx context.Context, id int64, errorMsg string) error {
	return nil
}
func (m *mockAccountRepoForGemini) ClearError(ctx context.Context, id int64) error {
	return nil
}
func (m *mockAccountRepoForGemini) SetSchedulable(ctx context.Context, id int64, schedulable bool) error {
	return nil
}
func (m *mockAccountRepoForGemini) AutoPauseExpiredAccounts(ctx context.Context, now time.Time) (int64, error) {
	return 0, nil
}
func (m *mockAccountRepoForGemini) BindGroups(ctx context.Context, accountID int64, groupIDs []int64) error {
	return nil
}
func (m *mockAccountRepoForGemini) ListSchedulable(ctx context.Context) ([]gatewayprovider.ExecutionAccount, error) {
	return nil, nil
}
func (m *mockAccountRepoForGemini) ListSchedulableByGroupID(ctx context.Context, groupID int64) ([]gatewayprovider.ExecutionAccount, error) {
	return nil, nil
}
func (m *mockAccountRepoForGemini) ListSchedulableByPlatforms(ctx context.Context, platforms []string) ([]gatewayprovider.ExecutionAccount, error) {
	if m.listByPlatformFunc != nil {
		return m.listByPlatformFunc(ctx, platforms)
	}
	var result []gatewayprovider.ExecutionAccount
	platformSet := make(map[string]bool)
	for _, p := range platforms {
		platformSet[p] = true
	}
	for _, acc := range m.accounts {
		if platformSet[acc.Record.Platform] && acc.View().IsSchedulable() {
			result = append(result, acc)
		}
	}
	return result, nil
}
func (m *mockAccountRepoForGemini) ListSchedulableByGroupIDAndPlatforms(ctx context.Context, groupID int64, platforms []string) ([]gatewayprovider.ExecutionAccount, error) {
	if m.listByGroupFunc != nil {
		return m.listByGroupFunc(ctx, groupID, platforms)
	}
	return m.ListSchedulableByPlatforms(ctx, platforms)
}
func (m *mockAccountRepoForGemini) ListSchedulableUngroupedByPlatform(ctx context.Context, platform string) ([]gatewayprovider.ExecutionAccount, error) {
	return m.ListSchedulableByPlatform(ctx, platform)
}
func (m *mockAccountRepoForGemini) ListSchedulableUngroupedByPlatforms(ctx context.Context, platforms []string) ([]gatewayprovider.ExecutionAccount, error) {
	return m.ListSchedulableByPlatforms(ctx, platforms)
}
func (m *mockAccountRepoForGemini) ListModelAvailabilityCandidates(ctx context.Context, _ *int64, platforms []string, _ bool) ([]gatewayprovider.ExecutionAccount, error) {
	return m.ListSchedulableByPlatforms(ctx, platforms)
}
func (m *mockAccountRepoForGemini) SetRateLimited(ctx context.Context, id int64, resetAt time.Time) error {
	return nil
}
func (m *mockAccountRepoForGemini) SetModelRateLimit(ctx context.Context, id int64, scope string, resetAt time.Time, reason ...string) error {
	return nil
}
func (m *mockAccountRepoForGemini) SetOverloaded(ctx context.Context, id int64, until time.Time) error {
	return nil
}
func (m *mockAccountRepoForGemini) SetTempUnschedulable(ctx context.Context, id int64, until time.Time, reason string) error {
	return nil
}
func (m *mockAccountRepoForGemini) ClearTempUnschedulable(ctx context.Context, id int64) error {
	return nil
}
func (m *mockAccountRepoForGemini) ClearRateLimit(ctx context.Context, id int64) error { return nil }
func (m *mockAccountRepoForGemini) ClearAntigravityQuotaScopes(ctx context.Context, id int64) error {
	return nil
}
func (m *mockAccountRepoForGemini) ClearModelRateLimits(ctx context.Context, id int64) error {
	return nil
}
func (m *mockAccountRepoForGemini) UpdateSessionWindow(ctx context.Context, id int64, start, end *time.Time, status string) error {
	return nil
}
func (m *mockAccountRepoForGemini) UpdateSessionWindowEnd(ctx context.Context, id int64, end time.Time) error {
	return nil
}
func (m *mockAccountRepoForGemini) UpdateExtra(ctx context.Context, id int64, updates map[string]any) error {
	return nil
}
func (m *mockAccountRepoForGemini) BulkUpdate(ctx context.Context, ids []int64, updates accountcore.AccountBulkUpdate) (int64, error) {
	return 0, nil
}

func (m *mockAccountRepoForGemini) IncrementQuotaUsed(ctx context.Context, id int64, amount float64) error {
	return nil
}

func (m *mockAccountRepoForGemini) ResetQuotaUsedAndClearRateLimitCooldown(ctx context.Context, id int64) error {
	return nil
}

func (m *mockAccountRepoForGemini) RevertProxyFallback(ctx context.Context, accountID int64) error {
	return nil
}

func (m *mockAccountRepoForGemini) ListShadowsByParent(ctx context.Context, parentID int64) ([]*gatewayprovider.ExecutionAccount, error) {
	return nil, nil
}

// Verify interface implementation
var _ gatewayprovider.ExecutionAccountStore = (*mockAccountRepoForGemini)(nil)

// mockGroupRepoForGemini Gemini 测试用的 group repo mock
type mockGroupRepoForGemini struct {
	groups           map[int64]*routing.Group
	getByIDCalls     int
	getByIDLiteCalls int
}

func (m *mockGroupRepoForGemini) GetByID(ctx context.Context, id int64) (*routing.Group, error) {
	m.getByIDCalls++
	if g, ok := m.groups[id]; ok {
		return g, nil
	}
	return nil, errors.New("group not found")
}

func (m *mockGroupRepoForGemini) GetByIDLite(ctx context.Context, id int64) (*routing.Group, error) {
	m.getByIDLiteCalls++
	if g, ok := m.groups[id]; ok {
		return g, nil
	}
	return nil, errors.New("group not found")
}

// Stub methods to implement GroupRepository interface
func (m *mockGroupRepoForGemini) Create(ctx context.Context, group *routing.Group) error { return nil }
func (m *mockGroupRepoForGemini) Update(ctx context.Context, group *routing.Group) error { return nil }
func (m *mockGroupRepoForGemini) Delete(ctx context.Context, id int64) error             { return nil }
func (m *mockGroupRepoForGemini) DeleteCascade(ctx context.Context, id int64) ([]int64, error) {
	return nil, nil
}
func (m *mockGroupRepoForGemini) List(ctx context.Context, params pagination.PaginationParams) ([]routing.Group, *pagination.PaginationResult, error) {
	return nil, nil, nil
}
func (m *mockGroupRepoForGemini) ListWithFilters(ctx context.Context, params pagination.PaginationParams, platform, status, search string, isExclusive *bool) ([]routing.Group, *pagination.PaginationResult, error) {
	return nil, nil, nil
}
func (m *mockGroupRepoForGemini) ListActive(ctx context.Context) ([]routing.Group, error) {
	return nil, nil
}
func (m *mockGroupRepoForGemini) ListActiveByPlatform(ctx context.Context, platform string) ([]routing.Group, error) {
	return nil, nil
}
func (m *mockGroupRepoForGemini) ListActiveByPlatformLite(ctx context.Context, platform string) ([]routing.Group, error) {
	return m.ListActiveByPlatform(ctx, platform)
}
func (m *mockGroupRepoForGemini) ExistsByName(ctx context.Context, name string) (bool, error) {
	return false, nil
}
func (m *mockGroupRepoForGemini) GetAccountCount(ctx context.Context, groupID int64) (int64, int64, error) {
	return 0, 0, nil
}
func (m *mockGroupRepoForGemini) DeleteAccountGroupsByGroupID(ctx context.Context, groupID int64) (int64, error) {
	return 0, nil
}

func (m *mockGroupRepoForGemini) BindAccountsToGroup(ctx context.Context, groupID int64, accountIDs []int64) error {
	return nil
}

func (m *mockGroupRepoForGemini) GetAccountIDsByGroupIDs(ctx context.Context, groupIDs []int64) ([]int64, error) {
	return nil, nil
}

func (m *mockGroupRepoForGemini) UpdateSortOrders(ctx context.Context, updates []routing.GroupSortOrderUpdate) error {
	return nil
}

var _ routing.GroupRepository = (*mockGroupRepoForGemini)(nil)
