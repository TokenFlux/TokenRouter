//go:build unit

package selection

import (
	"context"
	"errors"
	"testing"
	"time"

	routingtestkit "github.com/TokenFlux/TokenRouter/internal/routing/testkit"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	logging "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/stretchr/testify/require"
)

// testConfig 返回一个用于测试的默认配置
func testConfig() *config.Config {
	return &config.Config{RunMode: config.RunModeStandard}
}

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

// Verify interface implementation

// mockGatewayCacheForPlatform 单平台测试用的 cache mock
type mockGatewayCacheForPlatform struct {
	sessionBindings map[string]int64
	deletedSessions map[string]int
}

func (m *mockGatewayCacheForPlatform) GetSessionAccountID(ctx context.Context, groupID int64, sessionHash string) (int64, error) {
	if id, ok := m.sessionBindings[sessionHash]; ok {
		return id, nil
	}
	return 0, errors.New("not found")
}

func (m *mockGatewayCacheForPlatform) SetSessionAccountID(ctx context.Context, groupID int64, sessionHash string, accountID int64, ttl time.Duration) error {
	if m.sessionBindings == nil {
		m.sessionBindings = make(map[string]int64)
	}
	m.sessionBindings[sessionHash] = accountID
	return nil
}

func (m *mockGatewayCacheForPlatform) RefreshSessionTTL(ctx context.Context, groupID int64, sessionHash string, ttl time.Duration) error {
	return nil
}

func (m *mockGatewayCacheForPlatform) DeleteSessionAccountID(ctx context.Context, groupID int64, sessionHash string) error {
	if m.sessionBindings == nil {
		return nil
	}
	if m.deletedSessions == nil {
		m.deletedSessions = make(map[string]int)
	}
	m.deletedSessions[sessionHash]++
	delete(m.sessionBindings, sessionHash)
	return nil
}

type mockGroupRepoForGateway struct {
	groups           map[int64]*routing.Group
	getByIDCalls     int
	getByIDLiteCalls int
}

func (m *mockGroupRepoForGateway) GetByID(ctx context.Context, id int64) (*routing.Group, error) {
	m.getByIDCalls++
	if g, ok := m.groups[id]; ok {
		return g, nil
	}
	return nil, routing.ErrGroupNotFound
}

func (m *mockGroupRepoForGateway) GetByIDLite(ctx context.Context, id int64) (*routing.Group, error) {
	m.getByIDLiteCalls++
	if g, ok := m.groups[id]; ok {
		return g, nil
	}
	return nil, routing.ErrGroupNotFound
}

func ptr[T any](v T) *T {
	return &v
}

// TestGatewayService_SelectAccountForModelWithPlatform_Anthropic 测试 anthropic 单平台选择
func TestGatewayService_SelectAccountForModelWithPlatform_Anthropic(t *testing.T) {
	ctx := context.Background()

	repo := &mockAccountRepoForPlatform{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusActive, Schedulable: true}},
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 3, Platform: capability.PlatformAntigravity, Priority: 1, Status: billing.StatusActive, Schedulable: true}}, // 应被隔离
		},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
	}

	cache := &mockGatewayCacheForPlatform{}

	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo}, Shared: Shared{Cache: cache}}, testConfig())

	acc, err := svc.selectAccountForModelWithPlatform(ctx, nil, "", "claude-3-5-sonnet-20241022", nil, capability.PlatformAnthropic)
	require.NoError(t, err)
	require.NotNil(t, acc)
	require.Equal(t, int64(1), acc.Record.ID, "应选择优先级最高的 anthropic 账户")
	require.Equal(t, capability.PlatformAnthropic, acc.Record.Platform, "应只返回 anthropic 平台账户")
}

// TestGatewayService_SelectAccountForModelWithPlatform_Antigravity 测试 antigravity 单平台选择
func TestGatewayService_SelectAccountForModelWithPlatform_Antigravity(t *testing.T) {
	ctx := context.Background()

	repo := &mockAccountRepoForPlatform{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true}}, // 应被隔离
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAntigravity, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
		},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
	}

	cache := &mockGatewayCacheForPlatform{}

	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo}, Shared: Shared{Cache: cache}}, testConfig())

	acc, err := svc.selectAccountForModelWithPlatform(ctx, nil, "", "claude-sonnet-4-5", nil, capability.PlatformAntigravity)
	require.NoError(t, err)
	require.NotNil(t, acc)
	require.Equal(t, int64(2), acc.Record.ID)
	require.Equal(t, capability.PlatformAntigravity, acc.Record.Platform, "应只返回 antigravity 平台账户")
}

// TestGatewayService_SelectAccountForModelWithPlatform_PriorityAndLastUsed 测试优先级和最后使用时间
func TestGatewayService_SelectAccountForModelWithPlatform_PriorityAndLastUsed(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	repo := &mockAccountRepoForPlatform{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, LastUsedAt: ptr(now.Add(-1 * time.Hour))}},
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, LastUsedAt: ptr(now.Add(-2 * time.Hour))}},
		},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
	}

	cache := &mockGatewayCacheForPlatform{}

	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo}, Shared: Shared{Cache: cache}}, testConfig())

	acc, err := svc.selectAccountForModelWithPlatform(ctx, nil, "", "claude-3-5-sonnet-20241022", nil, capability.PlatformAnthropic)
	require.NoError(t, err)
	require.NotNil(t, acc)
	require.Equal(t, int64(2), acc.Record.ID, "同优先级应选择最久未用的账户")
}

func TestGatewayService_SelectAccountForModelWithPlatform_GeminiOAuthPreference(t *testing.T) {
	ctx := context.Background()

	repo := &mockAccountRepoForPlatform{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformGemini, Priority: 1, Status: billing.StatusActive, Schedulable: true, Type: capability.AccountTypeAPIKey}},
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformGemini, Priority: 1, Status: billing.StatusActive, Schedulable: true, Type: capability.AccountTypeOAuth}},
		},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
	}

	cache := &mockGatewayCacheForPlatform{}

	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo}, Shared: Shared{Cache: cache}}, testConfig())

	acc, err := svc.selectAccountForModelWithPlatform(ctx, nil, "", "gemini-2.5-pro", nil, capability.PlatformGemini)
	require.NoError(t, err)
	require.NotNil(t, acc)
	require.Equal(t, int64(2), acc.Record.ID, "同优先级且未使用时应优先选择OAuth账户")
}

// TestGatewayService_SelectAccountForModelWithPlatform_NoAvailableAccounts 测试无可用账户
func TestGatewayService_SelectAccountForModelWithPlatform_NoAvailableAccounts(t *testing.T) {
	ctx := context.Background()

	repo := &mockAccountRepoForPlatform{
		accounts:     []gatewayprovider.ExecutionAccount{},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
	}

	cache := &mockGatewayCacheForPlatform{}

	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo}, Shared: Shared{Cache: cache}}, testConfig())

	acc, err := svc.selectAccountForModelWithPlatform(ctx, nil, "", "claude-3-5-sonnet-20241022", nil, capability.PlatformAnthropic)
	require.Error(t, err)
	require.Nil(t, acc)
	require.ErrorIs(t, err, scheduler.ErrNoAvailableAccounts)
}

// TestGatewayService_SelectAccountForModelWithPlatform_AllExcluded 测试所有账户被排除
func TestGatewayService_SelectAccountForModelWithPlatform_AllExcluded(t *testing.T) {
	ctx := context.Background()

	repo := &mockAccountRepoForPlatform{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
		},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
	}

	cache := &mockGatewayCacheForPlatform{}

	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo}, Shared: Shared{Cache: cache}}, testConfig())

	excludedIDs := map[int64]struct{}{1: {}, 2: {}}
	acc, err := svc.selectAccountForModelWithPlatform(ctx, nil, "", "claude-3-5-sonnet-20241022", excludedIDs, capability.PlatformAnthropic)
	require.Error(t, err)
	require.Nil(t, acc)
}

// TestGatewayService_SelectAccountForModelWithPlatform_Schedulability 测试账户可调度性检查
func TestGatewayService_SelectAccountForModelWithPlatform_Schedulability(t *testing.T) {
	ctx := context.Background()
	now := time.Now()

	tests := []struct {
		name       string
		accounts   []gatewayprovider.ExecutionAccount
		expectedID int64
	}{
		{
			name: "过载账户被跳过",
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, OverloadUntil: ptr(now.Add(1 * time.Hour))}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusActive, Schedulable: true}},
			},
			expectedID: 2,
		},
		{
			name: "限流账户被跳过",
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, RateLimitResetAt: ptr(now.Add(1 * time.Hour))}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusActive, Schedulable: true}},
			},
			expectedID: 2,
		},
		{
			name: "非active账户被跳过",
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: "error", Schedulable: true}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusActive, Schedulable: true}},
			},
			expectedID: 2,
		},
		{
			name: "schedulable=false被跳过",
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: false}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusActive, Schedulable: true}},
			},
			expectedID: 2,
		},
		{
			name: "过期的过载账户可调度",
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, OverloadUntil: ptr(now.Add(-1 * time.Hour))}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusActive, Schedulable: true}},
			},
			expectedID: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &mockAccountRepoForPlatform{
				accounts:     tt.accounts,
				accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
			}
			for i := range repo.accounts {
				repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
			}

			cache := &mockGatewayCacheForPlatform{}

			svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo}, Shared: Shared{Cache: cache}}, testConfig())

			acc, err := svc.selectAccountForModelWithPlatform(ctx, nil, "", "claude-3-5-sonnet-20241022", nil, capability.PlatformAnthropic)
			require.NoError(t, err)
			require.NotNil(t, acc)
			require.Equal(t, tt.expectedID, acc.Record.ID)
		})
	}
}

// TestGatewayService_SelectAccountForModelWithPlatform_StickySession 测试粘性会话
func TestGatewayService_SelectAccountForModelWithPlatform_StickySession(t *testing.T) {
	ctx := context.Background()

	t.Run("粘性会话命中-同平台", func(t *testing.T) {
		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusActive, Schedulable: true}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForPlatform{
			sessionBindings: map[string]int64{"session-123": 1},
		}

		svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo}, Shared: Shared{Cache: cache}}, testConfig())

		acc, err := svc.selectAccountForModelWithPlatform(ctx, nil, "session-123", "claude-3-5-sonnet-20241022", nil, capability.PlatformAnthropic)
		require.NoError(t, err)
		require.NotNil(t, acc)
		require.Equal(t, int64(1), acc.Record.ID, "应返回粘性会话绑定的账户")
	})

	t.Run("粘性会话不匹配平台-降级选择", func(t *testing.T) {
		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAntigravity, Priority: 2, Status: billing.StatusActive, Schedulable: true}}, // 粘性会话绑定但平台不匹配
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForPlatform{
			sessionBindings: map[string]int64{"session-123": 1}, // 绑定 antigravity 账户
		}

		svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo}, Shared: Shared{Cache: cache}}, testConfig())

		// 请求 anthropic 平台，但粘性会话绑定的是 antigravity 账户
		acc, err := svc.selectAccountForModelWithPlatform(ctx, nil, "session-123", "claude-3-5-sonnet-20241022", nil, capability.PlatformAnthropic)
		require.NoError(t, err)
		require.NotNil(t, acc)
		require.Equal(t, int64(2), acc.Record.ID, "粘性会话账户平台不匹配，应降级选择同平台账户")
		require.Equal(t, capability.PlatformAnthropic, acc.Record.Platform)
	})

	t.Run("粘性会话账户被排除-降级选择", func(t *testing.T) {
		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusActive, Schedulable: true}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForPlatform{
			sessionBindings: map[string]int64{"session-123": 1},
		}

		svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo}, Shared: Shared{Cache: cache}}, testConfig())

		excludedIDs := map[int64]struct{}{1: {}}
		acc, err := svc.selectAccountForModelWithPlatform(ctx, nil, "session-123", "claude-3-5-sonnet-20241022", excludedIDs, capability.PlatformAnthropic)
		require.NoError(t, err)
		require.NotNil(t, acc)
		require.Equal(t, int64(2), acc.Record.ID, "粘性会话账户被排除，应选择其他账户")
	})

	t.Run("粘性会话账户不可调度-降级选择", func(t *testing.T) {
		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 2, Status: "error", Schedulable: true}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForPlatform{
			sessionBindings: map[string]int64{"session-123": 1},
		}

		svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo}, Shared: Shared{Cache: cache}}, testConfig())

		acc, err := svc.selectAccountForModelWithPlatform(ctx, nil, "session-123", "claude-3-5-sonnet-20241022", nil, capability.PlatformAnthropic)
		require.NoError(t, err)
		require.NotNil(t, acc)
		require.Equal(t, int64(2), acc.Record.ID, "粘性会话账户不可调度，应选择其他账户")
	})
}

func TestGatewayService_SelectAccountForModelWithExclusions_ForcePlatform(t *testing.T) {
	ctx := context.Background()
	ctx = apikey.WithForcePlatform(ctx, capability.PlatformAntigravity)

	repo := &mockAccountRepoForPlatform{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAntigravity, Priority: 2, Status: billing.StatusActive, Schedulable: true}},
		},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
	}

	cache := &mockGatewayCacheForPlatform{}

	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo}, Shared: Shared{Cache: cache}}, testConfig())

	acc, err := svc.SelectAccountForModelWithExclusions(ctx, nil, "", "claude-sonnet-4-5", nil)
	require.NoError(t, err)
	require.NotNil(t, acc)
	require.Equal(t, int64(2), acc.Record.ID)
	require.Equal(t, capability.PlatformAntigravity, acc.Record.Platform)
}

func TestGatewayService_SelectAccountForModelWithPlatform_RoutedStickySessionClears(t *testing.T) {
	ctx := context.Background()
	groupID := int64(10)
	requestedModel := "claude-3-5-sonnet-20241022"

	repo := &mockAccountRepoForPlatform{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusDisabled, Schedulable: true}},
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
		},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
	}

	cache := &mockGatewayCacheForPlatform{
		sessionBindings: map[string]int64{"session-123": 1},
	}

	groupRepo := &mockGroupRepoForGateway{
		groups: map[int64]*routing.Group{
			groupID: {
				ID:                  groupID,
				Name:                "route-group",
				Platform:            capability.PlatformAnthropic,
				Status:              billing.StatusActive,
				Hydrated:            true,
				ModelRoutingEnabled: true,
				ModelRouting: map[string][]int64{
					requestedModel: {1, 2},
				},
			},
		},
	}

	svc := newGenericSelectionForTest(GenericDependencies{
		Reads: Reads{
			Groups:   groupRepo,
			Accounts: repo,
		},
		Shared: Shared{Cache: cache},
	}, testConfig())

	acc, err := svc.selectAccountForModelWithPlatform(ctx, &groupID, "session-123", requestedModel, nil, capability.PlatformAnthropic)
	require.NoError(t, err)
	require.NotNil(t, acc)
	require.Equal(t, int64(2), acc.Record.ID)
	require.Equal(t, 1, cache.deletedSessions["session-123"])
	require.Equal(t, int64(2), cache.sessionBindings["session-123"])
}

func TestGatewayService_SelectAccountForModelWithPlatform_RoutedStickySessionHit(t *testing.T) {
	ctx := context.Background()
	groupID := int64(11)
	requestedModel := "claude-3-5-sonnet-20241022"

	repo := &mockAccountRepoForPlatform{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusActive, Schedulable: true}},
		},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
	}

	cache := &mockGatewayCacheForPlatform{
		sessionBindings: map[string]int64{"session-456": 1},
	}

	groupRepo := &mockGroupRepoForGateway{
		groups: map[int64]*routing.Group{
			groupID: {
				ID:                  groupID,
				Name:                "route-group-hit",
				Platform:            capability.PlatformAnthropic,
				Status:              billing.StatusActive,
				Hydrated:            true,
				ModelRoutingEnabled: true,
				ModelRouting: map[string][]int64{
					requestedModel: {1, 2},
				},
			},
		},
	}

	svc := newGenericSelectionForTest(GenericDependencies{
		Reads: Reads{
			Groups:   groupRepo,
			Accounts: repo,
		},
		Shared: Shared{Cache: cache},
	}, testConfig())

	acc, err := svc.selectAccountForModelWithPlatform(ctx, &groupID, "session-456", requestedModel, nil, capability.PlatformAnthropic)
	require.NoError(t, err)
	require.NotNil(t, acc)
	require.Equal(t, int64(1), acc.Record.ID)
}

func TestGatewayService_SelectAccountForModelWithPlatform_RoutedFallbackToNormal(t *testing.T) {
	ctx := context.Background()
	groupID := int64(12)
	requestedModel := "claude-3-5-sonnet-20241022"

	repo := &mockAccountRepoForPlatform{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusActive, Schedulable: true}},
		},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
	}

	cache := &mockGatewayCacheForPlatform{}

	groupRepo := &mockGroupRepoForGateway{
		groups: map[int64]*routing.Group{
			groupID: {
				ID:                  groupID,
				Name:                "route-fallback",
				Platform:            capability.PlatformAnthropic,
				Status:              billing.StatusActive,
				Hydrated:            true,
				ModelRoutingEnabled: true,
				ModelRouting: map[string][]int64{
					requestedModel: {99},
				},
			},
		},
	}

	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo, Groups: groupRepo}, Shared: Shared{Cache: cache}}, testConfig())

	acc, err := svc.selectAccountForModelWithPlatform(ctx, &groupID, "", requestedModel, nil, capability.PlatformAnthropic)
	require.NoError(t, err)
	require.NotNil(t, acc)
	require.Equal(t, int64(1), acc.Record.ID)
}

func TestGatewayService_SelectAccountForModelWithPlatform_NoModelSupport(t *testing.T) {
	ctx := context.Background()

	repo := &mockAccountRepoForPlatform{
		accounts: []gatewayprovider.ExecutionAccount{
			{
				Record: accountcore.Record{
					LoadLocation: time.LoadLocation, ID: 1,
					Platform:    capability.PlatformAnthropic,
					Priority:    1,
					Status:      billing.StatusActive,
					Schedulable: true,
					Credentials: map[string]any{"model_mapping": map[string]any{"claude-3-5-haiku-20241022": "claude-3-5-haiku-20241022"}},
				},
			},
		},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
	}

	cache := &mockGatewayCacheForPlatform{}

	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo}, Shared: Shared{Cache: cache}}, testConfig())

	acc, err := svc.selectAccountForModelWithPlatform(ctx, nil, "", "claude-3-5-sonnet-20241022", nil, capability.PlatformAnthropic)
	require.Error(t, err)
	require.Nil(t, acc)
	var modelErr *routing.GroupModelUnsupportedError
	require.True(t, errors.As(err, &modelErr))
	require.Equal(t, capability.PlatformAnthropic, modelErr.Platform)
	require.Equal(t, "claude-3-5-sonnet-20241022", modelErr.RequestedModel)
	require.Equal(t, []string{"claude-3-5-haiku-20241022"}, modelErr.AvailableModels)
	require.Contains(t, err.Error(), `The current group does not support the requested model "claude-3-5-sonnet-20241022"`)
	require.Contains(t, err.Error(), "Available models: claude-3-5-haiku-20241022")
}

func TestGatewayService_SelectAccountForModelWithPlatform_ModelRateLimitedNotGroupUnsupported(t *testing.T) {
	ctx := context.Background()
	resetAt := time.Now().Add(10 * time.Minute).Format(time.RFC3339)

	repo := &mockAccountRepoForPlatform{
		accounts: []gatewayprovider.ExecutionAccount{
			{
				Record: accountcore.Record{
					LoadLocation: time.LoadLocation, ID: 1,
					Platform:    capability.PlatformAnthropic,
					Priority:    1,
					Status:      billing.StatusActive,
					Schedulable: true,
					Credentials: map[string]any{"model_mapping": map[string]any{"claude-3-5-sonnet-20241022": "claude-3-5-sonnet-20241022"}},
					Extra: map[string]any{
						"model_rate_limits": map[string]any{
							"claude-3-5-sonnet-20241022": map[string]any{"rate_limit_reset_at": resetAt},
						},
					},
				},
			},
			{
				Record: accountcore.Record{
					LoadLocation: time.LoadLocation, ID: 2,
					Platform:    capability.PlatformAnthropic,
					Priority:    2,
					Status:      billing.StatusActive,
					Schedulable: true,
					Credentials: map[string]any{"model_mapping": map[string]any{"claude-3-5-haiku-20241022": "claude-3-5-haiku-20241022"}},
				},
			},
		},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
	}

	svc := newGenericSelectionForTest(GenericDependencies{
		Reads:  Reads{Accounts: repo},
		Shared: Shared{Cache: &mockGatewayCacheForPlatform{}},
	}, testConfig())

	acc, err := svc.selectAccountForModelWithPlatform(ctx, nil, "", "claude-3-5-sonnet-20241022", nil, capability.PlatformAnthropic)
	require.Error(t, err)
	require.Nil(t, acc)
	var modelErr *routing.GroupModelUnsupportedError
	require.False(t, errors.As(err, &modelErr))
	require.ErrorIs(t, err, scheduler.ErrNoAvailableAccounts)
}

func TestGatewayService_SelectAccountForModelWithPlatform_GeminiPreferOAuth(t *testing.T) {
	ctx := context.Background()

	repo := &mockAccountRepoForPlatform{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformGemini, Priority: 1, Status: billing.StatusActive, Schedulable: true, Type: capability.AccountTypeAPIKey}},
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformGemini, Priority: 1, Status: billing.StatusActive, Schedulable: true, Type: capability.AccountTypeOAuth}},
		},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
	}

	cache := &mockGatewayCacheForPlatform{}

	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo}, Shared: Shared{Cache: cache}}, testConfig())

	acc, err := svc.selectAccountForModelWithPlatform(ctx, nil, "", "gemini-2.5-pro", nil, capability.PlatformGemini)
	require.NoError(t, err)
	require.NotNil(t, acc)
	require.Equal(t, int64(2), acc.Record.ID)
}

func TestGatewayService_SelectAccountForModelWithPlatform_GeminiAPIKeyModelMappingFilter(t *testing.T) {
	ctx := context.Background()

	repo := &mockAccountRepoForPlatform{
		accounts: []gatewayprovider.ExecutionAccount{
			{
				Record: accountcore.Record{
					LoadLocation: time.LoadLocation, ID: 1,
					Platform:    capability.PlatformGemini,
					Type:        capability.AccountTypeAPIKey,
					Priority:    1,
					Status:      billing.StatusActive,
					Schedulable: true,
					Credentials: map[string]any{"model_mapping": map[string]any{"gemini-2.5-pro": "gemini-2.5-pro"}},
				},
			},
			{
				Record: accountcore.Record{
					LoadLocation: time.LoadLocation, ID: 2,
					Platform:    capability.PlatformGemini,
					Type:        capability.AccountTypeAPIKey,
					Priority:    2,
					Status:      billing.StatusActive,
					Schedulable: true,
					Credentials: map[string]any{"model_mapping": map[string]any{"gemini-2.5-flash": "gemini-2.5-flash"}},
				},
			},
		},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
	}

	cache := &mockGatewayCacheForPlatform{}

	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo}, Shared: Shared{Cache: cache}}, testConfig())

	acc, err := svc.selectAccountForModelWithPlatform(ctx, nil, "", "gemini-2.5-flash", nil, capability.PlatformGemini)
	require.NoError(t, err)
	require.NotNil(t, acc)
	require.Equal(t, int64(2), acc.Record.ID, "应过滤不支持请求模型的 APIKey 账号")

	acc, err = svc.selectAccountForModelWithPlatform(ctx, nil, "", "gemini-3-pro-preview", nil, capability.PlatformGemini)
	require.Error(t, err)
	require.Nil(t, acc)
	var modelErr *routing.GroupModelUnsupportedError
	require.True(t, errors.As(err, &modelErr))
	require.Equal(t, capability.PlatformGemini, modelErr.Platform)
	require.Equal(t, "gemini-3-pro-preview", modelErr.RequestedModel)
	require.Equal(t, []string{"gemini-2.5-flash", "gemini-2.5-pro"}, modelErr.AvailableModels)
	require.Contains(t, err.Error(), `The current group does not support the requested model "gemini-3-pro-preview"`)
	require.Contains(t, err.Error(), "Available models: gemini-2.5-flash, gemini-2.5-pro")
}

func TestGatewayService_SelectAccountForModelWithPlatform_StickyInGroup(t *testing.T) {
	ctx := context.Background()
	groupID := int64(50)

	repo := &mockAccountRepoForPlatform{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, AccountGroups: []accountcore.GroupMembership{{GroupID: groupID}}}},
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusActive, Schedulable: true, AccountGroups: []accountcore.GroupMembership{{GroupID: groupID}}}},
		},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
	}

	cache := &mockGatewayCacheForPlatform{
		sessionBindings: map[string]int64{"session-group": 1},
	}

	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo}, Shared: Shared{Cache: cache}}, testConfig())

	acc, err := svc.selectAccountForModelWithPlatform(ctx, &groupID, "session-group", "", nil, capability.PlatformAnthropic)
	require.NoError(t, err)
	require.NotNil(t, acc)
	require.Equal(t, int64(1), acc.Record.ID)
}

func TestGatewayService_SelectAccountForModelWithPlatform_StickyModelMismatchFallback(t *testing.T) {
	ctx := context.Background()

	repo := &mockAccountRepoForPlatform{
		accounts: []gatewayprovider.ExecutionAccount{
			{
				Record: accountcore.Record{
					LoadLocation: time.LoadLocation, ID: 1,
					Platform:    capability.PlatformAnthropic,
					Priority:    1,
					Status:      billing.StatusActive,
					Schedulable: true,
					Credentials: map[string]any{"model_mapping": map[string]any{"claude-3-5-haiku-20241022": "claude-3-5-haiku-20241022"}},
				},
			},
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusActive, Schedulable: true}},
		},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
	}

	cache := &mockGatewayCacheForPlatform{
		sessionBindings: map[string]int64{"session-miss": 1},
	}

	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo}, Shared: Shared{Cache: cache}}, testConfig())

	acc, err := svc.selectAccountForModelWithPlatform(ctx, nil, "session-miss", "claude-3-5-sonnet-20241022", nil, capability.PlatformAnthropic)
	require.NoError(t, err)
	require.NotNil(t, acc)
	require.Equal(t, int64(2), acc.Record.ID)
}

func TestGatewayService_SelectAccountForModelWithPlatform_PreferNeverUsed(t *testing.T) {
	ctx := context.Background()
	lastUsed := time.Now().Add(-1 * time.Hour)

	repo := &mockAccountRepoForPlatform{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, LastUsedAt: &lastUsed}},
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
		},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
	}

	cache := &mockGatewayCacheForPlatform{}

	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo}, Shared: Shared{Cache: cache}}, testConfig())

	acc, err := svc.selectAccountForModelWithPlatform(ctx, nil, "", "claude-3-5-sonnet-20241022", nil, capability.PlatformAnthropic)
	require.NoError(t, err)
	require.NotNil(t, acc)
	require.Equal(t, int64(2), acc.Record.ID)
}

func TestGatewayService_SelectAccountForModelWithPlatform_NoAccounts(t *testing.T) {
	ctx := context.Background()
	repo := &mockAccountRepoForPlatform{
		accounts:     []gatewayprovider.ExecutionAccount{},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
	}

	cache := &mockGatewayCacheForPlatform{}

	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo}, Shared: Shared{Cache: cache}}, testConfig())

	acc, err := svc.selectAccountForModelWithPlatform(ctx, nil, "", "", nil, capability.PlatformAnthropic)
	require.Error(t, err)
	require.Nil(t, acc)
	require.ErrorIs(t, err, scheduler.ErrNoAvailableAccounts)
}

func TestGatewayService_isModelSupportedByAccount(t *testing.T) {
	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{}, Shared: Shared{}}, nil)

	tests := []struct {
		name     string
		account  *gatewayprovider.ExecutionAccount
		model    string
		expected bool
	}{
		{
			name:     "Antigravity平台-支持默认映射中的claude模型",
			account:  &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAntigravity}},
			model:    "claude-sonnet-4-5",
			expected: true,
		},
		{
			name:     "Antigravity平台-不支持非默认映射中的claude模型",
			account:  &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAntigravity}},
			model:    "claude-3-5-sonnet-20241022",
			expected: false,
		},
		{
			name:     "Antigravity平台-支持gemini模型",
			account:  &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAntigravity}},
			model:    "gemini-2.5-flash",
			expected: true,
		},
		{
			name:     "Antigravity平台-不支持gpt模型",
			account:  &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAntigravity}},
			model:    "gpt-4",
			expected: false,
		},
		{
			name:     "Anthropic平台-无映射配置-支持所有模型",
			account:  &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic}},
			model:    "claude-3-5-sonnet-20241022",
			expected: true,
		},
		{
			name: "Anthropic平台-有映射配置-未命中映射时按透传支持模型",
			account: &gatewayprovider.ExecutionAccount{
				Record: accountcore.Record{
					LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic,
					Credentials: map[string]any{"model_mapping": map[string]any{"claude-opus-4": "x"}},
				},
			},
			model:    "claude-3-5-sonnet-20241022",
			expected: true,
		},
		{
			name: "Anthropic平台-有映射配置-支持配置的模型",
			account: &gatewayprovider.ExecutionAccount{
				Record: accountcore.Record{
					LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic,
					Credentials: map[string]any{"model_mapping": map[string]any{"claude-3-5-sonnet-20241022": "x"}},
				},
			},
			model:    "claude-3-5-sonnet-20241022",
			expected: true,
		},
		{
			name:     "Gemini平台-无映射配置-支持所有模型",
			account:  &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformGemini, Type: capability.AccountTypeAPIKey}},
			model:    "gemini-2.5-flash",
			expected: true,
		},
		{
			name: "Gemini平台-有映射配置-未命中映射时按透传支持模型",
			account: &gatewayprovider.ExecutionAccount{
				Record: accountcore.Record{
					LoadLocation: time.LoadLocation, Platform: capability.PlatformGemini,
					Type: capability.AccountTypeAPIKey,
					Credentials: map[string]any{
						"model_mapping": map[string]any{"gemini-2.5-pro": "upstream-model"},
					},
				},
			},
			model:    "gemini-2.5-flash",
			expected: true,
		},
		{
			name: "Gemini平台-有映射配置-支持配置的模型",
			account: &gatewayprovider.ExecutionAccount{
				Record: accountcore.Record{
					LoadLocation: time.LoadLocation, Platform: capability.PlatformGemini,
					Type: capability.AccountTypeAPIKey,
					Credentials: map[string]any{
						"model_mapping": map[string]any{"gemini-2.5-pro": "gemini-2.5-pro"},
					},
				},
			},
			model:    "gemini-2.5-pro",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := svc.isModelSupportedByAccount(tt.account, tt.model)
			require.Equal(t, tt.expected, got)
		})
	}
}

// TestGatewayService_selectAccountWithMixedScheduling 测试混合调度
func TestGatewayService_selectAccountWithMixedScheduling(t *testing.T) {
	ctx := context.Background()

	t.Run("混合调度-Gemini优先选择OAuth账户", func(t *testing.T) {
		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformGemini, Priority: 1, Status: billing.StatusActive, Schedulable: true, Type: capability.AccountTypeAPIKey}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformGemini, Priority: 1, Status: billing.StatusActive, Schedulable: true, Type: capability.AccountTypeOAuth}},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForPlatform{}

		svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo}, Shared: Shared{Cache: cache}}, testConfig())

		acc, err := svc.selectAccountWithMixedScheduling(ctx, nil, "", "gemini-2.5-pro", nil, capability.PlatformGemini)
		require.NoError(t, err)
		require.NotNil(t, acc)
		require.Equal(t, int64(2), acc.Record.ID, "同优先级且未使用时应优先选择OAuth账户")
	})

	t.Run("混合调度-包含启用mixed_scheduling的antigravity账户", func(t *testing.T) {
		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusActive, Schedulable: true}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAntigravity, Priority: 1, Status: billing.StatusActive, Schedulable: true, Extra: map[string]any{"mixed_scheduling": true}}},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForPlatform{}

		svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo}, Shared: Shared{Cache: cache}}, testConfig())

		acc, err := svc.selectAccountWithMixedScheduling(ctx, nil, "", "claude-sonnet-4-5", nil, capability.PlatformAnthropic)
		require.NoError(t, err)
		require.NotNil(t, acc)
		require.Equal(t, int64(2), acc.Record.ID, "应选择优先级最高的账户（包含启用混合调度的antigravity）")
	})

	t.Run("混合调度-Gemini家族限流后跳过Antigravity账户", func(t *testing.T) {
		resetAt := time.Now().Add(10 * time.Minute).Format(time.RFC3339)
		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{
					Record: accountcore.Record{
						LoadLocation: time.LoadLocation, ID: 1,
						Platform:    capability.PlatformAntigravity,
						Priority:    1,
						Status:      billing.StatusActive,
						Schedulable: true,
						Extra: map[string]any{
							"mixed_scheduling": true, "model_rate_limits": map[string]any{
								"antigravity:gemini": map[string]any{
									"rate_limit_reset_at": resetAt,
								},
							},
						},
					},
				},
				{
					Record: accountcore.Record{
						LoadLocation: time.LoadLocation, ID: 2,
						Platform:    capability.PlatformAntigravity,
						Priority:    1,
						Status:      billing.StatusActive,
						Schedulable: true,
						Extra: map[string]any{
							"mixed_scheduling": true, "model_rate_limits": map[string]any{
								"antigravity:gemini": map[string]any{
									"rate_limit_reset_at": resetAt,
								},
							},
						},
					},
				},
				{
					Record: accountcore.Record{
						LoadLocation: time.LoadLocation, ID: 3,
						Platform:    capability.PlatformAntigravity,
						Priority:    2,
						Status:      billing.StatusActive,
						Schedulable: true,
						Extra:       map[string]any{"mixed_scheduling": true},
					},
				},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		svc := newGenericSelectionForTest(GenericDependencies{
			Reads:  Reads{Accounts: repo},
			Shared: Shared{Cache: &mockGatewayCacheForPlatform{}},
		}, testConfig())

		acc, err := svc.selectAccountWithMixedScheduling(ctx, nil, "", "gemini-3-pro-preview", nil, capability.PlatformGemini)
		require.NoError(t, err)
		require.NotNil(t, acc)
		require.Equal(t, int64(3), acc.Record.ID)
	})

	t.Run("混合调度-Gemini家族限流不影响Claude调度", func(t *testing.T) {
		resetAt := time.Now().Add(10 * time.Minute).Format(time.RFC3339)
		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{
					Record: accountcore.Record{
						LoadLocation: time.LoadLocation, ID: 1,
						Platform:    capability.PlatformAntigravity,
						Priority:    1,
						Status:      billing.StatusActive,
						Schedulable: true,
						Extra: map[string]any{
							"mixed_scheduling": true, "model_rate_limits": map[string]any{
								"antigravity:gemini": map[string]any{
									"rate_limit_reset_at": resetAt,
								},
							},
						},
					},
				},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusActive, Schedulable: true}},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		svc := newGenericSelectionForTest(GenericDependencies{
			Reads:  Reads{Accounts: repo},
			Shared: Shared{Cache: &mockGatewayCacheForPlatform{}},
		}, testConfig())

		acc, err := svc.selectAccountWithMixedScheduling(ctx, nil, "", "claude-sonnet-4-5", nil, capability.PlatformAnthropic)
		require.NoError(t, err)
		require.NotNil(t, acc)
		require.Equal(t, int64(1), acc.Record.ID)
	})

	t.Run("混合调度-路由优先选择路由账号", func(t *testing.T) {
		groupID := int64(30)
		requestedModel := "claude-sonnet-4-5"
		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAntigravity, Priority: 2, Status: billing.StatusActive, Schedulable: true, Extra: map[string]any{"mixed_scheduling": true}}},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForPlatform{}

		groupRepo := &mockGroupRepoForGateway{
			groups: map[int64]*routing.Group{
				groupID: {
					ID:                  groupID,
					Name:                "route-mixed-select",
					Platform:            capability.PlatformAnthropic,
					Status:              billing.StatusActive,
					Hydrated:            true,
					ModelRoutingEnabled: true,
					ModelRouting: map[string][]int64{
						requestedModel: {2},
					},
				},
			},
		}

		svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo, Groups: groupRepo}, Shared: Shared{Cache: cache}}, testConfig())

		acc, err := svc.selectAccountWithMixedScheduling(ctx, &groupID, "", requestedModel, nil, capability.PlatformAnthropic)
		require.NoError(t, err)
		require.NotNil(t, acc)
		require.Equal(t, int64(2), acc.Record.ID)
	})

	t.Run("混合调度-路由粘性命中", func(t *testing.T) {
		groupID := int64(31)
		requestedModel := "claude-sonnet-4-5"
		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAntigravity, Priority: 2, Status: billing.StatusActive, Schedulable: true, Extra: map[string]any{"mixed_scheduling": true}, AccountGroups: []accountcore.GroupMembership{{GroupID: groupID}}}},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForPlatform{
			sessionBindings: map[string]int64{"session-777": 2},
		}

		groupRepo := &mockGroupRepoForGateway{
			groups: map[int64]*routing.Group{
				groupID: {
					ID:                  groupID,
					Name:                "route-mixed-sticky",
					Platform:            capability.PlatformAnthropic,
					Status:              billing.StatusActive,
					Hydrated:            true,
					ModelRoutingEnabled: true,
					ModelRouting: map[string][]int64{
						requestedModel: {2},
					},
				},
			},
		}

		svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo, Groups: groupRepo}, Shared: Shared{Cache: cache}}, testConfig())

		acc, err := svc.selectAccountWithMixedScheduling(ctx, &groupID, "session-777", requestedModel, nil, capability.PlatformAnthropic)
		require.NoError(t, err)
		require.NotNil(t, acc)
		require.Equal(t, int64(2), acc.Record.ID)
	})

	t.Run("混合调度-路由账号缺失回退", func(t *testing.T) {
		groupID := int64(32)
		requestedModel := "claude-3-5-sonnet-20241022"
		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAntigravity, Priority: 2, Status: billing.StatusActive, Schedulable: true, Extra: map[string]any{"mixed_scheduling": true}}},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForPlatform{}

		groupRepo := &mockGroupRepoForGateway{
			groups: map[int64]*routing.Group{
				groupID: {
					ID:                  groupID,
					Name:                "route-mixed-miss",
					Platform:            capability.PlatformAnthropic,
					Status:              billing.StatusActive,
					Hydrated:            true,
					ModelRoutingEnabled: true,
					ModelRouting: map[string][]int64{
						requestedModel: {99},
					},
				},
			},
		}

		svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo, Groups: groupRepo}, Shared: Shared{Cache: cache}}, testConfig())

		acc, err := svc.selectAccountWithMixedScheduling(ctx, &groupID, "", requestedModel, nil, capability.PlatformAnthropic)
		require.NoError(t, err)
		require.NotNil(t, acc)
		require.Equal(t, int64(1), acc.Record.ID)
	})

	t.Run("混合调度-路由账号未启用mixed_scheduling回退", func(t *testing.T) {
		groupID := int64(33)
		requestedModel := "claude-3-5-sonnet-20241022"
		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAntigravity, Priority: 2, Status: billing.StatusActive, Schedulable: true}}, // 未启用 mixed_scheduling
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForPlatform{}

		groupRepo := &mockGroupRepoForGateway{
			groups: map[int64]*routing.Group{
				groupID: {
					ID:                  groupID,
					Name:                "route-mixed-disabled",
					Platform:            capability.PlatformAnthropic,
					Status:              billing.StatusActive,
					Hydrated:            true,
					ModelRoutingEnabled: true,
					ModelRouting: map[string][]int64{
						requestedModel: {2},
					},
				},
			},
		}

		svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo, Groups: groupRepo}, Shared: Shared{Cache: cache}}, testConfig())

		acc, err := svc.selectAccountWithMixedScheduling(ctx, &groupID, "", requestedModel, nil, capability.PlatformAnthropic)
		require.NoError(t, err)
		require.NotNil(t, acc)
		require.Equal(t, int64(1), acc.Record.ID)
	})

	t.Run("混合调度-路由过滤覆盖", func(t *testing.T) {
		groupID := int64(35)
		requestedModel := "claude-3-5-sonnet-20241022"
		resetAt := time.Now().Add(10 * time.Minute)
		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: false}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 3, Platform: capability.PlatformAntigravity, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
				{
					Record: accountcore.Record{
						LoadLocation: time.LoadLocation, ID: 4,
						Platform:    capability.PlatformAnthropic,
						Priority:    1,
						Status:      billing.StatusActive,
						Schedulable: true,
						Extra: map[string]any{
							"model_rate_limits": map[string]any{
								"claude-3-5-sonnet-20241022": map[string]any{
									"rate_limit_reset_at": resetAt.Format(time.RFC3339),
								},
							},
						},
					},
				},
				{
					Record: accountcore.Record{
						LoadLocation: time.LoadLocation, ID: 5,
						Platform:    capability.PlatformAnthropic,
						Priority:    1,
						Status:      billing.StatusActive,
						Schedulable: true,
						Credentials: map[string]any{"model_mapping": map[string]any{"claude-3-5-haiku-20241022": "claude-3-5-haiku-20241022"}},
					},
				},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 6, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusActive, Schedulable: true}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 7, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForPlatform{}

		groupRepo := &mockGroupRepoForGateway{
			groups: map[int64]*routing.Group{
				groupID: {
					ID:                  groupID,
					Name:                "route-mixed-filter",
					Platform:            capability.PlatformAnthropic,
					Status:              billing.StatusActive,
					Hydrated:            true,
					ModelRoutingEnabled: true,
					ModelRouting: map[string][]int64{
						requestedModel: {1, 2, 3, 4, 5, 6, 7},
					},
				},
			},
		}

		svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo, Groups: groupRepo}, Shared: Shared{Cache: cache}}, testConfig())

		excluded := map[int64]struct{}{1: {}}
		acc, err := svc.selectAccountWithMixedScheduling(ctx, &groupID, "", requestedModel, excluded, capability.PlatformAnthropic)
		require.NoError(t, err)
		require.NotNil(t, acc)
		require.Equal(t, int64(7), acc.Record.ID)
	})

	t.Run("混合调度-粘性命中分组账号", func(t *testing.T) {
		groupID := int64(34)
		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, AccountGroups: []accountcore.GroupMembership{{GroupID: groupID}}}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusActive, Schedulable: true, AccountGroups: []accountcore.GroupMembership{{GroupID: groupID}}}},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForPlatform{
			sessionBindings: map[string]int64{"session-group": 1},
		}

		groupRepo := &mockGroupRepoForGateway{
			groups: map[int64]*routing.Group{
				groupID: {
					ID:       groupID,
					Platform: capability.PlatformAnthropic,
					Status:   billing.StatusActive,
					Hydrated: true,
				},
			},
		}

		svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo, Groups: groupRepo}, Shared: Shared{Cache: cache}}, testConfig())

		acc, err := svc.selectAccountWithMixedScheduling(ctx, &groupID, "session-group", "claude-3-5-sonnet-20241022", nil, capability.PlatformAnthropic)
		require.NoError(t, err)
		require.NotNil(t, acc)
		require.Equal(t, int64(1), acc.Record.ID)
	})

	t.Run("混合调度-过滤未启用mixed_scheduling的antigravity账户", func(t *testing.T) {
		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusActive, Schedulable: true}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAntigravity, Priority: 1, Status: billing.StatusActive, Schedulable: true}}, // 未启用 mixed_scheduling
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForPlatform{}

		svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo}, Shared: Shared{Cache: cache}}, testConfig())

		acc, err := svc.selectAccountWithMixedScheduling(ctx, nil, "", "claude-3-5-sonnet-20241022", nil, capability.PlatformAnthropic)
		require.NoError(t, err)
		require.NotNil(t, acc)
		require.Equal(t, int64(1), acc.Record.ID, "未启用mixed_scheduling的antigravity账户应被过滤")
		require.Equal(t, capability.PlatformAnthropic, acc.Record.Platform)
	})

	t.Run("混合调度-粘性会话命中启用mixed_scheduling的antigravity账户", func(t *testing.T) {
		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAntigravity, Priority: 2, Status: billing.StatusActive, Schedulable: true, Extra: map[string]any{"mixed_scheduling": true}}},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForPlatform{
			sessionBindings: map[string]int64{"session-123": 2},
		}

		svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo}, Shared: Shared{Cache: cache}}, testConfig())

		acc, err := svc.selectAccountWithMixedScheduling(ctx, nil, "session-123", "claude-sonnet-4-5", nil, capability.PlatformAnthropic)
		require.NoError(t, err)
		require.NotNil(t, acc)
		require.Equal(t, int64(2), acc.Record.ID, "应返回粘性会话绑定的启用mixed_scheduling的antigravity账户")
	})

	t.Run("混合调度-粘性会话命中未启用mixed_scheduling的antigravity账户-降级选择", func(t *testing.T) {
		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAntigravity, Priority: 2, Status: billing.StatusActive, Schedulable: true}}, // 未启用 mixed_scheduling
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForPlatform{
			sessionBindings: map[string]int64{"session-123": 2},
		}

		svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo}, Shared: Shared{Cache: cache}}, testConfig())

		acc, err := svc.selectAccountWithMixedScheduling(ctx, nil, "session-123", "claude-3-5-sonnet-20241022", nil, capability.PlatformAnthropic)
		require.NoError(t, err)
		require.NotNil(t, acc)
		require.Equal(t, int64(1), acc.Record.ID, "粘性会话绑定的账户未启用mixed_scheduling，应降级选择anthropic账户")
	})

	t.Run("混合调度-粘性会话不可调度-清理并回退", func(t *testing.T) {
		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAntigravity, Priority: 1, Status: billing.StatusDisabled, Schedulable: true, Extra: map[string]any{"mixed_scheduling": true}}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusActive, Schedulable: true}},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForPlatform{
			sessionBindings: map[string]int64{"session-123": 1},
		}

		svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo}, Shared: Shared{Cache: cache}}, testConfig())

		acc, err := svc.selectAccountWithMixedScheduling(ctx, nil, "session-123", "claude-3-5-sonnet-20241022", nil, capability.PlatformAnthropic)
		require.NoError(t, err)
		require.NotNil(t, acc)
		require.Equal(t, int64(2), acc.Record.ID)
		require.Equal(t, 1, cache.deletedSessions["session-123"])
		require.Equal(t, int64(2), cache.sessionBindings["session-123"])
	})

	t.Run("混合调度-路由粘性不可调度-清理并回退", func(t *testing.T) {
		groupID := int64(12)
		requestedModel := "claude-3-5-sonnet-20241022"
		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAntigravity, Priority: 1, Status: billing.StatusDisabled, Schedulable: true, Extra: map[string]any{"mixed_scheduling": true}}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusActive, Schedulable: true}},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForPlatform{
			sessionBindings: map[string]int64{"session-123": 1},
		}

		groupRepo := &mockGroupRepoForGateway{
			groups: map[int64]*routing.Group{
				groupID: {
					ID:                  groupID,
					Name:                "route-mixed",
					Platform:            capability.PlatformAnthropic,
					Status:              billing.StatusActive,
					Hydrated:            true,
					ModelRoutingEnabled: true,
					ModelRouting: map[string][]int64{
						requestedModel: {1, 2},
					},
				},
			},
		}

		svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo, Groups: groupRepo}, Shared: Shared{Cache: cache}}, testConfig())

		acc, err := svc.selectAccountWithMixedScheduling(ctx, &groupID, "session-123", requestedModel, nil, capability.PlatformAnthropic)
		require.NoError(t, err)
		require.NotNil(t, acc)
		require.Equal(t, int64(2), acc.Record.ID)
		require.Equal(t, 1, cache.deletedSessions["session-123"])
		require.Equal(t, int64(2), cache.sessionBindings["session-123"])
	})

	t.Run("混合调度-仅有启用mixed_scheduling的antigravity账户", func(t *testing.T) {
		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAntigravity, Priority: 1, Status: billing.StatusActive, Schedulable: true, Extra: map[string]any{"mixed_scheduling": true}}},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForPlatform{}

		svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo}, Shared: Shared{Cache: cache}}, testConfig())

		acc, err := svc.selectAccountWithMixedScheduling(ctx, nil, "", "claude-sonnet-4-5", nil, capability.PlatformAnthropic)
		require.NoError(t, err)
		require.NotNil(t, acc)
		require.Equal(t, int64(1), acc.Record.ID)
		require.Equal(t, capability.PlatformAntigravity, acc.Record.Platform)
	})

	t.Run("混合调度-无可用账户", func(t *testing.T) {
		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAntigravity, Priority: 1, Status: billing.StatusActive, Schedulable: true}}, // 未启用 mixed_scheduling
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForPlatform{}

		svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo}, Shared: Shared{Cache: cache}}, testConfig())

		acc, err := svc.selectAccountWithMixedScheduling(ctx, nil, "", "claude-3-5-sonnet-20241022", nil, capability.PlatformAnthropic)
		require.Error(t, err)
		require.Nil(t, acc)
		require.ErrorIs(t, err, scheduler.ErrNoAvailableAccounts)
	})

	t.Run("混合调度-不支持模型返回错误", func(t *testing.T) {
		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{
					Record: accountcore.Record{
						LoadLocation: time.LoadLocation, ID: 1,
						Platform:    capability.PlatformAnthropic,
						Priority:    1,
						Status:      billing.StatusActive,
						Schedulable: true,
						Credentials: map[string]any{"model_mapping": map[string]any{"claude-3-5-haiku-20241022": "claude-3-5-haiku-20241022"}},
					},
				},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForPlatform{}

		svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo}, Shared: Shared{Cache: cache}}, testConfig())

		acc, err := svc.selectAccountWithMixedScheduling(ctx, nil, "", "claude-3-5-sonnet-20241022", nil, capability.PlatformAnthropic)
		require.Error(t, err)
		require.Nil(t, acc)
		var modelErr *routing.GroupModelUnsupportedError
		require.True(t, errors.As(err, &modelErr))
		require.Equal(t, capability.PlatformAnthropic, modelErr.Platform)
		require.Equal(t, "claude-3-5-sonnet-20241022", modelErr.RequestedModel)
		require.Equal(t, []string{"claude-3-5-haiku-20241022"}, modelErr.AvailableModels)
		require.Contains(t, err.Error(), `The current group does not support the requested model "claude-3-5-sonnet-20241022"`)
		require.Contains(t, err.Error(), "Available models: claude-3-5-haiku-20241022")
	})

	t.Run("混合调度-优先未使用账号", func(t *testing.T) {
		lastUsed := time.Now().Add(-2 * time.Hour)
		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, LastUsedAt: &lastUsed}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForPlatform{}

		svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo}, Shared: Shared{Cache: cache}}, testConfig())

		acc, err := svc.selectAccountWithMixedScheduling(ctx, nil, "", "claude-3-5-sonnet-20241022", nil, capability.PlatformAnthropic)
		require.NoError(t, err)
		require.NotNil(t, acc)
		require.Equal(t, int64(2), acc.Record.ID)
	})
}

// TestAccount_IsMixedSchedulingEnabled 测试混合调度开关检查
func TestAccount_IsMixedSchedulingEnabled(t *testing.T) {
	tests := []struct {
		name     string
		account  gatewayprovider.ExecutionAccount
		expected bool
	}{
		{
			name:     "非antigravity平台-返回false",
			account:  gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAnthropic}},
			expected: false,
		},
		{
			name:     "antigravity平台-无extra-返回false",
			account:  gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAntigravity}},
			expected: false,
		},
		{
			name:     "antigravity平台-extra无mixed_scheduling-返回false",
			account:  gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAntigravity, Extra: map[string]any{}}},
			expected: false,
		},
		{
			name:     "antigravity平台-mixed_scheduling=false-返回false",
			account:  gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAntigravity, Extra: map[string]any{"mixed_scheduling": false}}},
			expected: false,
		},
		{
			name:     "antigravity平台-mixed_scheduling=true-返回true",
			account:  gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAntigravity, Extra: map[string]any{"mixed_scheduling": true}}},
			expected: true,
		},
		{
			name:     "antigravity平台-mixed_scheduling非bool类型-返回false",
			account:  gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAntigravity, Extra: map[string]any{"mixed_scheduling": "true"}}},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.account.View().IsMixedSchedulingEnabled()
			require.Equal(t, tt.expected, got)
		})
	}
}

type mockConcurrencyCache struct {
	acquireAccountCalls int
	loadBatchCalls      int
	acquireResults      map[int64]bool
	loadBatchErr        error
	loadMap             map[int64]*scheduler.AccountLoadInfo
	waitCounts          map[int64]int
	skipDefaultLoad     bool
}

func (m *mockConcurrencyCache) AcquireAccountSlot(ctx context.Context, accountID int64, maxConcurrency int, requestID string) (bool, error) {
	m.acquireAccountCalls++
	if m.acquireResults != nil {
		if result, ok := m.acquireResults[accountID]; ok {
			return result, nil
		}
	}
	return true, nil
}

func (m *mockConcurrencyCache) ReleaseAccountSlot(ctx context.Context, accountID int64, requestID string) error {
	return nil
}

func (m *mockConcurrencyCache) GetAccountConcurrency(ctx context.Context, accountID int64) (int, error) {
	return 0, nil
}

func (m *mockConcurrencyCache) GetAccountConcurrencyBatch(ctx context.Context, accountIDs []int64) (map[int64]int, error) {
	result := make(map[int64]int, len(accountIDs))
	for _, accountID := range accountIDs {
		result[accountID] = 0
	}
	return result, nil
}

func (m *mockConcurrencyCache) IncrementAccountWaitCount(ctx context.Context, accountID int64, maxWait int) (bool, error) {
	return true, nil
}

func (m *mockConcurrencyCache) DecrementAccountWaitCount(ctx context.Context, accountID int64) error {
	return nil
}

func (m *mockConcurrencyCache) GetAccountWaitingCount(ctx context.Context, accountID int64) (int, error) {
	if m.waitCounts != nil {
		if count, ok := m.waitCounts[accountID]; ok {
			return count, nil
		}
	}
	return 0, nil
}

func (m *mockConcurrencyCache) AcquireUserSlot(ctx context.Context, userID int64, maxConcurrency int, requestID string) (bool, error) {
	return true, nil
}

func (m *mockConcurrencyCache) ReleaseUserSlot(ctx context.Context, userID int64, requestID string) error {
	return nil
}

func (m *mockConcurrencyCache) GetUserConcurrency(ctx context.Context, userID int64) (int, error) {
	return 0, nil
}

func (m *mockConcurrencyCache) IncrementWaitCount(ctx context.Context, userID int64, maxWait int) (bool, error) {
	return true, nil
}

func (m *mockConcurrencyCache) DecrementWaitCount(ctx context.Context, userID int64) error {
	return nil
}

func (m *mockConcurrencyCache) GetAccountsLoadBatch(ctx context.Context, accounts []scheduler.AccountWithConcurrency) (map[int64]*scheduler.AccountLoadInfo, error) {
	m.loadBatchCalls++
	if m.loadBatchErr != nil {
		return nil, m.loadBatchErr
	}
	result := make(map[int64]*scheduler.AccountLoadInfo, len(accounts))
	if m.skipDefaultLoad && m.loadMap != nil {
		for _, acc := range accounts {
			if load, ok := m.loadMap[acc.ID]; ok {
				result[acc.ID] = load
			}
		}
		return result, nil
	}
	for _, acc := range accounts {
		if m.loadMap != nil {
			if load, ok := m.loadMap[acc.ID]; ok {
				result[acc.ID] = load
				continue
			}
		}
		result[acc.ID] = &scheduler.AccountLoadInfo{
			AccountID:          acc.ID,
			CurrentConcurrency: 0,
			WaitingCount:       0,
			LoadRate:           0,
		}
	}
	return result, nil
}

func (m *mockConcurrencyCache) CleanupExpiredAccountSlots(ctx context.Context, accountID int64) error {
	return nil
}

func (m *mockConcurrencyCache) CleanupExpiredAccountSlotKeys(ctx context.Context) error {
	return nil
}

func (m *mockConcurrencyCache) CleanupStaleProcessSlots(ctx context.Context, activeRequestPrefix string) error {
	return nil
}

func (m *mockConcurrencyCache) GetUsersLoadBatch(ctx context.Context, users []scheduler.UserWithConcurrency) (map[int64]*scheduler.UserLoadInfo, error) {
	result := make(map[int64]*scheduler.UserLoadInfo, len(users))
	for _, user := range users {
		result[user.ID] = &scheduler.UserLoadInfo{
			UserID:             user.ID,
			CurrentConcurrency: 0,
			WaitingCount:       0,
			LoadRate:           0,
		}
	}
	return result, nil
}

func TestSelectAccountWithLoadAwareness_FiltersUpstreamRestrictedAccounts(t *testing.T) {
	for _, loadBatchEnabled := range []bool{false, true} {
		loadMode := "旧版调度"
		if loadBatchEnabled {
			loadMode = "负载批量调度"
		}
		for _, modelRoutingEnabled := range []bool{false, true} {
			stickyMode := "普通粘性账号"
			if modelRoutingEnabled {
				stickyMode = "模型路由粘性账号"
			}
			t.Run(loadMode+"/"+stickyMode, func(t *testing.T) {
				groupID := int64(4210)
				pricingConfig := routingtestkit.Configuration{
					ID:                 76,
					Status:             billing.StatusActive,
					RestrictModels:     true,
					BillingModelSource: routing.BillingModelSourceUpstream,
					ModelMapping: map[string]map[string]string{
						capability.PlatformAnthropic: {"client-alias": "group-model"},
					},
					ModelPricing: []routing.ModelPricingEntry{{
						Platform: capability.PlatformAnthropic,
						Models:   []string{"allowed-upstream"},
					}},
				}
				accounts := []gatewayprovider.ExecutionAccount{
					{
						Record: accountcore.Record{
							LoadLocation: time.LoadLocation, ID: 1,
							Platform:    capability.PlatformAnthropic,
							Priority:    1,
							Status:      billing.StatusActive,
							Schedulable: true,
							Concurrency: 5,
							AccountGroups: []accountcore.GroupMembership{{
								AccountID: 1,
								GroupID:   groupID,
							}},
							Credentials: map[string]any{"model_mapping": map[string]any{"group-model": "blocked-upstream"}},
						},
					},
					{
						Record: accountcore.Record{
							LoadLocation: time.LoadLocation, ID: 2,
							Platform:    capability.PlatformAnthropic,
							Priority:    2,
							Status:      billing.StatusActive,
							Schedulable: true,
							Concurrency: 5,
							AccountGroups: []accountcore.GroupMembership{{
								AccountID: 2,
								GroupID:   groupID,
							}},
							Credentials: map[string]any{"model_mapping": map[string]any{"group-model": "allowed-upstream"}},
						},
					},
				}
				accountRepo := &mockAccountRepoForPlatform{accounts: accounts, accountsByID: map[int64]*gatewayprovider.ExecutionAccount{}}
				for i := range accountRepo.accounts {
					accountRepo.accountsByID[accountRepo.accounts[i].Record.ID] = &accountRepo.accounts[i]
				}
				group := &routing.Group{
					ID:                  groupID,
					Platform:            capability.PlatformAnthropic,
					Status:              billing.StatusActive,
					Hydrated:            true,
					ModelRoutingEnabled: modelRoutingEnabled,
				}
				if modelRoutingEnabled {
					group.ModelRouting = map[string][]int64{"group-model": {1, 2}}
				}

				cfg := testConfig()
				cfg.Gateway.Scheduling.LoadBatchEnabled = loadBatchEnabled
				svc := newGenericSelectionForTest(GenericDependencies{
					Reads: Reads{
						Accounts: accountRepo,

						Groups: &mockGroupRepoForGateway{groups: map[int64]*routing.Group{groupID: group}},
					},
					Shared: Shared{
						Concurrency: scheduler.NewConcurrencyService(&mockConcurrencyCache{}, scheduler.Diagnostics{
							Logf:  logging.LegacyPrintf,
							Event: logging.Event,
						}),
						GroupPolicies: routingtestkit.PricingConfig(groupID,
							capability.PlatformAnthropic, pricingConfig),
						Cache: &mockGatewayCacheForPlatform{
							sessionBindings: map[string]int64{"sticky": 1},
						},
					},
				}, cfg)

				result, err := svc.SelectAccountWithLoadAwareness(context.Background(), &groupID, "sticky", "client-alias", nil, "", 0)
				require.NoError(t, err)
				require.NotNil(t, result)
				require.Equal(t, int64(2), result.Account.Record.ID)
			})
		}
	}
}

// TestSelectAccountWithLoadAwareness_AppliesGroupMappingOnce 验证调度入口只把客户端模型 R 映射为一次 C。
func TestSelectAccountWithLoadAwareness_AppliesGroupMappingOnce(t *testing.T) {
	groupID := int64(4212)
	pricingConfig := routingtestkit.Configuration{
		ID:     78,
		Status: billing.StatusActive,
		ModelMapping: map[string]map[string]string{
			capability.PlatformGemini: {
				"client-alias": "group-model",
				"group-model":  "double-mapped-model",
			},
		},
	}
	account := gatewayprovider.ExecutionAccount{
		Record: accountcore.Record{
			LoadLocation: time.LoadLocation, ID: 1,
			Platform:    capability.PlatformGemini,
			Status:      billing.StatusActive,
			Schedulable: true,
			Concurrency: 5,
			AccountGroups: []accountcore.GroupMembership{{
				AccountID: 1,
				GroupID:   groupID,
			}},
			Credentials: map[string]any{
				"model_mapping":   map[string]any{"group-model": "upstream-model"},
				"model_whitelist": []any{"upstream-model"},
			},
		},
	}
	accountRepo := &mockAccountRepoForPlatform{
		accounts:     []gatewayprovider.ExecutionAccount{account},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{account.Record.ID: &account},
	}
	group := &routing.Group{
		ID:       groupID,
		Platform: capability.PlatformGemini,
		Status:   billing.StatusActive,
		Hydrated: true,
	}
	svc := newGenericSelectionForTest(GenericDependencies{
		Reads: Reads{
			Accounts: accountRepo,

			Groups: &mockGroupRepoForGateway{groups: map[int64]*routing.Group{groupID: group}},
		},
		Shared: Shared{GroupPolicies: routingtestkit.PricingConfig(groupID,
			capability.PlatformGemini, pricingConfig)},
	}, testConfig())

	result, err := svc.SelectAccountWithLoadAwareness(context.Background(), &groupID, "", "client-alias", nil, "", 0)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, account.Record.ID, result.Account.Record.ID)
}

func TestLegacySchedulers_FilterUpstreamRestrictedAccountsInEveryShortcut(t *testing.T) {
	testCases := []struct {
		name                string
		mixed               bool
		modelRoutingEnabled bool
		sessionHash         string
	}{
		{name: "单平台普通粘性", sessionHash: "sticky"},
		{name: "单平台路由粘性", modelRoutingEnabled: true, sessionHash: "sticky"},
		{name: "单平台路由候选", modelRoutingEnabled: true},
		{name: "混合调度普通粘性", mixed: true, sessionHash: "sticky"},
		{name: "混合调度路由粘性", mixed: true, modelRoutingEnabled: true, sessionHash: "sticky"},
		{name: "混合调度路由候选", mixed: true, modelRoutingEnabled: true},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			groupID := int64(4211)
			pricingConfig := routingtestkit.Configuration{
				ID:                 77,
				Status:             billing.StatusActive,
				RestrictModels:     true,
				BillingModelSource: routing.BillingModelSourceUpstream,
				ModelMapping: map[string]map[string]string{
					capability.PlatformAnthropic: {"client-alias": "group-model"},
				},
				ModelPricing: []routing.ModelPricingEntry{{
					Platform: capability.PlatformAnthropic,
					Models:   []string{"allowed-upstream"},
				}},
			}
			accounts := []gatewayprovider.ExecutionAccount{
				{
					Record: accountcore.Record{
						LoadLocation: time.LoadLocation, ID: 1,
						Platform:    capability.PlatformAnthropic,
						Priority:    1,
						Status:      billing.StatusActive,
						Schedulable: true,
						Concurrency: 5,
						AccountGroups: []accountcore.GroupMembership{{
							AccountID: 1,
							GroupID:   groupID,
						}},
						Credentials: map[string]any{"model_mapping": map[string]any{"group-model": "blocked-upstream"}},
					},
				},
				{
					Record: accountcore.Record{
						LoadLocation: time.LoadLocation, ID: 2,
						Platform:    capability.PlatformAnthropic,
						Priority:    2,
						Status:      billing.StatusActive,
						Schedulable: true,
						Concurrency: 5,
						AccountGroups: []accountcore.GroupMembership{{
							AccountID: 2,
							GroupID:   groupID,
						}},
						Credentials: map[string]any{"model_mapping": map[string]any{"group-model": "allowed-upstream"}},
					},
				},
			}
			accountRepo := &mockAccountRepoForPlatform{accounts: accounts, accountsByID: map[int64]*gatewayprovider.ExecutionAccount{}}
			for i := range accountRepo.accounts {
				accountRepo.accountsByID[accountRepo.accounts[i].Record.ID] = &accountRepo.accounts[i]
			}
			group := &routing.Group{
				ID:                  groupID,
				Platform:            capability.PlatformAnthropic,
				Status:              billing.StatusActive,
				Hydrated:            true,
				ModelRoutingEnabled: tt.modelRoutingEnabled,
			}
			if tt.modelRoutingEnabled {
				group.ModelRouting = map[string][]int64{"group-model": {1, 2}}
			}
			cache := &mockGatewayCacheForPlatform{sessionBindings: map[string]int64{"sticky": 1}}
			svc := newGenericSelectionForTest(GenericDependencies{
				Reads: Reads{
					Accounts: accountRepo,

					Groups: &mockGroupRepoForGateway{groups: map[int64]*routing.Group{groupID: group}},
				},
				Shared: Shared{
					GroupPolicies: routingtestkit.PricingConfig(groupID,
						capability.PlatformAnthropic, pricingConfig),
					Cache: cache,
				},
			}, testConfig())

			ctx := svc.withGroupContext(context.Background(), group)

			var (
				selected *gatewayprovider.ExecutionAccount
				err      error
			)
			if tt.mixed {
				selected, err = svc.selectAccountWithMixedScheduling(ctx, &groupID, tt.sessionHash, "client-alias", nil, capability.PlatformAnthropic)
			} else {
				selected, err = svc.selectAccountForModelWithPlatform(ctx, &groupID, tt.sessionHash, "client-alias", nil, capability.PlatformAnthropic)
			}

			require.NoError(t, err)
			require.NotNil(t, selected)
			require.Equal(t, int64(2), selected.Record.ID)
		})
	}
}

// TestGatewayService_SelectAccountWithLoadAwareness tests load-aware account selection
func TestGatewayService_SelectAccountWithLoadAwareness(t *testing.T) {
	ctx := context.Background()

	t.Run("禁用负载批量查询-降级到传统选择", func(t *testing.T) {
		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForPlatform{}

		cfg := testConfig()
		cfg.Gateway.Scheduling.LoadBatchEnabled = false

		svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo}, Shared: Shared{Cache: cache, Concurrency: nil}}, cfg)

		// No concurrency service

		result, err := svc.SelectAccountWithLoadAwareness(ctx, nil, "", "claude-3-5-sonnet-20241022", nil, "", int64(0))
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Account)
		require.Equal(t, int64(1), result.Account.Record.ID, "应选择优先级最高的账号")
	})

	t.Run("模型路由-无ConcurrencyService也生效", func(t *testing.T) {
		groupID := int64(1)
		sessionHash := "sticky"

		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5, AccountGroups: []accountcore.GroupMembership{{GroupID: groupID}}}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5, AccountGroups: []accountcore.GroupMembership{{GroupID: groupID}}}},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForPlatform{
			sessionBindings: map[string]int64{sessionHash: 1},
		}

		groupRepo := &mockGroupRepoForGateway{
			groups: map[int64]*routing.Group{
				groupID: {
					ID:                  groupID,
					Platform:            capability.PlatformAnthropic,
					Status:              billing.StatusActive,
					Hydrated:            true,
					ModelRoutingEnabled: true,
					ModelRouting: map[string][]int64{
						"claude-a": {1},
						"claude-b": {2},
					},
				},
			},
		}

		cfg := testConfig()
		cfg.Gateway.Scheduling.LoadBatchEnabled = true

		svc := newGenericSelectionForTest(GenericDependencies{
			Reads:  Reads{Accounts: repo, Groups: groupRepo},
			Shared: Shared{Cache: cache, Concurrency: nil},
		}, cfg)

		// legacy path

		result, err := svc.SelectAccountWithLoadAwareness(ctx, &groupID, sessionHash, "claude-b", nil, "", int64(0))
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Account)
		require.Equal(t, int64(2), result.Account.Record.ID, "切换到 claude-b 时应按模型路由切换账号")
		require.Equal(t, int64(2), cache.sessionBindings[sessionHash], "粘性绑定应更新为路由选择的账号")
	})

	t.Run("无ConcurrencyService-降级到传统选择", func(t *testing.T) {
		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForPlatform{}

		cfg := testConfig()
		cfg.Gateway.Scheduling.LoadBatchEnabled = true

		svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo}, Shared: Shared{Cache: cache, Concurrency: nil}}, cfg)

		result, err := svc.SelectAccountWithLoadAwareness(ctx, nil, "", "claude-3-5-sonnet-20241022", nil, "", int64(0))
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Account)
		require.Equal(t, int64(2), result.Account.Record.ID, "应选择优先级最高的账号")
	})

	t.Run("排除账号-不选择被排除的账号", func(t *testing.T) {
		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForPlatform{}

		cfg := testConfig()
		cfg.Gateway.Scheduling.LoadBatchEnabled = false

		svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo}, Shared: Shared{Concurrency: nil, Cache: cache}}, cfg)

		excludedIDs := map[int64]struct{}{1: {}}
		result, err := svc.SelectAccountWithLoadAwareness(ctx, nil, "", "claude-3-5-sonnet-20241022", excludedIDs, "", int64(0))
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Account)
		require.Equal(t, int64(2), result.Account.Record.ID, "不应选择被排除的账号")
	})

	t.Run("粘性命中-不调用GetByID", func(t *testing.T) {
		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForPlatform{
			sessionBindings: map[string]int64{"sticky": 1},
		}

		cfg := testConfig()
		cfg.Gateway.Scheduling.LoadBatchEnabled = true

		concurrencyCache := &mockConcurrencyCache{}

		svc := newGenericSelectionForTest(GenericDependencies{
			Reads: Reads{Accounts: repo},
			Shared: Shared{
				Cache: cache,
				Concurrency: scheduler.NewConcurrencyService(concurrencyCache,

					scheduler.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			},
		}, cfg)

		result, err := svc.SelectAccountWithLoadAwareness(ctx, nil, "sticky", "claude-3-5-sonnet-20241022", nil, "", int64(0))
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Account)
		require.Equal(t, int64(1), result.Account.Record.ID)
		require.Equal(t, 0, repo.getByIDCalls, "粘性命中不应调用GetByID")
		require.Equal(t, 0, concurrencyCache.loadBatchCalls, "粘性命中应在负载批量查询前返回")
	})

	t.Run("粘性账号不在候选集-回退负载感知选择", func(t *testing.T) {
		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForPlatform{
			sessionBindings: map[string]int64{"sticky": 1},
		}

		cfg := testConfig()
		cfg.Gateway.Scheduling.LoadBatchEnabled = true

		concurrencyCache := &mockConcurrencyCache{}

		svc := newGenericSelectionForTest(GenericDependencies{
			Reads: Reads{Accounts: repo},
			Shared: Shared{
				Cache: cache,
				Concurrency: scheduler.NewConcurrencyService(concurrencyCache,

					scheduler.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			},
		}, cfg)

		result, err := svc.SelectAccountWithLoadAwareness(ctx, nil, "sticky", "claude-3-5-sonnet-20241022", nil, "", int64(0))
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Account)
		require.Equal(t, int64(2), result.Account.Record.ID, "粘性账号不在候选集时应回退到可用账号")
		require.Equal(t, 0, repo.getByIDCalls, "粘性账号缺失不应回退到GetByID")
		require.Equal(t, 1, concurrencyCache.loadBatchCalls, "应继续进行负载批量查询")
	})

	t.Run("粘性账号禁用-清理会话并回退选择", func(t *testing.T) {
		testCtx := apikey.WithForcePlatform(ctx, capability.PlatformAnthropic)
		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: false, Concurrency: 5}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}
		repo.listPlatformFunc = func(ctx context.Context, platform string) ([]gatewayprovider.ExecutionAccount, error) {
			return repo.accounts, nil
		}

		cache := &mockGatewayCacheForPlatform{
			sessionBindings: map[string]int64{"sticky": 1},
		}

		cfg := testConfig()
		cfg.Gateway.Scheduling.LoadBatchEnabled = true

		concurrencyCache := &mockConcurrencyCache{}

		svc := newGenericSelectionForTest(GenericDependencies{
			Reads: Reads{Accounts: repo},
			Shared: Shared{
				Cache: cache,
				Concurrency: scheduler.NewConcurrencyService(concurrencyCache,

					scheduler.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			},
		}, cfg)

		result, err := svc.SelectAccountWithLoadAwareness(testCtx, nil, "sticky", "claude-3-5-sonnet-20241022", nil, "", int64(0))
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Account)
		require.Equal(t, int64(2), result.Account.Record.ID, "粘性账号禁用时应回退到可用账号")
		updatedID, ok := cache.sessionBindings["sticky"]
		require.True(t, ok, "粘性会话应更新绑定")
		require.Equal(t, int64(2), updatedID, "粘性会话应绑定到新账号")
	})

	t.Run("无可用账号-返回错误", func(t *testing.T) {
		repo := &mockAccountRepoForPlatform{
			accounts:     []gatewayprovider.ExecutionAccount{},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}

		cache := &mockGatewayCacheForPlatform{}

		cfg := testConfig()
		cfg.Gateway.Scheduling.LoadBatchEnabled = false

		svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo}, Shared: Shared{Concurrency: nil, Cache: cache}}, cfg)

		result, err := svc.SelectAccountWithLoadAwareness(ctx, nil, "", "claude-3-5-sonnet-20241022", nil, "", int64(0))
		require.Error(t, err)
		require.Nil(t, result)
		require.ErrorIs(t, err, scheduler.ErrNoAvailableAccounts)
	})

	t.Run("过滤不可调度账号-限流账号被跳过", func(t *testing.T) {
		now := time.Now()
		resetAt := now.Add(10 * time.Minute)

		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5, RateLimitResetAt: &resetAt}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForPlatform{}
		cfg := testConfig()
		cfg.Gateway.Scheduling.LoadBatchEnabled = false

		svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo}, Shared: Shared{Cache: cache, Concurrency: nil}}, cfg)

		result, err := svc.SelectAccountWithLoadAwareness(ctx, nil, "", "claude-3-5-sonnet-20241022", nil, "", int64(0))
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Account)
		require.Equal(t, int64(2), result.Account.Record.ID, "应跳过限流账号，选择可用账号")
	})

	t.Run("过滤不可调度账号-过载账号被跳过", func(t *testing.T) {
		now := time.Now()
		overloadUntil := now.Add(10 * time.Minute)

		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5, OverloadUntil: &overloadUntil}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForPlatform{}
		cfg := testConfig()
		cfg.Gateway.Scheduling.LoadBatchEnabled = false

		svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo}, Shared: Shared{Concurrency: nil, Cache: cache}}, cfg)

		result, err := svc.SelectAccountWithLoadAwareness(ctx, nil, "", "claude-3-5-sonnet-20241022", nil, "", int64(0))
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Account)
		require.Equal(t, int64(2), result.Account.Record.ID, "应跳过过载账号，选择可用账号")
	})

	t.Run("粘性账号槽位满-返回粘性等待计划", func(t *testing.T) {
		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForPlatform{
			sessionBindings: map[string]int64{"sticky": 1},
		}

		cfg := testConfig()
		cfg.Gateway.Scheduling.LoadBatchEnabled = true
		cfg.Gateway.Scheduling.StickySessionMaxWaiting = 1

		concurrencyCache := &mockConcurrencyCache{
			acquireResults: map[int64]bool{1: false},
			waitCounts:     map[int64]int{1: 0},
		}

		svc := newGenericSelectionForTest(GenericDependencies{
			Reads: Reads{Accounts: repo},
			Shared: Shared{
				Cache: cache,
				Concurrency: scheduler.NewConcurrencyService(concurrencyCache,

					scheduler.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			},
		}, cfg)

		result, err := svc.SelectAccountWithLoadAwareness(ctx, nil, "sticky", "claude-3-5-sonnet-20241022", nil, "", int64(0))
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.WaitPlan)
		require.Equal(t, int64(1), result.Account.Record.ID)
		require.Equal(t, 0, concurrencyCache.loadBatchCalls)
	})

	t.Run("负载批量查询失败-降级旧顺序选择", func(t *testing.T) {
		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForPlatform{}

		cfg := testConfig()
		cfg.Gateway.Scheduling.LoadBatchEnabled = true

		concurrencyCache := &mockConcurrencyCache{
			loadBatchErr: errors.New("load batch failed"),
		}

		svc := newGenericSelectionForTest(GenericDependencies{
			Reads: Reads{Accounts: repo},
			Shared: Shared{
				Cache: cache,
				Concurrency: scheduler.NewConcurrencyService(concurrencyCache,

					scheduler.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			},
		}, cfg)

		result, err := svc.SelectAccountWithLoadAwareness(ctx, nil, "legacy", "claude-3-5-sonnet-20241022", nil, "", int64(0))
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Account)
		require.Equal(t, int64(2), result.Account.Record.ID)
		require.Equal(t, int64(2), cache.sessionBindings["legacy"])
	})

	t.Run("模型路由-粘性账号等待计划", func(t *testing.T) {
		groupID := int64(20)
		sessionHash := "route-sticky"

		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForPlatform{
			sessionBindings: map[string]int64{sessionHash: 1},
		}

		groupRepo := &mockGroupRepoForGateway{
			groups: map[int64]*routing.Group{
				groupID: {
					ID:                  groupID,
					Platform:            capability.PlatformAnthropic,
					Status:              billing.StatusActive,
					Hydrated:            true,
					ModelRoutingEnabled: true,
					ModelRouting: map[string][]int64{
						"claude-3-5-sonnet-20241022": {1, 2},
					},
				},
			},
		}

		cfg := testConfig()
		cfg.Gateway.Scheduling.LoadBatchEnabled = true
		cfg.Gateway.Scheduling.StickySessionMaxWaiting = 1

		concurrencyCache := &mockConcurrencyCache{
			acquireResults: map[int64]bool{1: false},
			waitCounts:     map[int64]int{1: 0},
		}

		svc := newGenericSelectionForTest(GenericDependencies{
			Reads: Reads{Accounts: repo, Groups: groupRepo},
			Shared: Shared{
				Concurrency: scheduler.NewConcurrencyService(concurrencyCache,

					scheduler.Diagnostics{
						Logf:  logging.LegacyPrintf,
						Event: logging.Event,
					}),
				Cache: cache,
			},
		}, cfg)

		result, err := svc.SelectAccountWithLoadAwareness(ctx, &groupID, sessionHash, "claude-3-5-sonnet-20241022", nil, "", int64(0))
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.WaitPlan)
		require.Equal(t, int64(1), result.Account.Record.ID)
	})

	t.Run("模型路由-粘性账号命中", func(t *testing.T) {
		groupID := int64(20)
		sessionHash := "route-hit"

		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForPlatform{
			sessionBindings: map[string]int64{sessionHash: 1},
		}

		groupRepo := &mockGroupRepoForGateway{
			groups: map[int64]*routing.Group{
				groupID: {
					ID:                  groupID,
					Platform:            capability.PlatformAnthropic,
					Status:              billing.StatusActive,
					Hydrated:            true,
					ModelRoutingEnabled: true,
					ModelRouting: map[string][]int64{
						"claude-3-5-sonnet-20241022": {1, 2},
					},
				},
			},
		}

		cfg := testConfig()
		cfg.Gateway.Scheduling.LoadBatchEnabled = true

		concurrencyCache := &mockConcurrencyCache{}

		svc := newGenericSelectionForTest(GenericDependencies{
			Reads: Reads{Accounts: repo, Groups: groupRepo},
			Shared: Shared{
				Cache: cache,
				Concurrency: scheduler.NewConcurrencyService(
					concurrencyCache, scheduler.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			},
		}, cfg)

		result, err := svc.SelectAccountWithLoadAwareness(ctx, &groupID, sessionHash, "claude-3-5-sonnet-20241022", nil, "", int64(0))
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Account)
		require.Equal(t, int64(1), result.Account.Record.ID)
		require.Equal(t, 0, concurrencyCache.loadBatchCalls)
	})

	t.Run("模型路由-粘性账号缺失-清理并回退", func(t *testing.T) {
		groupID := int64(22)
		sessionHash := "route-missing"

		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForPlatform{
			sessionBindings: map[string]int64{sessionHash: 1},
		}

		groupRepo := &mockGroupRepoForGateway{
			groups: map[int64]*routing.Group{
				groupID: {
					ID:                  groupID,
					Platform:            capability.PlatformAnthropic,
					Status:              billing.StatusActive,
					Hydrated:            true,
					ModelRoutingEnabled: true,
					ModelRouting: map[string][]int64{
						"claude-3-5-sonnet-20241022": {1, 2},
					},
				},
			},
		}

		cfg := testConfig()
		cfg.Gateway.Scheduling.LoadBatchEnabled = true

		concurrencyCache := &mockConcurrencyCache{}

		svc := newGenericSelectionForTest(GenericDependencies{
			Reads: Reads{
				Groups:   groupRepo,
				Accounts: repo,
			},
			Shared: Shared{
				Cache: cache,
				Concurrency: scheduler.NewConcurrencyService(
					concurrencyCache, scheduler.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			},
		}, cfg)

		result, err := svc.SelectAccountWithLoadAwareness(ctx, &groupID, sessionHash, "claude-3-5-sonnet-20241022", nil, "", int64(0))
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Account)
		require.Equal(t, int64(2), result.Account.Record.ID)
		require.Equal(t, 1, cache.deletedSessions[sessionHash])
		require.Equal(t, int64(2), cache.sessionBindings[sessionHash])
	})

	t.Run("模型路由-按负载选择账号", func(t *testing.T) {
		groupID := int64(21)

		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForPlatform{}

		groupRepo := &mockGroupRepoForGateway{
			groups: map[int64]*routing.Group{
				groupID: {
					ID:                  groupID,
					Platform:            capability.PlatformAnthropic,
					Status:              billing.StatusActive,
					Hydrated:            true,
					ModelRoutingEnabled: true,
					ModelRouting: map[string][]int64{
						"claude-3-5-sonnet-20241022": {1, 2},
					},
				},
			},
		}

		cfg := testConfig()
		cfg.Gateway.Scheduling.LoadBatchEnabled = true

		concurrencyCache := &mockConcurrencyCache{
			loadMap: map[int64]*scheduler.AccountLoadInfo{
				1: {AccountID: 1, LoadRate: 80},
				2: {AccountID: 2, LoadRate: 20},
			},
		}

		svc := newGenericSelectionForTest(GenericDependencies{
			Reads: Reads{Accounts: repo, Groups: groupRepo},
			Shared: Shared{
				Concurrency: scheduler.NewConcurrencyService(concurrencyCache,

					scheduler.Diagnostics{
						Logf:  logging.LegacyPrintf,
						Event: logging.Event,
					}),
				Cache: cache,
			},
		}, cfg)

		result, err := svc.SelectAccountWithLoadAwareness(ctx, &groupID, "route", "claude-3-5-sonnet-20241022", nil, "", int64(0))
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Account)
		require.Equal(t, int64(2), result.Account.Record.ID)
		require.Equal(t, int64(2), cache.sessionBindings["route"])
	})

	t.Run("模型路由-路由账号全满返回等待计划", func(t *testing.T) {
		groupID := int64(23)

		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForPlatform{}

		groupRepo := &mockGroupRepoForGateway{
			groups: map[int64]*routing.Group{
				groupID: {
					ID:                  groupID,
					Platform:            capability.PlatformAnthropic,
					Status:              billing.StatusActive,
					Hydrated:            true,
					ModelRoutingEnabled: true,
					ModelRouting: map[string][]int64{
						"claude-3-5-sonnet-20241022": {1, 2},
					},
				},
			},
		}

		cfg := testConfig()
		cfg.Gateway.Scheduling.LoadBatchEnabled = true

		concurrencyCache := &mockConcurrencyCache{
			acquireResults: map[int64]bool{1: false, 2: false},
			loadMap: map[int64]*scheduler.AccountLoadInfo{
				1: {AccountID: 1, LoadRate: 10},
				2: {AccountID: 2, LoadRate: 20},
			},
		}

		svc := newGenericSelectionForTest(GenericDependencies{
			Reads: Reads{Accounts: repo, Groups: groupRepo},
			Shared: Shared{
				Concurrency: scheduler.NewConcurrencyService(concurrencyCache,

					scheduler.Diagnostics{
						Logf:  logging.LegacyPrintf,
						Event: logging.Event,
					}),
				Cache: cache,
			},
		}, cfg)

		result, err := svc.SelectAccountWithLoadAwareness(ctx, &groupID, "route-full", "claude-3-5-sonnet-20241022", nil, "", int64(0))
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.WaitPlan)
		require.Equal(t, int64(1), result.Account.Record.ID)
	})

	t.Run("模型路由-路由账号全满-回退普通选择", func(t *testing.T) {
		groupID := int64(22)

		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 3, Platform: capability.PlatformAnthropic, Priority: 0, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForPlatform{}

		groupRepo := &mockGroupRepoForGateway{
			groups: map[int64]*routing.Group{
				groupID: {
					ID:                  groupID,
					Platform:            capability.PlatformAnthropic,
					Status:              billing.StatusActive,
					Hydrated:            true,
					ModelRoutingEnabled: true,
					ModelRouting: map[string][]int64{
						"claude-3-5-sonnet-20241022": {1, 2},
					},
				},
			},
		}

		cfg := testConfig()
		cfg.Gateway.Scheduling.LoadBatchEnabled = true

		concurrencyCache := &mockConcurrencyCache{
			loadMap: map[int64]*scheduler.AccountLoadInfo{
				1: {AccountID: 1, LoadRate: 100},
				2: {AccountID: 2, LoadRate: 100},
				3: {AccountID: 3, LoadRate: 0},
			},
		}

		svc := newGenericSelectionForTest(GenericDependencies{
			Reads: Reads{Accounts: repo, Groups: groupRepo},
			Shared: Shared{
				Cache: cache,
				Concurrency: scheduler.NewConcurrencyService(
					concurrencyCache, scheduler.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			},
		}, cfg)

		result, err := svc.SelectAccountWithLoadAwareness(ctx, &groupID, "fallback", "claude-3-5-sonnet-20241022", nil, "", int64(0))
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Account)
		require.Equal(t, int64(3), result.Account.Record.ID)
		require.Equal(t, int64(3), cache.sessionBindings["fallback"])
	})

	t.Run("负载批量失败且无法获取-兜底等待", func(t *testing.T) {
		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForPlatform{}

		cfg := testConfig()
		cfg.Gateway.Scheduling.LoadBatchEnabled = true

		concurrencyCache := &mockConcurrencyCache{
			loadBatchErr:   errors.New("load batch failed"),
			acquireResults: map[int64]bool{1: false, 2: false},
		}

		svc := newGenericSelectionForTest(GenericDependencies{
			Reads: Reads{Accounts: repo},
			Shared: Shared{
				Cache: cache,
				Concurrency: scheduler.NewConcurrencyService(concurrencyCache,

					scheduler.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			},
		}, cfg)

		result, err := svc.SelectAccountWithLoadAwareness(ctx, nil, "", "claude-3-5-sonnet-20241022", nil, "", int64(0))
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.WaitPlan)
		require.Equal(t, int64(1), result.Account.Record.ID)
	})

	t.Run("Gemini负载排序-优先OAuth", func(t *testing.T) {
		groupID := int64(24)

		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformGemini, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5, Type: capability.AccountTypeAPIKey}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformGemini, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5, Type: capability.AccountTypeOAuth}},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForPlatform{}

		groupRepo := &mockGroupRepoForGateway{
			groups: map[int64]*routing.Group{
				groupID: {
					ID:       groupID,
					Platform: capability.PlatformGemini,
					Status:   billing.StatusActive,
					Hydrated: true,
				},
			},
		}

		cfg := testConfig()
		cfg.Gateway.Scheduling.LoadBatchEnabled = true

		concurrencyCache := &mockConcurrencyCache{
			loadMap: map[int64]*scheduler.AccountLoadInfo{
				1: {AccountID: 1, LoadRate: 10},
				2: {AccountID: 2, LoadRate: 10},
			},
		}

		svc := newGenericSelectionForTest(GenericDependencies{
			Reads: Reads{Accounts: repo, Groups: groupRepo},
			Shared: Shared{
				Cache: cache,
				Concurrency: scheduler.NewConcurrencyService(
					concurrencyCache, scheduler.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			},
		}, cfg)

		result, err := svc.SelectAccountWithLoadAwareness(ctx, &groupID, "gemini", "gemini-2.5-pro", nil, "", int64(0))
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Account)
		require.Equal(t, int64(2), result.Account.Record.ID)
	})

	t.Run("模型路由-过滤路径覆盖", func(t *testing.T) {
		groupID := int64(70)
		now := time.Now().Add(10 * time.Minute)
		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 3, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: false, Concurrency: 5}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 4, Platform: capability.PlatformAntigravity, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
				{
					Record: accountcore.Record{
						LoadLocation: time.LoadLocation, ID: 5,
						Platform:    capability.PlatformAnthropic,
						Priority:    1,
						Status:      billing.StatusActive,
						Schedulable: true,
						Concurrency: 5,
						Extra: map[string]any{
							"model_rate_limits": map[string]any{
								"claude-3-5-sonnet-20241022": map[string]any{
									"rate_limit_reset_at": now.Format(time.RFC3339),
								},
							},
						},
					},
				},
				{
					Record: accountcore.Record{
						LoadLocation: time.LoadLocation, ID: 6,
						Platform:    capability.PlatformAnthropic,
						Priority:    1,
						Status:      billing.StatusActive,
						Schedulable: true,
						Concurrency: 5,
						Credentials: map[string]any{"model_mapping": map[string]any{"claude-3-5-haiku-20241022": "claude-3-5-haiku-20241022"}},
					},
				},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 7, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForPlatform{}

		groupRepo := &mockGroupRepoForGateway{
			groups: map[int64]*routing.Group{
				groupID: {
					ID:                  groupID,
					Platform:            capability.PlatformAnthropic,
					Status:              billing.StatusActive,
					Hydrated:            true,
					ModelRoutingEnabled: true,
					ModelRouting: map[string][]int64{
						"claude-3-5-sonnet-20241022": {1, 2, 3, 4, 5, 6},
					},
				},
			},
		}

		cfg := testConfig()
		cfg.Gateway.Scheduling.LoadBatchEnabled = true

		concurrencyCache := &mockConcurrencyCache{}

		svc := newGenericSelectionForTest(GenericDependencies{
			Reads: Reads{Accounts: repo, Groups: groupRepo},
			Shared: Shared{
				Cache: cache,
				Concurrency: scheduler.NewConcurrencyService(
					concurrencyCache, scheduler.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			},
		}, cfg)

		excluded := map[int64]struct{}{1: {}}
		result, err := svc.SelectAccountWithLoadAwareness(ctx, &groupID, "", "claude-3-5-sonnet-20241022", excluded, "", int64(0))
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Account)
		require.Equal(t, int64(7), result.Account.Record.ID)
	})

	t.Run("ClaudeCode限制-回退分组", func(t *testing.T) {
		groupID := int64(60)
		fallbackID := int64(61)

		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformGemini, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		groupRepo := &mockGroupRepoForGateway{
			groups: map[int64]*routing.Group{
				groupID: {
					ID:             groupID,
					Platform:       capability.PlatformAnthropic,
					Status:         billing.StatusActive,
					Hydrated:       true,
					ClaudeCodeOnly: true,
					FallbackGroupID: func() *int64 {
						v := fallbackID
						return &v
					}(),
				},
				fallbackID: {
					ID:       fallbackID,
					Platform: capability.PlatformGemini,
					Status:   billing.StatusActive,
					Hydrated: true,
				},
			},
		}

		cfg := testConfig()
		cfg.Gateway.Scheduling.LoadBatchEnabled = false

		svc := newGenericSelectionForTest(GenericDependencies{
			Reads:  Reads{Accounts: repo, Groups: groupRepo},
			Shared: Shared{Cache: &mockGatewayCacheForPlatform{}, Concurrency: nil},
		},

			cfg)

		result, err := svc.SelectAccountWithLoadAwareness(ctx, &groupID, "", "gemini-2.5-pro", nil, "", int64(0))
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Account)
		require.Equal(t, int64(1), result.Account.Record.ID)
	})

	t.Run("ClaudeCode限制-无降级返回错误", func(t *testing.T) {
		groupID := int64(62)

		groupRepo := &mockGroupRepoForGateway{
			groups: map[int64]*routing.Group{
				groupID: {
					ID:             groupID,
					Platform:       capability.PlatformAnthropic,
					Status:         billing.StatusActive,
					Hydrated:       true,
					ClaudeCodeOnly: true,
				},
			},
		}

		cfg := testConfig()
		cfg.Gateway.Scheduling.LoadBatchEnabled = false

		svc := newGenericSelectionForTest(GenericDependencies{
			Reads:  Reads{Accounts: &mockAccountRepoForPlatform{}, Groups: groupRepo},
			Shared: Shared{Cache: &mockGatewayCacheForPlatform{}, Concurrency: nil},
		}, cfg)

		result, err := svc.SelectAccountWithLoadAwareness(ctx, &groupID, "", "claude-3-5-sonnet-20241022", nil, "", int64(0))
		require.Error(t, err)
		require.Nil(t, result)
		require.ErrorIs(t, err, routing.ErrClaudeCodeOnly)
	})

	t.Run("负载可用但无法获取槽位-兜底等待", func(t *testing.T) {
		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 2, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForPlatform{}

		cfg := testConfig()
		cfg.Gateway.Scheduling.LoadBatchEnabled = true

		concurrencyCache := &mockConcurrencyCache{
			acquireResults: map[int64]bool{1: false, 2: false},
			loadMap: map[int64]*scheduler.AccountLoadInfo{
				1: {AccountID: 1, LoadRate: 10},
				2: {AccountID: 2, LoadRate: 20},
			},
		}

		svc := newGenericSelectionForTest(GenericDependencies{
			Reads: Reads{Accounts: repo},
			Shared: Shared{
				Cache: cache,
				Concurrency: scheduler.NewConcurrencyService(concurrencyCache,

					scheduler.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			},
		}, cfg)

		result, err := svc.SelectAccountWithLoadAwareness(ctx, nil, "wait", "claude-3-5-sonnet-20241022", nil, "", int64(0))
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.WaitPlan)
		require.Equal(t, int64(1), result.Account.Record.ID)
	})

	t.Run("负载信息缺失-使用默认负载", func(t *testing.T) {
		repo := &mockAccountRepoForPlatform{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true, Concurrency: 5}},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForPlatform{}

		cfg := testConfig()
		cfg.Gateway.Scheduling.LoadBatchEnabled = true

		concurrencyCache := &mockConcurrencyCache{
			loadMap: map[int64]*scheduler.AccountLoadInfo{
				1: {AccountID: 1, LoadRate: 50},
			},
			skipDefaultLoad: true,
		}

		svc := newGenericSelectionForTest(GenericDependencies{
			Reads: Reads{Accounts: repo},
			Shared: Shared{
				Cache: cache,
				Concurrency: scheduler.NewConcurrencyService(concurrencyCache,

					scheduler.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}),
			},
		}, cfg)

		result, err := svc.SelectAccountWithLoadAwareness(ctx, nil, "missing-load", "claude-3-5-sonnet-20241022", nil, "", int64(0))
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Account)
		require.Equal(t, int64(2), result.Account.Record.ID)
	})
}

func TestGatewayService_GroupResolution_ReusesContextGroup(t *testing.T) {
	ctx := context.Background()
	groupID := int64(42)
	group := &routing.Group{
		ID:       groupID,
		Platform: capability.PlatformAnthropic,
		Status:   billing.StatusActive,
		Hydrated: true,
	}
	ctx = requeststate.WithGroup(ctx, group)

	repo := &mockAccountRepoForPlatform{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
		},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
	}

	groupRepo := &mockGroupRepoForGateway{
		groups: map[int64]*routing.Group{groupID: group},
	}

	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo, Groups: groupRepo}, Shared: Shared{}}, testConfig())

	account, err := svc.SelectAccountForModelWithExclusions(ctx, &groupID, "", "claude-3-5-sonnet-20241022", nil)
	require.NoError(t, err)
	require.NotNil(t, account)
	require.Equal(t, 1, groupRepo.getByIDCalls) // +1 for require_privacy_set check
	require.Equal(t, 0, groupRepo.getByIDLiteCalls)
}

func TestGatewayService_GroupResolution_IgnoresInvalidContextGroup(t *testing.T) {
	ctx := context.Background()
	groupID := int64(42)
	ctxGroup := &routing.Group{
		ID:       groupID,
		Platform: capability.PlatformAnthropic,
		Status:   billing.StatusActive,
	}
	ctx = requeststate.WithGroup(ctx, ctxGroup)

	repo := &mockAccountRepoForPlatform{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
		},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
	}

	group := &routing.Group{
		ID:       groupID,
		Platform: capability.PlatformAnthropic,
		Status:   billing.StatusActive,
		Hydrated: true,
	}
	groupRepo := &mockGroupRepoForGateway{
		groups: map[int64]*routing.Group{groupID: group},
	}

	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo, Groups: groupRepo}, Shared: Shared{}}, testConfig())

	account, err := svc.SelectAccountForModelWithExclusions(ctx, &groupID, "", "claude-3-5-sonnet-20241022", nil)
	require.NoError(t, err)
	require.NotNil(t, account)
	require.Equal(t, 1, groupRepo.getByIDCalls) // +1 for require_privacy_set check
	require.Equal(t, 1, groupRepo.getByIDLiteCalls)
}

func TestGatewayService_GroupContext_OverwritesInvalidContextGroup(t *testing.T) {
	groupID := int64(42)
	invalidGroup := &routing.Group{
		ID:       groupID,
		Platform: capability.PlatformAnthropic,
		Status:   billing.StatusActive,
	}
	hydratedGroup := &routing.Group{
		ID:       groupID,
		Platform: capability.PlatformAnthropic,
		Status:   billing.StatusActive,
		Hydrated: true,
	}

	ctx := requeststate.WithGroup(context.Background(), invalidGroup)
	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{}, Shared: Shared{}}, nil)

	ctx = svc.withGroupContext(ctx, hydratedGroup)

	got, ok := requeststate.GroupFromContext(ctx)
	require.True(t, ok)
	require.Equal(t, hydratedGroup, got)
	require.NotSame(t, hydratedGroup, got, "分组状态保存独立快照")
}

func TestGatewayService_GroupResolution_FallbackUsesLiteOnce(t *testing.T) {
	ctx := context.Background()
	groupID := int64(10)
	fallbackID := int64(11)
	group := &routing.Group{
		ID:              groupID,
		Platform:        capability.PlatformAnthropic,
		Status:          billing.StatusActive,
		ClaudeCodeOnly:  true,
		FallbackGroupID: &fallbackID,
		Hydrated:        true,
	}
	fallbackGroup := &routing.Group{
		ID:       fallbackID,
		Platform: capability.PlatformAnthropic,
		Status:   billing.StatusActive,
		Hydrated: true,
	}
	ctx = requeststate.WithGroup(ctx, group)

	repo := &mockAccountRepoForPlatform{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAnthropic, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
		},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
	}

	groupRepo := &mockGroupRepoForGateway{
		groups: map[int64]*routing.Group{fallbackID: fallbackGroup},
	}

	svc := newGenericSelectionForTest(GenericDependencies{Reads: Reads{Accounts: repo, Groups: groupRepo}, Shared: Shared{}}, testConfig())

	account, err := svc.SelectAccountForModelWithExclusions(ctx, &groupID, "", "claude-3-5-sonnet-20241022", nil)
	require.NoError(t, err)
	require.NotNil(t, account)
	require.Equal(t, 1, groupRepo.getByIDCalls) // +1 for require_privacy_set check
	require.Equal(t, 1, groupRepo.getByIDLiteCalls)
}
