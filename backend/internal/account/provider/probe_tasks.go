package provider

import (
	"context"
	"sync"

	"github.com/TokenFlux/TokenRouter/internal/account"
)

// ProbeTasks 为账号测试和模型预览共用原未持久账号互斥；持久账号仍由统一协调器认领。
type ProbeTasks struct {
	Coordinator *account.OpenAITaskCoordinator
	Options     account.OpenAITaskOptions
	fallback    sync.Mutex
}

func (p *ProbeTasks) Ensure(ctx context.Context, value *account.Record, expected string) error {
	options := p.Options
	options.FallbackMutex = &p.fallback
	return p.Coordinator.Ensure(ctx, options, value, expected)
}
