package service

import (
	"context"
	"sync"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/batchimage"

	"github.com/TokenFlux/TokenRouter/internal/config"
)

type BatchImageCleanupService struct {
	Core             *batchimage.Cleanup
	Repo             BatchImageRepository
	ProviderRegistry *BatchImageProviderRegistry
	AccountResolver  BatchImageAccountResolver
	Config           *config.Config

	runtime *batchimage.Runtime
	mu      sync.Mutex
}

func NewBatchImageCleanupService(repo BatchImageRepository, accountRepo AccountRepository, cfg *config.Config, registries ...*BatchImageProviderRegistry) *BatchImageCleanupService {
	return &BatchImageCleanupService{
		Repo:             repo,
		ProviderRegistry: batchRegistryFromOptions(cfg, registries),
		AccountResolver:  &BatchImageAccountRepositoryResolver{Repo: accountRepo},
		Config:           cfg,
	}
}

func (s *BatchImageCleanupService) DeleteOutputsForOwner(ctx context.Context, owner BatchImageOwner, batchID string) (*BatchImagePublicBatch, error) {
	return s.nativeCleanup().DeleteOutputsForOwner(ctx, owner, batchID)
}

func (s *BatchImageCleanupService) CleanupInput(ctx context.Context, batchID string) error {
	return s.nativeCleanup().CleanupInput(ctx, batchID)
}

func (s *BatchImageCleanupService) CleanupOutput(ctx context.Context, batchID string, reason string) error {
	return s.nativeCleanup().CleanupOutput(ctx, batchID, reason)
}

func (s *BatchImageCleanupService) RunOnce(ctx context.Context, now time.Time) (BatchImageCleanupRunResult, error) {
	return s.nativeCleanup().RunOnce(ctx, now)
}

func (s *BatchImageCleanupService) runtimeOwner() *batchimage.Runtime {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runtime != nil {
		return s.runtime
	}
	enabled := s.Repo != nil && s.Config != nil && s.Config.BatchImage.Enabled && s.cleanupInterval() > 0
	s.runtime = batchimage.NewRuntime("batch image cleanup", enabled, func(ctx context.Context) {
		ticker := time.NewTicker(s.cleanupInterval())
		defer ticker.Stop()
		for {
			if ctx.Err() != nil {
				return
			}
			_, _ = s.RunOnce(ctx, time.Now())
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	})
	return s.runtime
}
func (s *BatchImageCleanupService) Start() {
	if s != nil {
		s.runtimeOwner().Start()
	}
}
func (s *BatchImageCleanupService) Stop() {
	if s != nil {
		s.runtimeOwner().Stop()
	}
}

// StopContext 把清理任务等待纳入应用剩余退出预算。
func (s *BatchImageCleanupService) StopContext(ctx context.Context) error {
	if s == nil {
		return nil
	}
	return s.runtimeOwner().StopContext(ctx)
}

func (s *BatchImageCleanupService) cleanupInterval() time.Duration {
	return s.nativeCleanup().CleanupInterval()
}

type BatchImageCleanupRunResult = batchimage.BatchImageCleanupRunResult

func (s *BatchImageCleanupService) nativeCleanup() *batchimage.Cleanup {
	if s != nil && s.Core != nil {
		return s.Core
	}
	if s == nil {
		return nil
	}
	out := &batchimage.Cleanup{Repo: s.Repo, Observe: creativeLegacyObserve}
	if s.Config != nil {
		out.Options = batchimage.CleanupOptions{InputRetention: time.Duration(s.Config.BatchImage.InputRetentionAfterTerminalHours) * time.Hour, Interval: time.Duration(s.Config.BatchImage.CleanupIntervalMinutes) * time.Minute, BatchSize: s.Config.BatchImage.CleanupBatchSize}
	}
	if s.ProviderRegistry != nil && s.AccountResolver != nil {
		out.ResolveProvider = func(ctx context.Context, job *BatchImageJob) (batchimage.BoundProvider, error) {
			provider, ok := s.ProviderRegistry.Get(job.Provider)
			if !ok || provider == nil {
				return nil, ErrBatchImageUnsupportedProvider
			}
			if job.AccountID == nil || *job.AccountID <= 0 {
				return nil, ErrBatchImageMissingAccountID
			}
			account, err := s.AccountResolver.ResolveBatchImageAccount(ctx, *job.AccountID)
			if err != nil {
				return nil, err
			}
			return batchBoundProvider{provider: provider, account: account}, nil
		}
	}
	return out
}

// BindCleanupCore 仅在 app 构造阶段调用，之后所有兼容入口委托同一实例。
func (s *BatchImageCleanupService) BindCleanupCore() *batchimage.Cleanup {
	if s.Core == nil {
		s.Core = s.nativeCleanup()
	}
	return s.Core
}
