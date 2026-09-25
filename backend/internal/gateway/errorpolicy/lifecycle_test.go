package errorpolicy

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 受控存储固定回源交错，不依赖修复前能够越过更新屏障。
type controlledRules struct {
	ErrorPassthroughRepository
	mu      sync.Mutex
	current *ErrorPassthroughRule
	entered chan struct{}
	resume  chan struct{}
	block   bool
}

func (r *controlledRules) List(ctx context.Context) ([]*ErrorPassthroughRule, error) {
	r.mu.Lock()
	snapshot := cloneRule(r.current)
	block := r.block
	r.block = false
	r.mu.Unlock()
	if block {
		close(r.entered)
		select {
		case <-r.resume:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return []*ErrorPassthroughRule{snapshot}, nil
}
func (r *controlledRules) Update(_ context.Context, rule *ErrorPassthroughRule) (*ErrorPassthroughRule, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.current = cloneRule(rule)
	return cloneRule(rule), nil
}
func fixtureRule() *ErrorPassthroughRule {
	message := "original"
	code := 502
	return &ErrorPassthroughRule{ID: 1, Name: "fixture", Enabled: true, MatchMode: MatchModeAny, ErrorCodes: []int{503}, ResponseCode: &code, CustomMessage: &message, Keywords: []string{}, Platforms: []string{"openai"}}
}
func blockedRules() *controlledRules {
	return &controlledRules{current: fixtureRule(), entered: make(chan struct{}), resume: make(chan struct{}), block: true}
}

// 旧回源和管理写入只能依次发布，不能在禁用完成后恢复旧规则。
func TestRuleUpdateCannotBeOverwrittenByOlderLoad(t *testing.T) {
	repo := blockedRules()
	svc := NewErrorPassthroughService(repo, nil)
	defer svc.Stop()
	loaded := make(chan error, 1)
	go func() { loaded <- svc.reloadRulesFromDB(context.Background()) }()
	<-repo.entered
	changed := fixtureRule()
	changed.Enabled = false
	updated := make(chan error, 1)
	go func() { _, err := svc.Update(context.Background(), changed); updated <- err }()
	close(repo.resume)
	require.NoError(t, <-loaded)
	require.NoError(t, <-updated)
	require.Nil(t, svc.MatchRule("openai", 503, nil))
	// 反向顺序同样保留最新状态，后续回源读取权威的新值。
	require.NoError(t, svc.reloadRulesFromDB(context.Background()))
	require.Nil(t, svc.MatchRule("openai", 503, nil))
}

func TestRuleUpdateCoordinatorWaitHonorsCancellation(t *testing.T) {
	repo := blockedRules()
	svc := NewErrorPassthroughService(repo, nil)
	defer svc.Stop()
	loaded := make(chan error, 1)
	go func() { loaded <- svc.reloadRulesFromDB(context.Background()) }()
	<-repo.entered
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	changed := fixtureRule()
	changed.Enabled = false
	_, err := svc.Update(ctx, changed)
	close(repo.resume)
	require.NoError(t, <-loaded)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.NotNil(t, svc.MatchRule("openai", 503, nil), "未成功写入不能发布禁用状态")
}

// 输入、结果和嵌套响应动作均与已编译快照独立。
func TestRuleSnapshotOwnership(t *testing.T) {
	source := fixtureRule()
	svc := NewErrorPassthroughService(nil, nil)
	defer svc.Stop()
	svc.setLocalCache([]*ErrorPassthroughRule{source})
	*source.CustomMessage = "input mutation"
	source.Platforms[0] = "other"
	source.ErrorCodes[0] = 400
	first := svc.MatchRule("openai", 503, nil)
	require.NotNil(t, first)
	require.Equal(t, "original", *first.CustomMessage)
	first.Enabled = false
	*first.CustomMessage = "output mutation"
	*first.ResponseCode = 418
	first.Keywords = append(first.Keywords, "changed")
	second := svc.MatchRule("openai", 503, nil)
	require.NotNil(t, second)
	require.Equal(t, "original", *second.CustomMessage)
	require.Equal(t, 502, *second.ResponseCode)
	require.NotNil(t, second.Keywords)
	require.Empty(t, second.Keywords)
}

// 运行取消能够到达启动中的数据库调用，Stop 不必等外部释放夹具。
func TestStopCancelsStartupRuleLoad(t *testing.T) {
	repo := blockedRules()
	svc := NewErrorPassthroughService(repo, nil)
	started := make(chan error, 1)
	go func() { started <- svc.StartContext(context.Background()) }()
	<-repo.entered
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, svc.StopContext(ctx))
	require.ErrorIs(t, <-started, context.Canceled)
	require.NoError(t, svc.StopContext(ctx))
	require.NoError(t, svc.StartContext(ctx))
}

type callbackRuleCache struct {
	ErrorPassthroughCache
	callback func()
	done     chan struct{}
}

func (c *callbackRuleCache) Get(context.Context) ([]*ErrorPassthroughRule, bool) { return nil, false }
func (c *callbackRuleCache) Set(context.Context, []*ErrorPassthroughRule) error  { return nil }
func (c *callbackRuleCache) SubscribeUpdates(_ context.Context, callback func()) {
	c.callback = callback
}
func (c *callbackRuleCache) StopSubscription() {
	if c.done != nil {
		<-c.done
	}
}
func TestStopCancelsSubscriptionRuleLoad(t *testing.T) {
	repo := blockedRules()
	repo.block = false
	cache := &callbackRuleCache{}
	svc := NewErrorPassthroughService(repo, cache)
	require.NoError(t, svc.StartContext(context.Background()))
	repo.mu.Lock()
	repo.block = true
	repo.mu.Unlock()
	cache.done = make(chan struct{})
	go func() { cache.callback(); close(cache.done) }()
	<-repo.entered
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, svc.StopContext(ctx))
}
