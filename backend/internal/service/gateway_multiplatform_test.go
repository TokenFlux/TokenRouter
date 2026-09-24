//go:build unit

package service

import (
	"context"
	"errors"
	"time"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"

	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
)

// mockAccountRepoForPlatform 单平台测试用的 mock
type mockAccountRepoForPlatform struct {
	accounts         []gatewayprovider.ExecutionAccount
	accountsByID     map[int64]*gatewayprovider.ExecutionAccount
	listPlatformFunc func(ctx context.Context, platform string) ([]gatewayprovider.ExecutionAccount, error)
	getByIDCalls     int
}

func (m *mockAccountRepoForPlatform) GetByID(ctx context.Context, id int64) (*gatewayprovider.ExecutionAccount, error) {
	m.getByIDCalls++
	if acc, ok := m.accountsByID[id]; ok {
		return acc, nil
	}
	return nil, errors.New("account not found")
}

func (m *mockAccountRepoForPlatform) GetByIDs(ctx context.Context, ids []int64) ([]*gatewayprovider.ExecutionAccount, error) {
	var result []*gatewayprovider.ExecutionAccount
	for _, id := range ids {
		if acc, ok := m.accountsByID[id]; ok {
			result = append(result, acc)
		}
	}
	return result, nil
}

func (m *mockAccountRepoForPlatform) ExistsByID(ctx context.Context, id int64) (bool, error) {
	if m.accountsByID == nil {
		return false, nil
	}
	_, ok := m.accountsByID[id]
	return ok, nil
}

func (m *mockAccountRepoForPlatform) ListSchedulableByPlatform(ctx context.Context, platform string) ([]gatewayprovider.ExecutionAccount, error) {
	if m.listPlatformFunc != nil {
		return m.listPlatformFunc(ctx, platform)
	}
	var result []gatewayprovider.ExecutionAccount
	for _, acc := range m.accounts {
		if acc.Record.Platform == platform && acc.View().IsSchedulable() {
			result = append(result, acc)
		}
	}
	return result, nil
}

func (m *mockAccountRepoForPlatform) ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]gatewayprovider.ExecutionAccount, error) {
	return m.ListSchedulableByPlatform(ctx, platform)
}

// Stub methods to implement AccountRepository interface
func (m *mockAccountRepoForPlatform) Create(ctx context.Context, account *gatewayprovider.ExecutionAccount) error {
	return nil
}
func (m *mockAccountRepoForPlatform) GetByCRSAccountID(ctx context.Context, crsAccountID string) (*gatewayprovider.ExecutionAccount, error) {
	return nil, nil
}

func (m *mockAccountRepoForPlatform) FindByExtraField(ctx context.Context, key string, value any) ([]gatewayprovider.ExecutionAccount, error) {
	return nil, nil
}

func (m *mockAccountRepoForPlatform) ListCRSAccountIDs(ctx context.Context) (map[string]int64, error) {
	return nil, nil
}
func (m *mockAccountRepoForPlatform) Update(ctx context.Context, account *gatewayprovider.ExecutionAccount) error {
	return nil
}
func (m *mockAccountRepoForPlatform) Delete(ctx context.Context, id int64) error { return nil }
func (m *mockAccountRepoForPlatform) List(ctx context.Context, params pagination.PaginationParams) ([]gatewayprovider.ExecutionAccount, *pagination.PaginationResult, error) {
	return nil, nil, nil
}
func (m *mockAccountRepoForPlatform) ListWithFilters(ctx context.Context, params pagination.PaginationParams, platform, accountType, status, search string, groupID int64, privacyMode string) ([]gatewayprovider.ExecutionAccount, *pagination.PaginationResult, error) {
	return nil, nil, nil
}
func (m *mockAccountRepoForPlatform) ListAllWithFilters(ctx context.Context, platform, accountType, status, search string, groupID int64, privacyMode string) ([]gatewayprovider.ExecutionAccount, error) {
	return nil, nil
}
func (m *mockAccountRepoForPlatform) ListByGroup(ctx context.Context, groupID int64) ([]gatewayprovider.ExecutionAccount, error) {
	return nil, nil
}
func (m *mockAccountRepoForPlatform) ListActive(ctx context.Context) ([]gatewayprovider.ExecutionAccount, error) {
	return nil, nil
}
func (m *mockAccountRepoForPlatform) ListByPlatform(ctx context.Context, platform string) ([]gatewayprovider.ExecutionAccount, error) {
	return nil, nil
}
func (m *mockAccountRepoForPlatform) UpdateLastUsed(ctx context.Context, id int64) error {
	return nil
}
func (m *mockAccountRepoForPlatform) BatchUpdateLastUsed(ctx context.Context, updates map[int64]time.Time) error {
	return nil
}
func (m *mockAccountRepoForPlatform) SetError(ctx context.Context, id int64, errorMsg string) error {
	return nil
}
func (m *mockAccountRepoForPlatform) ClearError(ctx context.Context, id int64) error {
	return nil
}
func (m *mockAccountRepoForPlatform) SetSchedulable(ctx context.Context, id int64, schedulable bool) error {
	return nil
}
func (m *mockAccountRepoForPlatform) AutoPauseExpiredAccounts(ctx context.Context, now time.Time) (int64, error) {
	return 0, nil
}
func (m *mockAccountRepoForPlatform) BindGroups(ctx context.Context, accountID int64, groupIDs []int64) error {
	return nil
}
func (m *mockAccountRepoForPlatform) ListSchedulable(ctx context.Context) ([]gatewayprovider.ExecutionAccount, error) {
	return nil, nil
}
func (m *mockAccountRepoForPlatform) ListSchedulableByGroupID(ctx context.Context, groupID int64) ([]gatewayprovider.ExecutionAccount, error) {
	return nil, nil
}
func (m *mockAccountRepoForPlatform) ListSchedulableByPlatforms(ctx context.Context, platforms []string) ([]gatewayprovider.ExecutionAccount, error) {
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
func (m *mockAccountRepoForPlatform) ListSchedulableByGroupIDAndPlatforms(ctx context.Context, groupID int64, platforms []string) ([]gatewayprovider.ExecutionAccount, error) {
	return m.ListSchedulableByPlatforms(ctx, platforms)
}
func (m *mockAccountRepoForPlatform) ListSchedulableUngroupedByPlatform(ctx context.Context, platform string) ([]gatewayprovider.ExecutionAccount, error) {
	return m.ListSchedulableByPlatform(ctx, platform)
}
func (m *mockAccountRepoForPlatform) ListSchedulableUngroupedByPlatforms(ctx context.Context, platforms []string) ([]gatewayprovider.ExecutionAccount, error) {
	return m.ListSchedulableByPlatforms(ctx, platforms)
}
func (m *mockAccountRepoForPlatform) ListModelAvailabilityCandidates(_ context.Context, groupID *int64, platforms []string, includeGrouped bool) ([]gatewayprovider.ExecutionAccount, error) {
	platformSet := make(map[string]struct{}, len(platforms))
	for _, platform := range platforms {
		platformSet[platform] = struct{}{}
	}
	result := make([]gatewayprovider.ExecutionAccount, 0, len(m.accounts))
	for _, acc := range m.accounts {
		if _, ok := platformSet[acc.Record.Platform]; !ok || acc.Record.Status != billing.StatusActive || !acc.Record.Schedulable {
			continue
		}
		if groupID != nil {
			inGroup := false
			for _, accountGroup := range acc.Record.AccountGroups {
				if accountGroup.GroupID == *groupID {
					inGroup = true
					break
				}
			}
			if !inGroup {
				continue
			}
		} else if !includeGrouped && (len(acc.Record.AccountGroups) > 0 || len(acc.Record.GroupIDs) > 0) {
			continue
		}
		result = append(result, acc)
	}
	return result, nil
}
func (m *mockAccountRepoForPlatform) SetRateLimited(ctx context.Context, id int64, resetAt time.Time) error {
	return nil
}
func (m *mockAccountRepoForPlatform) SetModelRateLimit(ctx context.Context, id int64, scope string, resetAt time.Time, reason ...string) error {
	return nil
}
func (m *mockAccountRepoForPlatform) SetOverloaded(ctx context.Context, id int64, until time.Time) error {
	return nil
}
func (m *mockAccountRepoForPlatform) SetTempUnschedulable(ctx context.Context, id int64, until time.Time, reason string) error {
	return nil
}
func (m *mockAccountRepoForPlatform) ClearTempUnschedulable(ctx context.Context, id int64) error {
	return nil
}
func (m *mockAccountRepoForPlatform) ClearRateLimit(ctx context.Context, id int64) error {
	return nil
}
func (m *mockAccountRepoForPlatform) ClearAntigravityQuotaScopes(ctx context.Context, id int64) error {
	return nil
}
func (m *mockAccountRepoForPlatform) ClearModelRateLimits(ctx context.Context, id int64) error {
	return nil
}
func (m *mockAccountRepoForPlatform) UpdateSessionWindow(ctx context.Context, id int64, start, end *time.Time, status string) error {
	return nil
}
func (m *mockAccountRepoForPlatform) UpdateSessionWindowEnd(ctx context.Context, id int64, end time.Time) error {
	return nil
}
func (m *mockAccountRepoForPlatform) UpdateExtra(ctx context.Context, id int64, updates map[string]any) error {
	return nil
}
func (m *mockAccountRepoForPlatform) BulkUpdate(ctx context.Context, ids []int64, updates accountcore.AccountBulkUpdate) (int64, error) {
	return 0, nil
}

func (m *mockAccountRepoForPlatform) IncrementQuotaUsed(ctx context.Context, id int64, amount float64) error {
	return nil
}

func (m *mockAccountRepoForPlatform) ResetQuotaUsedAndClearRateLimitCooldown(ctx context.Context, id int64) error {
	return nil
}

func (m *mockAccountRepoForPlatform) RevertProxyFallback(ctx context.Context, accountID int64) error {
	return nil
}

func (m *mockAccountRepoForPlatform) ListShadowsByParent(ctx context.Context, parentID int64) ([]*gatewayprovider.ExecutionAccount, error) {
	return nil, nil
}

// Verify interface implementation
var _ gatewayprovider.ExecutionAccountStore = (*mockAccountRepoForPlatform)(nil)
