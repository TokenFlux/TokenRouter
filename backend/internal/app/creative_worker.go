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

// creative_worker.go constructs creative workers with user concurrency
// admission and runtime pool settings.
// Creative generation requests capacity using the concurrency limit read from
// its user's record, and the worker releases an acquired slot when generation
// finishes. Pool sizing comes from composite settings at runtime startup, while
// queue timings and the enable flag come from process configuration.

// provideCreativeWorkerRuntime constructs creative execution, user-slot admission,
// and result reconciliation. Callers supply populated public services, cfg, and
// read options with storage and scheduler dependencies usable during execution.
// The returned runtime starts its worker and reconciliation loops through Start;
// acquired user-slot releases are transferred to the creative worker's lease.
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
			// An empty scheduler result reports unavailable capacity; the creative
			// worker treats acquired=false as a request to requeue generation.
			if slot == nil {
				return nil, false, nil
			}
			return slot.ReleaseFunc, slot.Acquired, nil
		},
	}
	// Queue lease, retry, polling, and recovery settings use seconds in cfg
	// and durations in the worker; normalization replaces nonpositive option values.
	opts := creative.NormalizeCreativeWorkerOptions(creative.CreativeWorkerOptions{
		JobLockTTL:          time.Duration(cfg.Creative.Queue.JobLockTTLSeconds) * time.Second,
		LockConflictDelay:   time.Duration(cfg.Creative.Queue.LockConflictDelaySeconds) * time.Second,
		DefaultRequeueDelay: time.Duration(cfg.Creative.Queue.DefaultRequeueDelaySeconds) * time.Second,
		ErrorRetryDelay:     time.Duration(cfg.Creative.Queue.ErrorRetryDelaySeconds) * time.Second,
		DelayedPollInterval: time.Duration(cfg.Creative.Queue.DelayedMoverIntervalSeconds) * time.Second,
		RecoveryInterval:    time.Duration(cfg.Creative.Queue.RecoveryIntervalSeconds) * time.Second,
		StaleActiveAfter:    time.Duration(cfg.Creative.Queue.StaleActiveAfterSeconds) * time.Second,
		DelayedMoveLimit:    cfg.Creative.Queue.DelayedMoveLimit,
		RecoverLimit:        cfg.Creative.Queue.RecoverLimit,
		MaxAttempts:         cfg.Creative.MaxExecuteAttempts,
	})
	worker := creative.NewCreativeRunWorker(public.Queue, public.Repo, public.TransientStore, executor, public.Results, opts, ports)
	return creative.NewCreativeWorkerRuntime(worker, creative.RuntimeOptions{
		Enabled:   cfg.Creative.QueueWorkerEnabled,
		Outbox:    public.Results.RunCreativeOutboxReconciler,
		Transient: public.Results.RunCreativeTransientReconciler,
		WorkerCount: func(ctx context.Context) int {
			values, err := store.GetAll(ctx)
			// Zero makes desiredWorkerCountValue keep its stored or default count,
			// so a failed settings read does not abort runtime startup.
			if err != nil {
				return 0
			}
			return composite.Parse(values, *read).CreativeWorkerCount
		},
	})
}
