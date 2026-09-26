package selection

import (
	"context"
	"errors"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/rediscache/codec"
)

// responseSelectionOptions 保留原 WSv2 夹具的显式开关和零值等待配置。
func responseSelectionOptions() Options {
	return Options{
		WS: &egress.OpenAIWSOptions{Enabled: true, OAuthEnabled: true, APIKeyEnabled: true, ResponsesWebsocketsV2: true},

		StickyTTL:   time.Hour,
		ResponseTTL: time.Hour,
	}
}

func responseSelectionParameters() *scheduler.Parameters {
	return scheduler.NewParameters(scheduler.NewSettingsRuntime(scheduler.Diagnostics{}), nil, diagnosticParameterDefaults(&config.Config{}))
}

// selectPreviousResponseForTest 组合原入口的上下文及模型投影，不为私有合同扩大生产 API。
func selectPreviousResponseForTest(s *Compatible, ctx context.Context, group *int64, previous, model string, excluded map[int64]struct{}, compact bool) (*provider.SelectionResult, error) {
	ctx = s.withOpenAIGroupPrivacyRequirement(ctx, group)
	model = s.resolveGroupRoutingModel(ctx, group, model)
	return s.selectAccountByPreviousResponseIDForCapability(ctx, group, previous, model, excluded, "", compact)
}

// 以下替身仅实现选择合同实际使用的读取；意外访问其他能力直接暴露测试缺口。
type selectionAccountFixture struct {
	Accounts
	accounts []provider.ExecutionAccount
}

func (r selectionAccountFixture) GetByID(_ context.Context, id int64) (*provider.ExecutionAccount, error) {
	for i := range r.accounts {
		if r.accounts[i].Record.ID == id {
			return &r.accounts[i], nil
		}
	}
	return nil, errors.New("account not found")
}

func (r selectionAccountFixture) ListSchedulableByPlatform(_ context.Context, platform string) ([]provider.ExecutionAccount, error) {
	var out []provider.ExecutionAccount
	for _, value := range r.accounts {
		if value.Record.Platform == platform {
			out = append(out, value)
		}
	}
	return out, nil
}

func (r selectionAccountFixture) ListSchedulableByGroupIDAndPlatform(ctx context.Context, _ int64, platform string) ([]provider.ExecutionAccount, error) {
	return r.ListSchedulableByPlatform(ctx, platform)
}

func (r selectionAccountFixture) ListSchedulableUngroupedByPlatform(ctx context.Context, platform string) ([]provider.ExecutionAccount, error) {
	return r.ListSchedulableByPlatform(ctx, platform)
}

type responseCacheFixture struct {
	stickyCacheFixture
	session.GatewayCache
}

func (c *responseCacheFixture) GetSessionAccountID(ctx context.Context, group int64, key string) (int64, error) {
	return c.stickyCacheFixture.GetSessionAccountID(ctx, group, key)
}

func (c *responseCacheFixture) SetSessionAccountID(ctx context.Context, group int64, key string, id int64, ttl time.Duration) error {
	return c.stickyCacheFixture.SetSessionAccountID(ctx, group, key, id, ttl)
}

func (c *responseCacheFixture) RefreshSessionTTL(ctx context.Context, group int64, key string, ttl time.Duration) error {
	return c.stickyCacheFixture.RefreshSessionTTL(ctx, group, key, ttl)
}

func (c *responseCacheFixture) DeleteSessionAccountID(ctx context.Context, group int64, key string) error {
	return c.stickyCacheFixture.DeleteSessionAccountID(ctx, group, key)
}

type selectionConcurrencyFixture struct {
	scheduler.ConcurrencyCache
	acquireResults  map[int64]bool
	waitCounts      map[int64]int
	loadBatchErr    error
	loadMap         map[int64]*scheduler.AccountLoadInfo
	skipDefaultLoad bool
}

func (c selectionConcurrencyFixture) AcquireAccountSlot(_ context.Context, id int64, _ int, _ string) (bool, error) {
	if value, ok := c.acquireResults[id]; ok {
		return value, nil
	}
	return true, nil
}

func (selectionConcurrencyFixture) ReleaseAccountSlot(context.Context, int64, string) error {
	return nil
}

func (c selectionConcurrencyFixture) GetAccountWaitingCount(_ context.Context, id int64) (int, error) {
	return c.waitCounts[id], nil
}

func (c selectionConcurrencyFixture) GetAccountsLoadBatch(ctx context.Context, accounts []scheduler.AccountWithConcurrency) (map[int64]*scheduler.AccountLoadInfo, error) {
	if c.loadBatchErr != nil {
		return nil, c.loadBatchErr
	}
	out := make(map[int64]*scheduler.AccountLoadInfo, len(accounts))
	if c.skipDefaultLoad && c.loadMap != nil {
		for _, acc := range accounts {
			if load, ok := c.loadMap[acc.ID]; ok {
				out[acc.ID] = load
			}
		}
		return out, nil
	}
	for _, acc := range accounts {
		if c.loadMap != nil {
			if load, ok := c.loadMap[acc.ID]; ok {
				out[acc.ID] = load
				continue
			}
		}
		out[acc.ID] = &scheduler.AccountLoadInfo{AccountID: acc.ID, LoadRate: 0}
	}
	return out, nil
}

// selectionSnapshotFixture 经真实快照读取器解码，数据库重检仍读取另一份新状态。
type selectionSnapshotFixture struct {
	scheduler.SnapshotCache
	accountsByID map[int64]*provider.ExecutionAccount
}

func (s *selectionSnapshotFixture) GetAccount(_ context.Context, id int64) (scheduler.SnapshotAccount, error) {
	value := s.accountsByID[id]
	if value == nil {
		return nil, nil
	}
	copy := value.Record
	return codec.WrapRecord(&copy), nil
}

type groupAwareStubOpenAIAccountRepo struct {
	selectionAccountFixture
}

func (r groupAwareStubOpenAIAccountRepo) ListSchedulableByGroupIDAndPlatform(ctx context.Context, groupID int64, platform string) ([]provider.ExecutionAccount, error) {
	var result []provider.ExecutionAccount
	for _, acc := range r.accounts {
		if acc.Record.Platform == platform && openAIStickyAccountMatchesGroup(&acc, &groupID) {
			result = append(result, acc)
		}
	}
	return result, nil
}

func (r groupAwareStubOpenAIAccountRepo) ListSchedulableUngroupedByPlatform(ctx context.Context, platform string) ([]provider.ExecutionAccount, error) {
	var result []provider.ExecutionAccount
	for _, acc := range r.accounts {
		if acc.Record.Platform == platform && openAIStickyAccountMatchesGroup(&acc, nil) {
			result = append(result, acc)
		}
	}
	return result, nil
}

// codex 配额读取合同保留写入哨兵，任何原不应发生的持久化仍使断言失败。
type openAICodexExtraListRepo struct {
	selectionAccountFixture
	rateLimitCh chan time.Time
}

func (r *openAICodexExtraListRepo) SetRateLimited(_ context.Context, _ int64, at time.Time) error {
	if r.rateLimitCh != nil {
		r.rateLimitCh <- at
	}
	return nil
}

// hydrationAccountSource 保留原回源错误，不自行模拟补全成功或失败。
type hydrationAccountSource struct {
	scheduler.SnapshotAccountSource
	source Accounts
}

func (s hydrationAccountSource) GetByID(ctx context.Context, id int64) (scheduler.SnapshotAccount, error) {
	value, err := s.source.GetByID(ctx, id)
	return codec.WrapRecord(provider.ExecutionRecord(value)), err
}
