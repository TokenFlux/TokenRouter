package app

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/idempotency"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
)

// idempotency_runtime.go constructs the idempotency coordinator and cleanup
// worker from configuration and shared storage. Idempotency settings
// distinguish absent process configuration from an explicit false observe-mode
// flag. Construction starts from package defaults, replaces positive retention
// and lock durations and response-prefix limits, and takes ObserveOnly from
// every present configuration. A zero-valued config therefore retains numeric
// defaults while disabling observe-only admission for commands that require a
// key.
//
// The coordinator and expiry worker receive application logging callbacks and
// the repository supplied by Wire. Cleanup intervals and batch sizes are passed
// to the worker constructor, which selects defaults for nonpositive values.
// Construction leaves that worker inactive; provideCoreRuntime registers its
// later start and stop callbacks with the application lifecycle manager.

// idempotencyOptions derives coordinator settings from cfg and package
// defaults. Nil cfg retains ObserveOnly true, while every present cfg copies
// its ObserveOnly value, including false.
//
// Positive duration and response-prefix settings replace their defaults. For
// example, a zero-valued configuration retains numeric defaults and sets
// ObserveOnly false; commands with RequireKey then reject a missing key. The
// duration fields are seconds and the response prefix limit is bytes.
// ProcessingTimeout sets a persisted lock window; the command's caller context
// controls callback cancellation.
func idempotencyOptions(cfg *config.Config) idempotency.IdempotencyConfig {
	opts := idempotency.DefaultIdempotencyConfig()
	if cfg == nil {
		return opts
	}
	// Convert the four deployment duration overrides from seconds into
	// coordinator durations; nonpositive inputs retain their package defaults.
	if cfg.Idempotency.DefaultTTLSeconds > 0 {
		opts.DefaultTTL = time.Duration(cfg.Idempotency.DefaultTTLSeconds) * time.Second
	}
	if cfg.Idempotency.SystemOperationTTLSeconds > 0 {
		opts.SystemOperationTTL = time.Duration(cfg.Idempotency.SystemOperationTTLSeconds) * time.Second
	}
	if cfg.Idempotency.ProcessingTimeoutSeconds > 0 {
		opts.ProcessingTimeout = time.Duration(cfg.Idempotency.ProcessingTimeoutSeconds) * time.Second
	}
	if cfg.Idempotency.FailedRetryBackoffSeconds > 0 {
		opts.FailedRetryBackoff = time.Duration(cfg.Idempotency.FailedRetryBackoffSeconds) * time.Second
	}
	if cfg.Idempotency.MaxStoredResponseBytes > 0 {
		opts.MaxStoredResponseLen = cfg.Idempotency.MaxStoredResponseBytes
	}
	opts.ObserveOnly = cfg.Idempotency.ObserveOnly
	return opts
}

// provideIdempotencyCoordinator constructs the HTTP command coordinator using
// settings derived from cfg and an application log observer. Keyed execution
// relies on the supplied repository; nil configuration selects package defaults.
func provideIdempotencyCoordinator(repo idempotency.IdempotencyRepository, cfg *config.Config) *idempotency.IdempotencyCoordinator {
	coordinator := idempotency.NewIdempotencyCoordinator(repo, idempotencyOptions(cfg), idempotencyObserver())
	return coordinator
}

// provideIdempotencyCleanupService constructs an inactive expiry worker with
// the application observer. A missing config passes zero scheduling options,
// and the worker constructor selects defaults for nonpositive interval and
// batch values. The application core lifecycle registration supplies its later
// start and stop callbacks.
func provideIdempotencyCleanupService(repo idempotency.IdempotencyRepository, cfg *config.Config) *idempotency.IdempotencyCleanupService {
	opts := idempotency.CleanupOptions{Observer: idempotencyObserver()}
	if cfg != nil {
		// The worker schedules with durations; deployment cleanup intervals are seconds.
		opts.Interval = time.Duration(cfg.Idempotency.CleanupIntervalSeconds) * time.Second
		opts.Batch = cfg.Idempotency.CleanupBatchSize
	}
	return idempotency.NewIdempotencyCleanupService(repo, opts)
}

// idempotencyObserver writes idempotency events to the application log.
func idempotencyObserver() idempotency.Observer {
	return idempotency.ObserverFunc(func(component, message string) { logging.LegacyPrintf(component, "%s", message) })
}
