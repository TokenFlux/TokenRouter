package app

import (
	"context"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/stretchr/testify/require"
)

// creativeExecutionGroupProbe 只提供原生分组读取，验证装配不提前取得账号或启动尝试。
type creativeExecutionGroupProbe struct{ reads int }

func (p *creativeExecutionGroupProbe) GetByIDLite(context.Context, int64) (*routing.Group, error) {
	p.reads++
	return &routing.Group{ID: 12, Platform: "gemini", ResponsesImagePolicy: "inherit"}, nil
}

func TestCreativeExecutorNativeAssemblyPreservesPrepareReads(t *testing.T) {
	groups := &creativeExecutionGroupProbe{}
	cfg := &config.Config{}
	cfg.Creative.ExecuteTimeoutSeconds = 17
	executor := provideCreativeExecutor(cfg, groups, nil, nil, nil)
	require.Zero(t, groups.reads)
	require.Equal(t, 17*time.Second, executor.Timeout)
	_, err := executor.Prepare(context.Background(), creative.CreativeRun{GroupID: 12, Model: "gemini-3.1-flash-image", Operation: creative.CreativeOperationGenerate})
	require.ErrorContains(t, err, "creative gateway service is not configured")
	require.Equal(t, 2, groups.reads, "保持平台读取与协议投影两次原读取时点")
	require.Equal(t, 5*time.Minute, provideCreativeExecutor(nil, nil, nil, nil, nil).Timeout)
}
