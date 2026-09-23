//go:build unit

package selection

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
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

// Verify interface implementation

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

var _ Groups = (*mockGroupRepoForGemini)(nil)

// mockGatewayCacheForGemini Gemini 测试用的 cache mock
type mockGatewayCacheForGemini struct {
	sessionBindings map[string]int64
	deletedSessions map[string]int
}

func (m *mockGatewayCacheForGemini) GetSessionAccountID(ctx context.Context, groupID int64, sessionHash string) (int64, error) {
	if id, ok := m.sessionBindings[sessionHash]; ok {
		return id, nil
	}
	return 0, errors.New("not found")
}

func (m *mockGatewayCacheForGemini) SetSessionAccountID(ctx context.Context, groupID int64, sessionHash string, accountID int64, ttl time.Duration) error {
	if m.sessionBindings == nil {
		m.sessionBindings = make(map[string]int64)
	}
	m.sessionBindings[sessionHash] = accountID
	return nil
}

func (m *mockGatewayCacheForGemini) RefreshSessionTTL(ctx context.Context, groupID int64, sessionHash string, ttl time.Duration) error {
	return nil
}

func (m *mockGatewayCacheForGemini) DeleteSessionAccountID(ctx context.Context, groupID int64, sessionHash string) error {
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

// TestGeminiMessagesCompatService_SelectAccountForModelWithExclusions_GeminiPlatform 测试 Gemini 单平台选择
func TestGeminiMessagesCompatService_SelectAccountForModelWithExclusions_GeminiPlatform(t *testing.T) {
	ctx := context.Background()

	repo := &mockAccountRepoForGemini{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformGemini, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformGemini, Priority: 2, Status: billing.StatusActive, Schedulable: true}},
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 3, Platform: capability.PlatformAntigravity, Priority: 1, Status: billing.StatusActive, Schedulable: true}}, // 应被隔离
		},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
	}

	cache := &mockGatewayCacheForGemini{}
	groupRepo := &mockGroupRepoForGemini{groups: map[int64]*routing.Group{}}

	svc := newGeminiSelectionForTest(GeminiDependencies{Reads: Reads{Accounts: repo, Groups: groupRepo}, Shared: Shared{Cache: cache}}, nil)

	// 无分组时使用 gemini 平台
	acc, err := svc.SelectAccountForModelWithExclusions(ctx, nil, "", "gemini-2.5-flash", nil)
	require.NoError(t, err)
	require.NotNil(t, acc)
	require.Equal(t, int64(1), acc.Record.ID, "应选择优先级最高的 gemini 账户")
	require.Equal(t, capability.PlatformGemini, acc.Record.Platform, "无分组时应只返回 gemini 平台账户")
}

func TestGeminiMessagesCompatService_GroupResolution_ReusesContextGroup(t *testing.T) {
	ctx := context.Background()
	groupID := int64(7)
	group := &routing.Group{
		ID:       groupID,
		Platform: capability.PlatformGemini,
		Status:   billing.StatusActive,
		Hydrated: true,
	}
	ctx = requeststate.WithGroup(ctx, group)

	repo := &mockAccountRepoForGemini{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformGemini, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
		},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
	}

	cache := &mockGatewayCacheForGemini{}
	groupRepo := &mockGroupRepoForGemini{groups: map[int64]*routing.Group{}}

	svc := newGeminiSelectionForTest(GeminiDependencies{Reads: Reads{Accounts: repo, Groups: groupRepo}, Shared: Shared{Cache: cache}}, nil)

	acc, err := svc.SelectAccountForModelWithExclusions(ctx, &groupID, "", "gemini-2.5-flash", nil)
	require.NoError(t, err)
	require.NotNil(t, acc)
	require.Equal(t, 0, groupRepo.getByIDCalls)
	require.Equal(t, 0, groupRepo.getByIDLiteCalls)
}

func TestGeminiMessagesCompatService_AdvancedGroupUsesGenericScore(t *testing.T) {
	groupID := int64(71)
	ctx := requeststate.WithGroup(context.Background(), &routing.Group{
		ID:            groupID,
		Platform:      capability.PlatformGemini,
		SchedulerType: routing.GroupSchedulerTypeAdvanced,
		Status:        billing.StatusActive,
		Hydrated:      true,
	})
	repo := &mockAccountRepoForGemini{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformGemini, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformGemini, Priority: 99, Status: billing.StatusActive, Schedulable: true}},
		},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
	}
	stats := scheduler.NewRuntimeStats(time.Now)
	for range 16 {
		stats.Report(1, false, nil)
		stats.Report(2, true, nil)
	}
	cfg := &config.Config{}
	cfg.Gateway.AdvancedScheduler.LBTopK = 1
	cfg.Gateway.AdvancedScheduler.ScoreWeights = config.GatewayAdvancedSchedulerScoreWeights{
		ErrorRate: 10,
	}
	svc := newGeminiSelectionForTest(GeminiDependencies{
		Reads: Reads{Accounts: repo, Groups: &mockGroupRepoForGemini{groups: map[int64]*routing.Group{}}},

		Shared: Shared{Cache: &mockGatewayCacheForGemini{}, Feedback: stats},
	}, cfg)

	account, err := svc.SelectAccountForModelWithExclusions(ctx, &groupID, "", "gemini-2.5-flash", nil)

	require.NoError(t, err)
	require.NotNil(t, account)
	require.Equal(t, int64(2), account.Record.ID, "高级分组应在既有硬过滤后按通用错误率评分选择")
}

func TestGeminiMessagesCompatService_GroupResolution_UsesLiteFetch(t *testing.T) {
	ctx := context.Background()
	groupID := int64(7)

	repo := &mockAccountRepoForGemini{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformGemini, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
		},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
	}

	cache := &mockGatewayCacheForGemini{}
	groupRepo := &mockGroupRepoForGemini{
		groups: map[int64]*routing.Group{
			groupID: {ID: groupID, Platform: capability.PlatformGemini},
		},
	}

	svc := newGeminiSelectionForTest(GeminiDependencies{Reads: Reads{Accounts: repo, Groups: groupRepo}, Shared: Shared{Cache: cache}}, nil)

	acc, err := svc.SelectAccountForModelWithExclusions(ctx, &groupID, "", "gemini-2.5-flash", nil)
	require.NoError(t, err)
	require.NotNil(t, acc)
	require.Equal(t, 0, groupRepo.getByIDCalls)
	require.Equal(t, 1, groupRepo.getByIDLiteCalls)
}

// TestGeminiMessagesCompatService_SelectAccountForModelWithExclusions_AntigravityGroup 测试 antigravity 分组
func TestGeminiMessagesCompatService_SelectAccountForModelWithExclusions_AntigravityGroup(t *testing.T) {
	ctx := context.Background()

	repo := &mockAccountRepoForGemini{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformGemini, Priority: 1, Status: billing.StatusActive, Schedulable: true}},      // 应被隔离
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformAntigravity, Priority: 1, Status: billing.StatusActive, Schedulable: true}}, // 应被选择
		},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
	}

	cache := &mockGatewayCacheForGemini{}
	groupRepo := &mockGroupRepoForGemini{
		groups: map[int64]*routing.Group{
			1: {ID: 1, Platform: capability.PlatformAntigravity},
		},
	}

	svc := newGeminiSelectionForTest(GeminiDependencies{Reads: Reads{Groups: groupRepo, Accounts: repo}, Shared: Shared{Cache: cache}}, nil)

	groupID := int64(1)
	acc, err := svc.SelectAccountForModelWithExclusions(ctx, &groupID, "", "gemini-2.5-flash", nil)
	require.NoError(t, err)
	require.NotNil(t, acc)
	require.Equal(t, int64(2), acc.Record.ID)
	require.Equal(t, capability.PlatformAntigravity, acc.Record.Platform, "antigravity 分组应只返回 antigravity 账户")
}

// TestGeminiMessagesCompatService_SelectAccountForModelWithExclusions_OAuthPreferred 测试 OAuth 优先
func TestGeminiMessagesCompatService_SelectAccountForModelWithExclusions_OAuthPreferred(t *testing.T) {
	ctx := context.Background()

	repo := &mockAccountRepoForGemini{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformGemini, Type: capability.AccountTypeAPIKey, Priority: 1, Status: billing.StatusActive, Schedulable: true, LastUsedAt: nil}},
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformGemini, Type: capability.AccountTypeOAuth, Priority: 1, Status: billing.StatusActive, Schedulable: true, LastUsedAt: nil}},
		},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
	}

	cache := &mockGatewayCacheForGemini{}
	groupRepo := &mockGroupRepoForGemini{groups: map[int64]*routing.Group{}}

	svc := newGeminiSelectionForTest(GeminiDependencies{Reads: Reads{Accounts: repo, Groups: groupRepo}, Shared: Shared{Cache: cache}}, nil)

	acc, err := svc.SelectAccountForModelWithExclusions(ctx, nil, "", "gemini-2.5-flash", nil)
	require.NoError(t, err)
	require.NotNil(t, acc)
	require.Equal(t, int64(2), acc.Record.ID, "同优先级且都未使用时，应优先选择 OAuth 账户")
	require.Equal(t, capability.AccountTypeOAuth, acc.Record.Type)
}

// TestGeminiMessagesCompatService_SelectAccountForModelWithExclusions_NoAvailableAccounts 测试无可用账户
func TestGeminiMessagesCompatService_SelectAccountForModelWithExclusions_NoAvailableAccounts(t *testing.T) {
	ctx := context.Background()

	repo := &mockAccountRepoForGemini{
		accounts:     []gatewayprovider.ExecutionAccount{},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
	}

	cache := &mockGatewayCacheForGemini{}
	groupRepo := &mockGroupRepoForGemini{groups: map[int64]*routing.Group{}}

	svc := newGeminiSelectionForTest(GeminiDependencies{Reads: Reads{Accounts: repo, Groups: groupRepo}, Shared: Shared{Cache: cache}}, nil)

	acc, err := svc.SelectAccountForModelWithExclusions(ctx, nil, "", "gemini-2.5-flash", nil)
	require.Error(t, err)
	require.Nil(t, acc)
	require.Contains(t, err.Error(), "no available")
}

// TestGeminiMessagesCompatService_SelectAccountForModelWithExclusions_StickySession 测试粘性会话
func TestGeminiMessagesCompatService_SelectAccountForModelWithExclusions_StickySession(t *testing.T) {
	ctx := context.Background()

	t.Run("粘性会话命中-同平台", func(t *testing.T) {
		repo := &mockAccountRepoForGemini{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformGemini, Priority: 2, Status: billing.StatusActive, Schedulable: true}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformGemini, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		// 注意：缓存键使用 "gemini:" 前缀
		cache := &mockGatewayCacheForGemini{
			sessionBindings: map[string]int64{"gemini:session-123": 1},
		}
		groupRepo := &mockGroupRepoForGemini{groups: map[int64]*routing.Group{}}

		svc := newGeminiSelectionForTest(GeminiDependencies{Reads: Reads{Accounts: repo, Groups: groupRepo}, Shared: Shared{Cache: cache}}, nil)

		acc, err := svc.SelectAccountForModelWithExclusions(ctx, nil, "session-123", "gemini-2.5-flash", nil)
		require.NoError(t, err)
		require.NotNil(t, acc)
		require.Equal(t, int64(1), acc.Record.ID, "应返回粘性会话绑定的账户")
	})

	t.Run("粘性会话平台不匹配-降级选择", func(t *testing.T) {
		repo := &mockAccountRepoForGemini{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAntigravity, Priority: 2, Status: billing.StatusActive, Schedulable: true}}, // 粘性会话绑定
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformGemini, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForGemini{
			sessionBindings: map[string]int64{"gemini:session-123": 1}, // 绑定 antigravity 账户
		}
		groupRepo := &mockGroupRepoForGemini{groups: map[int64]*routing.Group{}}

		svc := newGeminiSelectionForTest(GeminiDependencies{Reads: Reads{Accounts: repo, Groups: groupRepo}, Shared: Shared{Cache: cache}}, nil)

		// 无分组时使用 gemini 平台，粘性会话绑定的 antigravity 账户平台不匹配
		acc, err := svc.SelectAccountForModelWithExclusions(ctx, nil, "session-123", "gemini-2.5-flash", nil)
		require.NoError(t, err)
		require.NotNil(t, acc)
		require.Equal(t, int64(2), acc.Record.ID, "粘性会话账户平台不匹配，应降级选择 gemini 账户")
		require.Equal(t, capability.PlatformGemini, acc.Record.Platform)
	})

	t.Run("粘性会话不命中无前缀缓存键", func(t *testing.T) {
		repo := &mockAccountRepoForGemini{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformGemini, Priority: 2, Status: billing.StatusActive, Schedulable: true}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformGemini, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		// 缓存键没有 "gemini:" 前缀，不应命中
		cache := &mockGatewayCacheForGemini{
			sessionBindings: map[string]int64{"session-123": 1},
		}
		groupRepo := &mockGroupRepoForGemini{groups: map[int64]*routing.Group{}}

		svc := newGeminiSelectionForTest(GeminiDependencies{Reads: Reads{Accounts: repo, Groups: groupRepo}, Shared: Shared{Cache: cache}}, nil)

		acc, err := svc.SelectAccountForModelWithExclusions(ctx, nil, "session-123", "gemini-2.5-flash", nil)
		require.NoError(t, err)
		require.NotNil(t, acc)
		// 粘性会话未命中，按优先级选择
		require.Equal(t, int64(2), acc.Record.ID, "粘性会话未命中，应按优先级选择")
	})

	t.Run("粘性会话不可调度-清理并回退选择", func(t *testing.T) {
		repo := &mockAccountRepoForGemini{
			accounts: []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformGemini, Priority: 2, Status: billing.StatusDisabled, Schedulable: true}},
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformGemini, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
			},
			accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
		}
		for i := range repo.accounts {
			repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
		}

		cache := &mockGatewayCacheForGemini{
			sessionBindings: map[string]int64{"gemini:session-123": 1},
		}
		groupRepo := &mockGroupRepoForGemini{groups: map[int64]*routing.Group{}}

		svc := newGeminiSelectionForTest(GeminiDependencies{Reads: Reads{Accounts: repo, Groups: groupRepo}, Shared: Shared{Cache: cache}}, nil)

		acc, err := svc.SelectAccountForModelWithExclusions(ctx, nil, "session-123", "gemini-2.5-flash", nil)
		require.NoError(t, err)
		require.NotNil(t, acc)
		require.Equal(t, int64(2), acc.Record.ID)
		require.Equal(t, 1, cache.deletedSessions["gemini:session-123"])
		require.Equal(t, int64(2), cache.sessionBindings["gemini:session-123"])
	})
}

func TestGeminiMessagesCompatService_SelectAccountForModelWithExclusions_ForcePlatformFallback(t *testing.T) {
	ctx := context.Background()
	groupID := int64(9)
	ctx = apikey.WithForcePlatform(ctx, capability.PlatformAntigravity)

	repo := &mockAccountRepoForGemini{
		listByGroupFunc: func(ctx context.Context, groupID int64, platforms []string) ([]gatewayprovider.ExecutionAccount, error) {
			return nil, nil
		},
		listByPlatformFunc: func(ctx context.Context, platforms []string) ([]gatewayprovider.ExecutionAccount, error) {
			return []gatewayprovider.ExecutionAccount{
				{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAntigravity, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
			}, nil
		},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{
			1: {Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAntigravity, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
		},
	}

	cache := &mockGatewayCacheForGemini{}
	groupRepo := &mockGroupRepoForGemini{groups: map[int64]*routing.Group{
		groupID: {ID: groupID, Platform: capability.PlatformAntigravity, Status: billing.StatusActive},
	}}

	svc := newGeminiSelectionForTest(GeminiDependencies{Reads: Reads{Accounts: repo, Groups: groupRepo}, Shared: Shared{Cache: cache}}, nil)

	acc, err := svc.SelectAccountForModelWithExclusions(ctx, &groupID, "", "gemini-2.5-flash", nil)
	require.NoError(t, err)
	require.NotNil(t, acc)
	require.Equal(t, int64(1), acc.Record.ID)
}

func TestGeminiMessagesCompatService_SelectAccountForModelWithExclusions_NoModelSupport(t *testing.T) {
	ctx := context.Background()

	repo := &mockAccountRepoForGemini{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1,
				Platform:    capability.PlatformGemini,
				Priority:    1,
				Status:      billing.StatusActive,
				Schedulable: true,
				Credentials: map[string]any{"model_mapping": map[string]any{"gemini-1.0-pro": "gemini-1.0-pro"}}},
			},
		},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
	}

	cache := &mockGatewayCacheForGemini{}
	groupRepo := &mockGroupRepoForGemini{groups: map[int64]*routing.Group{}}

	svc := newGeminiSelectionForTest(GeminiDependencies{Reads: Reads{Accounts: repo, Groups: groupRepo}, Shared: Shared{Cache: cache}}, nil)

	acc, err := svc.SelectAccountForModelWithExclusions(ctx, nil, "", "gemini-2.5-flash", nil)
	require.Error(t, err)
	require.Nil(t, acc)
	var modelErr *routing.GroupModelUnsupportedError
	require.True(t, errors.As(err, &modelErr))
	require.Equal(t, capability.PlatformGemini, modelErr.Platform)
	require.Equal(t, "gemini-2.5-flash", modelErr.RequestedModel)
	require.Equal(t, []string{"gemini-1.0-pro"}, modelErr.AvailableModels)
	require.Contains(t, err.Error(), `The current group does not support the requested model "gemini-2.5-flash"`)
	require.Contains(t, err.Error(), "Available models: gemini-1.0-pro")
}

func TestGeminiMessagesCompatService_SelectAccountForModelWithExclusions_ModelRateLimitedNotGroupUnsupported(t *testing.T) {
	ctx := context.Background()
	resetAt := time.Now().Add(10 * time.Minute).Format(time.RFC3339)

	repo := &mockAccountRepoForGemini{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1,
				Platform:    capability.PlatformGemini,
				Priority:    1,
				Status:      billing.StatusActive,
				Schedulable: true,
				Credentials: map[string]any{"model_mapping": map[string]any{"gemini-2.5-flash": "gemini-2.5-flash"}},
				Extra: map[string]any{"model_rate_limits": map[string]any{
					"gemini-2.5-flash": map[string]any{"rate_limit_reset_at": resetAt},
				},
				}},
			},
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2,
				Platform:    capability.PlatformGemini,
				Priority:    2,
				Status:      billing.StatusActive,
				Schedulable: true,
				Credentials: map[string]any{"model_mapping": map[string]any{"gemini-1.0-pro": "gemini-1.0-pro"}}},
			},
		},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
	}

	svc := newGeminiSelectionForTest(GeminiDependencies{
		Reads: Reads{Accounts: repo, Groups: &mockGroupRepoForGemini{groups: map[int64]*routing.Group{}}},

		Shared: Shared{Cache: &mockGatewayCacheForGemini{}},
	}, nil,
	)

	acc, err := svc.SelectAccountForModelWithExclusions(ctx, nil, "", "gemini-2.5-flash", nil)
	require.Error(t, err)
	require.Nil(t, acc)
	var modelErr *routing.GroupModelUnsupportedError
	require.False(t, errors.As(err, &modelErr))
	require.Contains(t, err.Error(), "supporting model")
}

func TestGeminiMessagesCompatService_SelectAccountForModelWithExclusions_StickyMixedScheduling(t *testing.T) {
	ctx := context.Background()
	repo := &mockAccountRepoForGemini{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAntigravity, Priority: 1, Status: billing.StatusActive, Schedulable: true, Extra: map[string]any{"mixed_scheduling": true}}},
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformGemini, Priority: 2, Status: billing.StatusActive, Schedulable: true}},
		},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
	}

	cache := &mockGatewayCacheForGemini{
		sessionBindings: map[string]int64{"gemini:session-999": 1},
	}
	groupRepo := &mockGroupRepoForGemini{groups: map[int64]*routing.Group{}}

	svc := newGeminiSelectionForTest(GeminiDependencies{Reads: Reads{Accounts: repo, Groups: groupRepo}, Shared: Shared{Cache: cache}}, nil)

	acc, err := svc.SelectAccountForModelWithExclusions(ctx, nil, "session-999", "gemini-2.5-flash", nil)
	require.NoError(t, err)
	require.NotNil(t, acc)
	require.Equal(t, int64(1), acc.Record.ID)
}

func TestGeminiMessagesCompatService_SelectAccountForModelWithExclusions_SkipDisabledMixedScheduling(t *testing.T) {
	ctx := context.Background()
	repo := &mockAccountRepoForGemini{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformAntigravity, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformGemini, Priority: 2, Status: billing.StatusActive, Schedulable: true}},
		},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
	}

	cache := &mockGatewayCacheForGemini{}
	groupRepo := &mockGroupRepoForGemini{groups: map[int64]*routing.Group{}}

	svc := newGeminiSelectionForTest(GeminiDependencies{Reads: Reads{Groups: groupRepo, Accounts: repo}, Shared: Shared{Cache: cache}}, nil)

	acc, err := svc.SelectAccountForModelWithExclusions(ctx, nil, "", "gemini-2.5-flash", nil)
	require.NoError(t, err)
	require.NotNil(t, acc)
	require.Equal(t, int64(2), acc.Record.ID)
}

func TestGeminiMessagesCompatService_SelectAccountForModelWithExclusions_ExcludedAccount(t *testing.T) {
	ctx := context.Background()
	repo := &mockAccountRepoForGemini{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformGemini, Priority: 1, Status: billing.StatusActive, Schedulable: true}},
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformGemini, Priority: 2, Status: billing.StatusActive, Schedulable: true}},
		},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
	}

	cache := &mockGatewayCacheForGemini{}
	groupRepo := &mockGroupRepoForGemini{groups: map[int64]*routing.Group{}}

	svc := newGeminiSelectionForTest(GeminiDependencies{Reads: Reads{Accounts: repo, Groups: groupRepo}, Shared: Shared{Cache: cache}}, nil)

	excluded := map[int64]struct{}{1: {}}
	acc, err := svc.SelectAccountForModelWithExclusions(ctx, nil, "", "gemini-2.5-flash", excluded)
	require.NoError(t, err)
	require.NotNil(t, acc)
	require.Equal(t, int64(2), acc.Record.ID)
}

func TestGeminiMessagesCompatService_SelectAccountForModelWithExclusions_ListError(t *testing.T) {
	ctx := context.Background()
	repo := &mockAccountRepoForGemini{
		listByPlatformFunc: func(ctx context.Context, platforms []string) ([]gatewayprovider.ExecutionAccount, error) {
			return nil, errors.New("query failed")
		},
	}

	cache := &mockGatewayCacheForGemini{}
	groupRepo := &mockGroupRepoForGemini{groups: map[int64]*routing.Group{}}

	svc := newGeminiSelectionForTest(GeminiDependencies{Reads: Reads{Accounts: repo, Groups: groupRepo}, Shared: Shared{Cache: cache}}, nil)

	acc, err := svc.SelectAccountForModelWithExclusions(ctx, nil, "", "gemini-2.5-flash", nil)
	require.Error(t, err)
	require.Nil(t, acc)
	require.Contains(t, err.Error(), "query accounts failed")
}

func TestGeminiMessagesCompatService_SelectAccountForModelWithExclusions_PreferOAuth(t *testing.T) {
	ctx := context.Background()
	repo := &mockAccountRepoForGemini{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformGemini, Priority: 1, Status: billing.StatusActive, Schedulable: true, Type: capability.AccountTypeAPIKey}},
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformGemini, Priority: 1, Status: billing.StatusActive, Schedulable: true, Type: capability.AccountTypeOAuth}},
		},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
	}

	cache := &mockGatewayCacheForGemini{}
	groupRepo := &mockGroupRepoForGemini{groups: map[int64]*routing.Group{}}

	svc := newGeminiSelectionForTest(GeminiDependencies{Reads: Reads{Groups: groupRepo, Accounts: repo}, Shared: Shared{Cache: cache}}, nil)

	acc, err := svc.SelectAccountForModelWithExclusions(ctx, nil, "", "gemini-2.5-pro", nil)
	require.NoError(t, err)
	require.NotNil(t, acc)
	require.Equal(t, int64(2), acc.Record.ID)
}

func TestGeminiMessagesCompatService_SelectAccountForModelWithExclusions_PreferLeastRecentlyUsed(t *testing.T) {
	ctx := context.Background()
	oldTime := time.Now().Add(-2 * time.Hour)
	newTime := time.Now().Add(-1 * time.Hour)
	repo := &mockAccountRepoForGemini{
		accounts: []gatewayprovider.ExecutionAccount{
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Platform: capability.PlatformGemini, Priority: 1, Status: billing.StatusActive, Schedulable: true, LastUsedAt: &newTime}},
			{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2, Platform: capability.PlatformGemini, Priority: 1, Status: billing.StatusActive, Schedulable: true, LastUsedAt: &oldTime}},
		},
		accountsByID: map[int64]*gatewayprovider.ExecutionAccount{},
	}
	for i := range repo.accounts {
		repo.accountsByID[repo.accounts[i].Record.ID] = &repo.accounts[i]
	}

	cache := &mockGatewayCacheForGemini{}
	groupRepo := &mockGroupRepoForGemini{groups: map[int64]*routing.Group{}}

	svc := newGeminiSelectionForTest(GeminiDependencies{Reads: Reads{Accounts: repo, Groups: groupRepo}, Shared: Shared{Cache: cache}}, nil)

	acc, err := svc.SelectAccountForModelWithExclusions(ctx, nil, "", "gemini-2.5-pro", nil)
	require.NoError(t, err)
	require.NotNil(t, acc)
	require.Equal(t, int64(2), acc.Record.ID)
}

// TestGeminiPlatformRouting_DocumentRouteDecision 测试平台路由决策逻辑
func TestGeminiPlatformRouting_DocumentRouteDecision(t *testing.T) {
	tests := []struct {
		name            string
		platform        string
		expectedService string // "gemini" 表示 ForwardNative, "antigravity" 表示 ForwardGemini
	}{
		{
			name:            "Gemini平台走ForwardNative",
			platform:        capability.PlatformGemini,
			expectedService: "gemini",
		},
		{
			name:            "Antigravity平台走ForwardGemini",
			platform:        capability.PlatformAntigravity,
			expectedService: "antigravity",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation:

			// 模拟 Handler 层的路由逻辑
			time.LoadLocation, Platform: tt.platform}}

			var serviceName string
			if account.Record.Platform == capability.PlatformAntigravity {
				serviceName = "antigravity"
			} else {
				serviceName = "gemini"
			}

			require.Equal(t, tt.expectedService, serviceName,
				"平台 %s 应该路由到 %s 服务", tt.platform, tt.expectedService)
		})
	}
}

func TestGeminiMessagesCompatService_isModelSupportedByAccount(t *testing.T) {
	svc := newGeminiSelectionForTest(GeminiDependencies{Reads: Reads{}, Shared: Shared{}}, nil)

	tests := []struct {
		name     string
		account  *gatewayprovider.ExecutionAccount
		model    string
		expected bool
	}{
		{
			name:     "Antigravity平台-支持gemini模型",
			account:  &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAntigravity}},
			model:    "gemini-2.5-flash",
			expected: true,
		},
		{
			name:     "Antigravity平台-支持claude模型",
			account:  &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAntigravity}},
			model:    "claude-sonnet-4-5",
			expected: true,
		},
		{
			name:     "Antigravity平台-不支持gpt模型",
			account:  &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAntigravity}},
			model:    "gpt-4",
			expected: false,
		},
		{
			name:     "Antigravity平台-空模型允许",
			account:  &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAntigravity}},
			model:    "",
			expected: true,
		},
		{
			name: "Antigravity平台-自定义映射-支持自定义模型",
			account: &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAntigravity,
				Credentials: map[string]any{
					"model_mapping": map[string]any{
						"my-custom-model": "upstream-model",
						"gpt-4o":          "some-model",
					},
				}},
			},
			model:    "my-custom-model",
			expected: true,
		},
		{
			name: "Antigravity平台-自定义映射-不在映射中的模型不支持",
			account: &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformAntigravity,
				Credentials: map[string]any{
					"model_mapping": map[string]any{
						"my-custom-model": "upstream-model",
					},
				}},
			},
			model:    "claude-sonnet-4-5",
			expected: false,
		},
		{
			name:     "Gemini平台-无映射配置-支持所有模型",
			account:  &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformGemini}},
			model:    "gemini-2.5-flash",
			expected: true,
		},
		{
			name: "Gemini平台-有映射配置-未命中映射时按透传支持模型",
			account: &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, Platform: capability.PlatformGemini,
				Credentials: map[string]any{"model_mapping": map[string]any{"gemini-2.5-pro": "x"}}},
			},
			model:    "gemini-2.5-flash",
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
