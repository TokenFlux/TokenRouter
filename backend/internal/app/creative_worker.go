package app

import (
	"context"
	"errors"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/settings/composite"
)

// provideCreativeWorkerRuntime 将原生任务结果和用户准入端口固定到同一 worker，构造不启动。
func provideCreativeWorkerRuntime(public *creative.Public, executor *creative.Executor, users *identitypostgres.UserStore, concurrency *scheduler.ConcurrencyService, store *settings.Store, read *composite.ReadOptions, cfg *config.Config) *creative.CreativeWorkerRuntime {
	ports := creative.WorkerPorts{
		Observe:     creativeObserve,
		UserMissing: func(err error) bool { return errors.Is(err, identity.ErrUserNotFound) },
		AcquireUser: func(ctx context.Context, id int64) (func(), bool, error) {
			user, err := users.GetByID(ctx, id)
			if err != nil {
				return nil, false, err
			}
			if user == nil {
				return nil, false, errors.New("creative run user is unavailable")
			}
			slot, err := concurrency.AcquireUserSlot(ctx, id, user.Concurrency)
			if err != nil {
				return nil, false, err
			}
			if slot == nil {
				return nil, false, nil
			}
			return slot.ReleaseFunc, slot.Acquired, nil
		}}
	opts := creative.NormalizeCreativeWorkerOptions(creative.CreativeWorkerOptions{
		JobLockTTL:          time.Duration(cfg.Creative.JobLockTTLSeconds) * time.Second,
		LockConflictDelay:   time.Duration(cfg.Creative.LockConflictDelaySeconds) * time.Second,
		DefaultRequeueDelay: time.Duration(cfg.Creative.DefaultRequeueDelaySeconds) * time.Second,
		ErrorRetryDelay:     time.Duration(cfg.Creative.ErrorRetryDelaySeconds) * time.Second,
		DelayedPollInterval: time.Duration(cfg.Creative.DelayedMoverIntervalSeconds) * time.Second,
		RecoveryInterval:    time.Duration(cfg.Creative.RecoveryIntervalSeconds) * time.Second,
		StaleActiveAfter:    time.Duration(cfg.Creative.StaleActiveAfterSeconds) * time.Second,
		DelayedMoveLimit:    cfg.Creative.DelayedMoveLimit,
		RecoverLimit:        cfg.Creative.RecoverLimit,
		MaxAttempts:         cfg.Creative.MaxExecuteAttempts,
	})
	worker := creative.NewCreativeRunWorker(public.Queue, public.Repo, public.TransientStore, executor, public.Results, opts, ports)
	return creative.NewCreativeWorkerRuntime(worker, creative.RuntimeOptions{
		Enabled:   cfg.Creative.QueueEnabled,
		Outbox:    public.Results.RunCreativeOutboxReconciler,
		Transient: public.Results.RunCreativeTransientReconciler,
		WorkerCount: func(ctx context.Context) int {
			values, err := store.GetAll(ctx)
			if err != nil {
				return 0
			}
			return composite.Parse(values, *read).CreativeWorkerCount
		}})
}
