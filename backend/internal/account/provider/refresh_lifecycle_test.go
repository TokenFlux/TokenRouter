package provider

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/stretchr/testify/require"
)

type s06RefreshStartPager struct {
	calls   atomic.Int32
	entered chan struct{}
}

func (p *s06RefreshStartPager) ListOAuthRefreshCandidatePage(ctx context.Context, _ account.OAuthRefreshPageOptions) (*account.OAuthRefreshCandidatePage, error) {
	p.calls.Add(1)
	p.entered <- struct{}{}
	<-ctx.Done()
	return nil, ctx.Err()
}

// 重复 Start 不能并发开启两轮立即扫描，即使两个循环共享同一个取消 context。
func TestS06TokenRefreshStartIsIdempotent(t *testing.T) {
	p := &s06RefreshStartPager{entered: make(chan struct{}, 2)}
	s := account.NewBackgroundRefreshService(account.BackgroundRefreshOptions{Tuning: &account.RefreshTuning{Enabled: true}, Pager: p, Registrations: []account.RefreshRegistration{{Platform: account.PlatformOpenAI, Refresher: &tokenRefreshTestRefresher{}}}})
	defer func() { require.NoError(t, s.StopContext(context.Background())) }()
	require.Zero(t, p.calls.Load())
	require.NoError(t, s.StartContext(context.Background()))
	select {
	case <-p.entered:
	case <-time.After(time.Second):
		t.Fatal("首轮扫描未启动")
	}
	require.NoError(t, s.StartContext(context.Background()))
	select {
	case <-p.entered:
	case <-time.After(30 * time.Millisecond):
	}
	require.Equal(t, int32(1), p.calls.Load(), "重复启动产生并行扫描")
}
