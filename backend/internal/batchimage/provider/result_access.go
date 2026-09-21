package provider

import (
	"context"
	"io"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/batchimage"
)

// ResultAccounts 只读取任务已经绑定的执行账号，不参与重新选号。
type ResultAccounts interface {
	GetByID(context.Context, int64) (*account.Record, error)
}

// ResultAccess 保留下载与清理各自的资格检查及错误顺序。
type ResultAccess struct {
	Registry *batchimage.Registry[BatchImageProvider]
	Accounts ResultAccounts
}

func (a ResultAccess) Download(ctx context.Context, job *batchimage.BatchImageJob) (batchimage.BoundProvider, error) {
	if a.Registry == nil || a.Accounts == nil || job == nil {
		return nil, batchimage.ErrBatchImageDownloadFailed
	}
	selected, ok := a.Registry.Get(job.Provider)
	if !ok || selected == nil {
		return nil, batchimage.ErrBatchImageUnsupportedProvider
	}
	if job.AccountID == nil || *job.AccountID <= 0 {
		return nil, batchimage.ErrBatchImageMissingAccountID
	}
	value, err := a.Accounts.GetByID(ctx, *job.AccountID)
	if err != nil {
		return nil, batchimage.ErrBatchImageDownloadFailed
	}
	if !selected.SupportsAccount(account.CloneRecord(value)) {
		return nil, batchimage.ErrBatchImageProviderUnsupportedAccount
	}
	return BindAccount(selected, value), nil
}

func (a ResultAccess) Cleanup(ctx context.Context, job *batchimage.BatchImageJob) (batchimage.BoundProvider, error) {
	selected, ok := a.Registry.Get(job.Provider)
	if !ok || selected == nil {
		return nil, batchimage.ErrBatchImageUnsupportedProvider
	}
	if job.AccountID == nil || *job.AccountID <= 0 {
		return nil, batchimage.ErrBatchImageMissingAccountID
	}
	value, err := a.Accounts.GetByID(ctx, *job.AccountID)
	if err != nil {
		return nil, err
	}
	return BindAccount(selected, value), nil
}

// boundAccount 只交付当前任务的供应商操作，凭据不进入公开结果。
type boundAccount struct {
	provider BatchImageProvider
	account  *account.Record
}

func (b boundAccount) OpenResult(ctx context.Context, job *batchimage.BatchImageJob) (io.ReadCloser, string, error) {
	return b.provider.OpenResult(ctx, job, account.CloneRecord(b.account))
}
func (b boundAccount) Cleanup(ctx context.Context, job *batchimage.BatchImageJob, target batchimage.CleanupTarget) error {
	return b.provider.Cleanup(ctx, job, account.CloneRecord(b.account), target)
}

func (b boundAccount) Submit(ctx context.Context, job *batchimage.BatchImageJob, input batchimage.BatchImageInput) (*batchimage.BatchProviderJob, error) {
	return b.provider.Submit(ctx, job, account.CloneRecord(b.account), input)
}
func (b boundAccount) Get(ctx context.Context, job *batchimage.BatchImageJob) (*batchimage.BatchProviderStatus, error) {
	return b.provider.Get(ctx, job, account.CloneRecord(b.account))
}
func (b boundAccount) Cancel(ctx context.Context, job *batchimage.BatchImageJob) error {
	return b.provider.Cancel(ctx, job, account.CloneRecord(b.account))
}

// Process 沿用执行阶段的账号资格检查，读取失败保留原错误。
func (a ResultAccess) Process(ctx context.Context, job *batchimage.BatchImageJob) (batchimage.BoundProvider, error) {
	selected, ok := a.Registry.Get(job.Provider)
	if !ok || selected == nil {
		return nil, batchimage.ErrBatchImageUnsupportedProvider
	}
	if job.AccountID == nil || *job.AccountID <= 0 {
		return nil, batchimage.ErrBatchImageMissingAccountID
	}
	value, err := a.Accounts.GetByID(ctx, *job.AccountID)
	if err != nil {
		return nil, err
	}
	if !selected.SupportsAccount(account.CloneRecord(value)) {
		return nil, batchimage.ErrBatchImageProviderUnsupportedAccount
	}
	return BindAccount(selected, value), nil
}

// BindAccount 固化本次任务账号读取结果，每次供应商调用再取得独立副本。
func BindAccount(selected BatchImageProvider, value *account.Record) batchimage.ExecutionProvider {
	return boundAccount{selected, value}
}

func (b boundAccount) Name() string { return b.provider.Name() }
