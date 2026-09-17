package service

import (
	"context"
	"io"

	"github.com/TokenFlux/TokenRouter/internal/batchimage"
)

type batchBoundProvider struct {
	provider BatchImageProvider
	account  *Account
}

func (p batchBoundProvider) Submit(ctx context.Context, job *BatchImageJob, input BatchImageInput) (*BatchProviderJob, error) {
	return p.provider.Submit(ctx, job, p.account, input)
}
func (p batchBoundProvider) Get(ctx context.Context, job *BatchImageJob) (*BatchProviderStatus, error) {
	return p.provider.Get(ctx, job, p.account)
}
func (p batchBoundProvider) Cancel(ctx context.Context, job *BatchImageJob) error {
	return p.provider.Cancel(ctx, job, p.account)
}
func (p batchBoundProvider) OpenResult(ctx context.Context, job *BatchImageJob) (io.ReadCloser, string, error) {
	return p.provider.OpenResult(ctx, job, p.account)
}
func (p batchBoundProvider) Cleanup(ctx context.Context, job *BatchImageJob, target CleanupTarget) error {
	return p.provider.Cleanup(ctx, job, p.account, target)
}

// 账号资格校验与读取次序沿用原处理入口，绑定后只允许本任务的供应商操作。
func resolveBatchBoundProvider(ctx context.Context, job *BatchImageJob, registry *BatchImageProviderRegistry, resolver BatchImageAccountResolver) (batchimage.BoundProvider, error) {
	provider, ok := registry.Get(job.Provider)
	if !ok || provider == nil {
		return nil, ErrBatchImageUnsupportedProvider
	}
	if job.AccountID == nil || *job.AccountID <= 0 {
		return nil, ErrBatchImageMissingAccountID
	}
	account, err := resolver.ResolveBatchImageAccount(ctx, *job.AccountID)
	if err != nil {
		return nil, err
	}
	if !provider.SupportsAccount(account) {
		return nil, ErrBatchImageProviderUnsupportedAccount
	}
	return batchBoundProvider{provider: provider, account: account}, nil
}
func (p *BatchImageProviderProcessor) nativeProcessor() *batchimage.ProviderProcessor {
	if p == nil {
		return nil
	}
	out := &batchimage.ProviderProcessor{Repo: p.Repo, Funding: batchFundingProjection(p.BillingRepo), DefaultRequeue: p.DefaultRequeue, Observe: creativeLegacyObserve}
	if p.ProviderRegistry != nil && p.AccountResolver != nil {
		out.ResolveProvider = func(ctx context.Context, job *BatchImageJob) (batchimage.BoundProvider, error) {
			return resolveBatchBoundProvider(ctx, job, p.ProviderRegistry, p.AccountResolver)
		}
	}
	if p.Indexer != nil {
		out.Indexer = p.Indexer.nativeIndexer()
	}
	if p.AuthCache != nil {
		out.InvalidateAuth = p.AuthCache.InvalidateAuthCacheByUserID
	}
	return out
}
func (i *BatchImageResultIndexer) nativeIndexer() *batchimage.ResultIndexer {
	if i == nil {
		return nil
	}
	return &batchimage.ResultIndexer{Repo: i.Repo, Observe: creativeLegacyObserve}
}
