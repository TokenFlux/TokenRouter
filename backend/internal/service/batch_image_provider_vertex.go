// 兼容供应商入口只投影账号，平台任务操作由所属 Adapter 唯一实现。
package service

import (
	"context"
	"io"
	"net/http"

	native "github.com/TokenFlux/TokenRouter/internal/batchimage/provider"
	"github.com/TokenFlux/TokenRouter/internal/config"
)

type VertexBatchImageProvider struct {
	inner *native.VertexBatchImageProvider
}

type VertexBatchImageProviderOptions = native.VertexBatchImageProviderOptions

func NewVertexBatchImageProviderOptionsFromConfig(cfg *config.Config) VertexBatchImageProviderOptions {
	if cfg == nil {
		return VertexBatchImageProviderOptions{}
	}
	return VertexBatchImageProviderOptions{
		Enabled:                cfg.BatchImage.VertexEnabled,
		ProjectID:              cfg.BatchImage.VertexProjectID,
		Location:               cfg.BatchImage.VertexLocation,
		ManagedGCSBucket:       cfg.BatchImage.VertexManagedGCSBucket,
		ManagedGCSPrefix:       cfg.BatchImage.VertexManagedGCSPrefix,
		Environment:            cfg.Log.Environment,
		InputRetentionHours:    cfg.BatchImage.VertexInputRetentionHours,
		OutputRetentionHours:   cfg.BatchImage.VertexOutputRetentionHours,
		BatchPredictionBaseURL: cfg.BatchImage.VertexBatchPredictionBaseURL,
		GCSBaseURL:             cfg.BatchImage.VertexGCSBaseURL,
	}
}
func NewVertexBatchImageProvider(opts VertexBatchImageProviderOptions, client VertexBatchClient, objectStore VertexBatchObjectStore, tokenCache GeminiTokenCache) *VertexBatchImageProvider {
	return &VertexBatchImageProvider{inner: native.NewVertexBatchImageProvider(opts, client, objectStore, tokenCache)}
}
func NewVertexBatchImageProviderFromConfig(cfg *config.Config, client VertexBatchClient, objectStore VertexBatchObjectStore, tokenCache GeminiTokenCache) *VertexBatchImageProvider {
	return NewVertexBatchImageProvider(NewVertexBatchImageProviderOptionsFromConfig(cfg), client, objectStore, tokenCache)
}

func (p *VertexBatchImageProvider) Name() string { return p.inner.Name() }
func (p *VertexBatchImageProvider) SupportsAccount(account *Account) bool {
	return p.inner.SupportsAccount(AccountRecordView(account))
}
func (p *VertexBatchImageProvider) Submit(ctx context.Context, job *BatchImageJob, account *Account, input BatchImageInput) (*BatchProviderJob, error) {
	return p.inner.Submit(ctx, job, AccountRecordView(account), input)
}
func (p *VertexBatchImageProvider) Get(ctx context.Context, job *BatchImageJob, account *Account) (*BatchProviderStatus, error) {
	return p.inner.Get(ctx, job, AccountRecordView(account))
}
func (p *VertexBatchImageProvider) Cancel(ctx context.Context, job *BatchImageJob, account *Account) error {
	return p.inner.Cancel(ctx, job, AccountRecordView(account))
}
func (p *VertexBatchImageProvider) OpenResult(ctx context.Context, job *BatchImageJob, account *Account) (io.ReadCloser, string, error) {
	return p.inner.OpenResult(ctx, job, AccountRecordView(account))
}
func (p *VertexBatchImageProvider) Cleanup(ctx context.Context, job *BatchImageJob, account *Account, target CleanupTarget) error {
	return p.inner.Cleanup(ctx, job, AccountRecordView(account), target)
}

type VertexBatchClient = native.VertexBatchClient
type VertexBatchObjectStore = native.VertexBatchObjectStore
type VertexCreateBatchPredictionJobRequest = native.VertexCreateBatchPredictionJobRequest
type VertexBatchInputConfig = native.VertexBatchInputConfig
type VertexBatchGCSSource = native.VertexBatchGCSSource
type VertexBatchOutputConfig = native.VertexBatchOutputConfig
type VertexBatchGCSDestination = native.VertexBatchGCSDestination
type VertexBatchInstanceConfig = native.VertexBatchInstanceConfig
type VertexBatchPredictionJob = native.VertexBatchPredictionJob
type VertexBatchJobError = native.VertexBatchJobError

func NormalizeVertexBatchModelPath(model string) string {
	return native.NormalizeVertexBatchModelPath(model)
}
func BuildVertexBatchPredictionJobsEndpoint(baseURL, projectID, location string) (string, error) {
	return native.BuildVertexBatchPredictionJobsEndpoint(baseURL, projectID, location)
}

type VertexBatchHTTPClient = native.VertexBatchHTTPClient
type VertexGCSObjectStore = native.VertexGCSObjectStore
type VertexAPIError = native.VertexAPIError

func NewVertexBatchHTTPClient(baseURL string, client *http.Client) *VertexBatchHTTPClient {
	return native.NewVertexBatchHTTPClient(baseURL, client)
}
func NewVertexGCSObjectStore(baseURL string, client *http.Client) *VertexGCSObjectStore {
	return native.NewVertexGCSObjectStore(baseURL, client)
}
func BuildVertexBatchJSONL(input BatchImageInput) ([]byte, error) {
	return native.BuildVertexBatchJSONL(input)
}
