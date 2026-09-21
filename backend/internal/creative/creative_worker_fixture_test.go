//go:build unit

package creative_test

import (
	"context"
	"errors"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	testassert "github.com/TokenFlux/TokenRouter/internal/testutil/assertion"
)

// 原 worker 测试显式注入执行与用户准入端口，不重建旧 worker 服务。
func newCreativeWorkerFixtureForSources(queue creative.CreativeRunQueue, repo creative.CreativeRunRepository, store creative.CreativeTransientStore, executor creative.CreativeRunExecutor, public *creative.Public, options creative.CreativeWorkerOptions, limits ...*scheduler.ConcurrencyService) *creative.CreativeRunWorker {
	ports := creative.WorkerPorts{Observe: creativeLegacyObserve}
	var results *creative.Results
	if public != nil {
		results = public.Results
	}
	if len(limits) > 0 && limits[0] != nil && public != nil && public.UserRepo != nil {
		users := testassert.MustType[creativeUserReader](public.UserRepo).source
		ports.UserMissing = func(err error) bool { return errors.Is(err, identity.ErrUserNotFound) }
		ports.AcquireUser = func(ctx context.Context, id int64) (func(), bool, error) {
			user, err := users.GetByID(ctx, id)
			if err != nil {
				return nil, false, err
			}
			if user == nil {
				return nil, false, errors.New("creative run user is unavailable")
			}
			slot, err := limits[0].AcquireUserSlot(ctx, id, user.Concurrency)
			if err != nil {
				return nil, false, err
			}
			if slot == nil {
				return nil, false, nil
			}
			return slot.ReleaseFunc, slot.Acquired, nil
		}
	}
	return creative.NewCreativeRunWorker(queue, repo, store, executor, results, options, ports)
}

func newCreativeRuntimeFixture(worker *creative.CreativeRunWorker, public *creative.Public, cfg *config.Config) *creative.CreativeWorkerRuntime {
	options := creative.RuntimeOptions{Enabled: cfg != nil && cfg.Creative.QueueEnabled}
	if public != nil && public.Results != nil {
		options.Outbox = public.Results.RunCreativeOutboxReconciler
		options.Transient = public.Results.RunCreativeTransientReconciler
	}
	return creative.NewCreativeWorkerRuntime(worker, options)
}

// creativeFixtureTarget 保留替身 Execute 的原输入、返回和取消传播。
type creativeFixtureTarget func(context.Context, creative.CreativeRun, creative.CreativeRunPayload) (*creative.CreativeExecuteResult, error)

func (f creativeFixtureTarget) Execute(ctx context.Context, run creative.CreativeRun, payload creative.CreativeRunPayload) (*creative.CreativeExecuteResult, error) {
	return f(ctx, run, payload)
}

func bindCreativeRepoFixture(public *creative.Public, repo creative.CreativeRunRepository) {
	public.Repo = repo
	public.Results.Repo = repo
}
func bindCreativeTransientFixture(public *creative.Public, store creative.CreativeTransientStore) {
	public.TransientStore = store
	public.Results.TransientStore = store
}
