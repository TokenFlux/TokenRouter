package legacybridge

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// RoutingProbe 仅投影旧执行句柄；测试已委托账号事件用例，平台选择在 S07、供应商执行在 S09 改绑。
type RoutingProbe struct{ Execution service.GroupProbeExecution }

func (p RoutingProbe) Select(ctx context.Context, group routing.GroupAvailabilityProbeDueGroup, model string) (int64, error) {
	return p.Execution.Select(ctx, group, model)
}
func (p RoutingProbe) Test(ctx context.Context, id int64, model, prompt, userAgent string) (*routing.ProbeExecutionResult, error) {
	return p.Execution.Test(ctx, id, model, prompt, userAgent)
}
