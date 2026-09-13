package service

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/stretchr/testify/require"
	"sync/atomic"
	"testing"
	"time"
)

type s06RefreshStartPager struct {
	AccountRepository
	calls   atomic.Int32
	entered chan struct{}
}

func (p *s06RefreshStartPager) ListOAuthRefreshCandidatePage(ctx context.Context, _ OAuthRefreshPageOptions) (*OAuthRefreshCandidatePage, error) {
	p.calls.Add(1)
	p.entered <- struct{}{}
	<-ctx.Done()
	return nil, ctx.Err()
}

// 重复 Start 不能并发开启两轮立即扫描，即使两个循环共享同一个取消 context。
func TestS06TokenRefreshStartIsIdempotent(t *testing.T) {
	p := &s06RefreshStartPager{entered: make(chan struct{}, 2)}
	s := NewTokenRefreshService(p, nil, nil, nil, nil, nil, nil, &config.Config{TokenRefresh: config.TokenRefreshConfig{Enabled: true}}, nil, nil, nil)
	defer s.Stop()
	require.Zero(t, p.calls.Load())
	s.Start()
	select {
	case <-p.entered:
	case <-time.After(time.Second):
		t.Fatal("首轮扫描未启动")
	}
	s.Start()
	select {
	case <-p.entered:
	case <-time.After(30 * time.Millisecond):
	}
	require.Equal(t, int32(1), p.calls.Load(), "重复启动产生并行扫描")
}
