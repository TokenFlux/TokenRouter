package systemone

import (
	"context"
	"errors"

	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// ErrNoProvider 表示协议和模型筛选后没有可用提供商。
var ErrNoProvider = errors.New("no available SystemOne provider")

// Outcome 保存一次交换结果及网关允许的重试状态。
type Outcome struct {
	Result upstream.AttemptResult
	Err    error
	Retry  bool
}

// Ports 绑定当前请求的选号、并发、交换、完成和健康反馈。
type Ports interface {
	Select(context.Context, map[int64]struct{}) (provider.ProviderSnapshot, error)
	Acquire(context.Context) (func(), bool)
	Forward(context.Context) Outcome
	Report(context.Context, Outcome)
	Complete(context.Context, upstream.AttemptResult)
	MissingUsage(context.Context, upstream.AttemptResult)
	Switch()
}

// Run 在输出前有限换号，成功答案的完成快照在释放提供商槽之前捕获。
// @project-doc docs/interfaces/jev_upstream.md#systemone_usage
func Run(ctx context.Context, maxSwitches int, ports Ports) error {
	excluded := make(map[int64]struct{})
	var last error
	for attempt := 0; attempt <= max(0, maxSwitches); attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		selected, err := ports.Select(ctx, excluded)
		if err != nil {
			if last != nil {
				return last
			}
			return err
		}
		if selected.ID == 0 {
			if last != nil {
				return last
			}
			return ErrNoProvider
		}
		release, ok := ports.Acquire(ctx)
		if !ok {
			return ctx.Err()
		}
		outcome := func() Outcome {
			if release != nil {
				defer release()
			}
			result := ports.Forward(ctx)
			ports.Report(ctx, result)
			if result.Result.Served {
				if result.Result.HasUsage {
					ports.Complete(ctx, result.Result)
				} else {
					ports.MissingUsage(ctx, result.Result)
				}
			}
			return result
		}()
		if outcome.Result.Served || outcome.Err == nil || !outcome.Retry || outcome.Result.HTTPCommitted || outcome.Result.RetryCommitted {
			return outcome.Err
		}
		last = outcome.Err
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if attempt == max(0, maxSwitches) {
			break
		}
		excluded[selected.ID] = struct{}{}
		ports.Switch()
	}
	return last
}
