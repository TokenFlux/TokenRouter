package provider

import (
	"context"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// 合并网络操作不能使两个管理请求共享可修改的用量结果。
func TestS06UpstreamUsageSingleflightResultIsolation(t *testing.T) {
	value := &accountcore.Record{ID: 5, Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey, Status: accountcore.StatusActive, Credentials: map[string]any{"api_key": "fixture", "base_url": "https://usage.example/v1"}}
	repo := &upstreamUsageAccountRepoStub{account: value, getEvent: make(chan struct{}, 16)}
	upstream := &blockingUpstreamUsageHTTP{started: make(chan struct{}), release: make(chan struct{}), body: `{"isValid":true,"mode":"unrestricted","unit":"USD","planName":"payg","remaining":3,"balance":3}`}
	service := newUsageContractService(repo, upstream, testUpstreamUsageConfig())
	results := make(chan *accountcore.UpstreamUsageQueryResult, 2)
	errs := make(chan error, 2)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	run := func() { result, err := service.QueryAccount(ctx, value.ID); results <- result; errs <- err }
	go run()
	select {
	case <-upstream.started:
	case <-ctx.Done():
		t.Fatal("首次查询未开始")
	}
	for len(repo.getEvent) > 0 {
		<-repo.getEvent
	}
	go run()
	select {
	case <-repo.getEvent:
	case <-ctx.Done():
		t.Fatal("等待方未读取身份")
	}
	// 与原有独立取消夹具相同，预读完成后让等待方进入 singleflight。
	time.Sleep(20 * time.Millisecond)
	close(upstream.release)
	first, second := <-results, <-results
	require.NoError(t, <-errs)
	require.NoError(t, <-errs)
	require.Equal(t, int32(1), upstream.calls.Load())
	*first.Balance.Remaining = 91
	require.Equal(t, 3.0, *second.Balance.Remaining)
	require.Equal(t, 3.0, *second.Usage.Balance.Remaining)
}
