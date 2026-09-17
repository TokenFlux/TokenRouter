package service

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/batchimage"
)

const BatchImageParsedStatusSucceeded = batchimage.BatchImageParsedStatusSucceeded
const BatchImageParsedStatusFailed = batchimage.BatchImageParsedStatusFailed

type BatchImageAccountResolver interface {
	ResolveBatchImageAccount(ctx context.Context, accountID int64) (*Account, error)
}

type BatchImageAccountLookup interface {
	GetByID(ctx context.Context, id int64) (*Account, error)
}

type BatchImageAccountRepositoryResolver struct {
	Repo BatchImageAccountLookup
}

func (r *BatchImageAccountRepositoryResolver) ResolveBatchImageAccount(ctx context.Context, accountID int64) (*Account, error) {
	if r == nil || r.Repo == nil {
		return nil, ErrAccountNotFound
	}
	return r.Repo.GetByID(ctx, accountID)
}

type BatchImageProviderProcessor struct {
	Repo             BatchImageRepository
	ProviderRegistry *BatchImageProviderRegistry
	AccountResolver  BatchImageAccountResolver
	Indexer          *BatchImageResultIndexer
	BillingRepo      UsageBillingRepository
	AuthCache        APIKeyAuthCacheInvalidator
	DefaultRequeue   time.Duration
}

func (p *BatchImageProviderProcessor) Process(ctx context.Context, batchID string) (BatchImageProcessResult, error) {
	return p.nativeProcessor().Process(ctx, batchID)
}

type BatchImageIndexResult = batchimage.BatchImageIndexResult

type BatchImageResultIndexer struct {
	Repo BatchImageRepository
}

func (i *BatchImageResultIndexer) Index(ctx context.Context, job *BatchImageJob, provider BatchImageProvider, account *Account) (*BatchImageIndexResult, error) {
	return i.nativeIndexer().Index(ctx, job, batchBoundProvider{provider: provider, account: account})
}

type ParsedBatchImageResult = batchimage.ParsedBatchImageResult

func ParseBatchImageResultLine(line []byte, lineNumber int) (*ParsedBatchImageResult, error) {
	return batchimage.ParseBatchImageResultLine(line, lineNumber)
}
