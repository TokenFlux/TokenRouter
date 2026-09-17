// 兼容供应商入口只投影账号，平台任务操作由所属 Adapter 唯一实现。
package service

import (
	"context"
	"io"
	"net/http"

	native "github.com/TokenFlux/TokenRouter/internal/batchimage/provider"
)

type GeminiAPIBatchImageProvider struct {
	inner *native.GeminiAPIBatchImageProvider
}

type GeminiBatchClient = native.GeminiBatchClient
type GeminiUploadedFile = native.GeminiUploadedFile
type GeminiBatchJob = native.GeminiBatchJob
type GeminiBatchDest = native.GeminiBatchDest
type GeminiBatchResponse = native.GeminiBatchResponse
type GeminiBatchError = native.GeminiBatchError

func NewGeminiAPIBatchImageProvider(client GeminiBatchClient) *GeminiAPIBatchImageProvider {
	return &GeminiAPIBatchImageProvider{inner: native.NewGeminiAPIBatchImageProvider(client)}
}
func (p *GeminiAPIBatchImageProvider) Name() string { return p.inner.Name() }
func (p *GeminiAPIBatchImageProvider) SupportsAccount(account *Account) bool {
	return p.inner.SupportsAccount(AccountRecordView(account))
}
func (p *GeminiAPIBatchImageProvider) Submit(ctx context.Context, job *BatchImageJob, account *Account, input BatchImageInput) (*BatchProviderJob, error) {
	return p.inner.Submit(ctx, job, AccountRecordView(account), input)
}
func (p *GeminiAPIBatchImageProvider) Get(ctx context.Context, job *BatchImageJob, account *Account) (*BatchProviderStatus, error) {
	return p.inner.Get(ctx, job, AccountRecordView(account))
}
func (p *GeminiAPIBatchImageProvider) Cancel(ctx context.Context, job *BatchImageJob, account *Account) error {
	return p.inner.Cancel(ctx, job, AccountRecordView(account))
}
func (p *GeminiAPIBatchImageProvider) OpenResult(ctx context.Context, job *BatchImageJob, account *Account) (io.ReadCloser, string, error) {
	return p.inner.OpenResult(ctx, job, AccountRecordView(account))
}
func (p *GeminiAPIBatchImageProvider) Cleanup(ctx context.Context, job *BatchImageJob, account *Account, target CleanupTarget) error {
	return p.inner.Cleanup(ctx, job, AccountRecordView(account), target)
}

func BuildGeminiBatchJSONL(input BatchImageInput) ([]byte, error) {
	return native.BuildGeminiBatchJSONL(input)
}

type GeminiBatchHTTPClient = native.GeminiBatchHTTPClient

func NewGeminiBatchHTTPClient(baseURL string, client *http.Client) *GeminiBatchHTTPClient {
	return native.NewGeminiBatchHTTPClient(baseURL, client)
}

type GeminiAPIError = native.GeminiAPIError
