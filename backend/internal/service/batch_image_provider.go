package service

import (
	"context"
	"io"

	"github.com/TokenFlux/TokenRouter/internal/batchimage"

	"github.com/TokenFlux/TokenRouter/internal/config"
)

type BatchImageProvider interface {
	Name() string
	SupportsAccount(account *Account) bool
	Submit(ctx context.Context, job *BatchImageJob, account *Account, input BatchImageInput) (*BatchProviderJob, error)
	Get(ctx context.Context, job *BatchImageJob, account *Account) (*BatchProviderStatus, error)
	Cancel(ctx context.Context, job *BatchImageJob, account *Account) error
	OpenResult(ctx context.Context, job *BatchImageJob, account *Account) (io.ReadCloser, string, error)
	Cleanup(ctx context.Context, job *BatchImageJob, account *Account, target CleanupTarget) error
}

type BatchImageProviderRegistry struct {
	inner *batchimage.Registry[BatchImageProvider]
}

func NewBatchImageProviderRegistry(providers ...BatchImageProvider) *BatchImageProviderRegistry {
	return &BatchImageProviderRegistry{inner: batchimage.NewRegistry(providers...)}
}

func NewDefaultBatchImageProviderRegistry() *BatchImageProviderRegistry {
	return NewBatchImageProviderRegistry(
		NewGeminiAPIBatchImageProvider(nil),
		NewVertexBatchImageProvider(VertexBatchImageProviderOptions{}, nil, nil, nil),
	)
}

func NewBatchImageProviderRegistryFromConfig(cfg *config.Config) *BatchImageProviderRegistry {
	return NewBatchImageProviderRegistry(
		NewGeminiAPIBatchImageProvider(nil),
		NewVertexBatchImageProviderFromConfig(cfg, nil, nil, nil),
	)
}

func (r *BatchImageProviderRegistry) Get(provider string) (BatchImageProvider, bool) {
	if r == nil {
		return nil, false
	}
	return r.inner.Get(provider)
}

func (r *BatchImageProviderRegistry) MustGet(provider string) (BatchImageProvider, error) {
	if r == nil {
		return nil, ErrBatchImageInvalidProvider
	}
	return r.inner.MustGet(provider)
}

type BatchImageInput = batchimage.BatchImageInput

type BatchImageInputItem = batchimage.BatchImageInputItem

type BatchImageReference = batchimage.BatchImageReference

type BatchProviderJob = batchimage.BatchProviderJob

type BatchProviderInternalState = batchimage.BatchProviderInternalState

const BatchProviderStateQueued = batchimage.BatchProviderStateQueued
const BatchProviderStateRunning = batchimage.BatchProviderStateRunning
const BatchProviderStateSucceeded = batchimage.BatchProviderStateSucceeded
const BatchProviderStateFailed = batchimage.BatchProviderStateFailed
const BatchProviderStateCancelled = batchimage.BatchProviderStateCancelled
const BatchProviderStateExpired = batchimage.BatchProviderStateExpired

type BatchProviderStatus = batchimage.BatchProviderStatus

type CleanupTarget = batchimage.CleanupTarget

const CleanupTargetInput = batchimage.CleanupTargetInput
const CleanupTargetOutput = batchimage.CleanupTargetOutput
const CleanupTargetAll = batchimage.CleanupTargetAll

var ErrBatchImageProviderUnsupportedAccount = batchimage.ErrBatchImageProviderUnsupportedAccount
var ErrBatchImageProviderMissingAPIKey = batchimage.ErrBatchImageProviderMissingAPIKey
var ErrBatchImageProviderMissingServiceAccount = batchimage.ErrBatchImageProviderMissingServiceAccount
var ErrBatchImageProviderMissingJobName = batchimage.ErrBatchImageProviderMissingJobName
var ErrBatchImageProviderMissingResultRef = batchimage.ErrBatchImageProviderMissingResultRef
var ErrBatchImageProviderInlineResultUnsupported = batchimage.ErrBatchImageProviderInlineResultUnsupported
var ErrBatchImageProviderInvalidInput = batchimage.ErrBatchImageProviderInvalidInput
var ErrBatchImageProviderUnsafeCleanupPath = batchimage.ErrBatchImageProviderUnsafeCleanupPath
var ErrUnsupportedCleanupTarget = batchimage.ErrUnsupportedCleanupTarget

// 显式传入时复用 app 的唯一注册表；旧独立构造保持默认行为。
func batchRegistryFromOptions(cfg *config.Config, registries []*BatchImageProviderRegistry) *BatchImageProviderRegistry {
	if len(registries) > 0 {
		return registries[0]
	}
	return NewBatchImageProviderRegistryFromConfig(cfg)
}
