package service

import (
	"context"
	"io"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/batchimage"

	"github.com/TokenFlux/TokenRouter/internal/config"
)

type BatchImageDownloadLimiter = batchimage.BatchImageDownloadLimiter

type BatchImageDownloadPermit = batchimage.BatchImageDownloadPermit

type BatchImageContentStream = batchimage.BatchImageContentStream

type BatchImageZipOptions = batchimage.BatchImageZipOptions

type BatchImageZipResult = batchimage.BatchImageZipResult

type BatchImageLineImages = batchimage.BatchImageLineImages

type BatchImageInlineImage = batchimage.BatchImageInlineImage

type BatchImageDownloadService struct {
	Core             *batchimage.Download
	Repo             BatchImageRepository
	ProviderRegistry *BatchImageProviderRegistry
	AccountResolver  BatchImageAccountResolver
	Limiter          BatchImageDownloadLimiter
	Config           *config.Config
}

func NewBatchImageDownloadService(repo BatchImageRepository, accountRepo AccountRepository, limiter BatchImageDownloadLimiter, cfg *config.Config, registries ...*BatchImageProviderRegistry) *BatchImageDownloadService {
	return &BatchImageDownloadService{
		Repo:             repo,
		ProviderRegistry: batchRegistryFromOptions(cfg, registries),
		AccountResolver:  &BatchImageAccountRepositoryResolver{Repo: accountRepo},
		Limiter:          limiter,
		Config:           cfg,
	}
}

func (s *BatchImageDownloadService) OpenItemContent(ctx context.Context, owner BatchImageOwner, batchID string, customID string, imageIndex int) (*BatchImageContentStream, error) {
	return s.nativeDownload().OpenItemContent(ctx, owner, batchID, customID, imageIndex)
}

func (s *BatchImageDownloadService) StreamZip(ctx context.Context, owner BatchImageOwner, batchID string, opts BatchImageZipOptions, w io.Writer) (*BatchImageZipResult, error) {
	return s.nativeDownload().StreamZip(ctx, owner, batchID, opts, w)
}

func (s *BatchImageDownloadService) providerAndAccount(ctx context.Context, job *BatchImageJob) (BatchImageProvider, *Account, error) {
	if s == nil || s.ProviderRegistry == nil || s.AccountResolver == nil || job == nil {
		return nil, nil, ErrBatchImageDownloadFailed
	}
	provider, ok := s.ProviderRegistry.Get(job.Provider)
	if !ok || provider == nil {
		return nil, nil, ErrBatchImageUnsupportedProvider
	}
	if job.AccountID == nil || *job.AccountID <= 0 {
		return nil, nil, ErrBatchImageMissingAccountID
	}
	account, err := s.AccountResolver.ResolveBatchImageAccount(ctx, *job.AccountID)
	if err != nil {
		return nil, nil, ErrBatchImageDownloadFailed
	}
	if !provider.SupportsAccount(account) {
		return nil, nil, ErrBatchImageProviderUnsupportedAccount
	}
	return provider, account, nil
}

func ExtractBatchImagePartsFromResultLine(line []byte) (*BatchImageLineImages, error) {
	return batchimage.ExtractBatchImagePartsFromResultLine(line)
}

func BatchImageSafeDownloadFilename(customID, extension string) string {
	return batchimage.BatchImageSafeDownloadFilename(customID, extension)
}

func BatchImageContentDispositionAttachment(filename string) string {
	return batchimage.BatchImageContentDispositionAttachment(filename)
}

func (s *BatchImageDownloadService) nativeDownload() *batchimage.Download {
	if s != nil && s.Core != nil {
		return s.Core
	}
	if s == nil {
		return nil
	}
	out := &batchimage.Download{Repo: s.Repo, Limiter: s.Limiter}
	if s.Config != nil {
		out.Options = batchimage.DownloadOptions{MaxItems: s.Config.BatchImage.MaxDownloadItemsZip, MaxBytes: s.Config.BatchImage.MaxDownloadBytesPerRequest, Duration: time.Duration(s.Config.BatchImage.MaxDownloadDurationSeconds) * time.Second}
	}
	out.ResolveProvider = func(ctx context.Context, job *BatchImageJob) (batchimage.BoundProvider, error) {
		provider, account, err := s.providerAndAccount(ctx, job)
		if err != nil {
			return nil, err
		}
		return batchBoundProvider{provider: provider, account: account}, nil
	}
	return out
}

// BindDownloadCore 仅在 app 构造阶段调用，之后所有兼容入口委托同一实例。
func (s *BatchImageDownloadService) BindDownloadCore() *batchimage.Download {
	if s.Core == nil {
		s.Core = s.nativeDownload()
	}
	return s.Core
}
