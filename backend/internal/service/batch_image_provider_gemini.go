package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/domain"

	gemininative "github.com/TokenFlux/TokenRouter/internal/upstream/gemini"
)

const defaultGeminiBatchRequeueAfter = 30 * time.Second

type GeminiBatchClient interface {
	UploadJSONL(ctx context.Context, apiKey string, displayName string, r io.Reader) (*GeminiUploadedFile, error)
	CreateBatch(ctx context.Context, apiKey string, model string, fileName string, displayName string) (*GeminiBatchJob, error)
	GetBatch(ctx context.Context, apiKey string, batchName string) (*GeminiBatchJob, error)
	CancelBatch(ctx context.Context, apiKey string, batchName string) error
	DownloadFile(ctx context.Context, apiKey string, fileName string) (io.ReadCloser, string, error)
	DeleteFile(ctx context.Context, apiKey string, fileName string) error
}

type GeminiUploadedFile = gemininative.GeminiUploadedFile

type GeminiBatchJob = gemininative.GeminiBatchJob

type GeminiBatchDest = gemininative.GeminiBatchDest

type GeminiBatchResponse = gemininative.GeminiBatchResponse

type GeminiBatchError = gemininative.GeminiBatchError

type GeminiAPIBatchImageProvider struct {
	client GeminiBatchClient
}

func NewGeminiAPIBatchImageProvider(client GeminiBatchClient) *GeminiAPIBatchImageProvider {
	if client == nil {
		client = NewGeminiBatchHTTPClient("", nil)
	}
	return &GeminiAPIBatchImageProvider{client: client}
}

func (p *GeminiAPIBatchImageProvider) Name() string {
	return BatchImageProviderGeminiAPI
}

func (p *GeminiAPIBatchImageProvider) SupportsAccount(account *Account) bool {
	return account != nil &&
		account.Platform == PlatformGemini &&
		account.Type == AccountTypeAPIKey &&
		batchImageProviderAPIKey(account) != ""
}

func (p *GeminiAPIBatchImageProvider) Submit(ctx context.Context, job *BatchImageJob, account *Account, input BatchImageInput) (*BatchProviderJob, error) {
	if _, enabled := ResolveProtocolRoute(account, nil, domain.ProtocolImageBatches); !enabled {
		return nil, ErrBatchImageProviderUnsupportedAccount
	}
	if account == nil || account.Platform != PlatformGemini || account.Type != AccountTypeAPIKey {
		return nil, ErrBatchImageProviderUnsupportedAccount
	}
	apiKey := batchImageProviderAPIKey(account)
	if apiKey == "" {
		return nil, ErrBatchImageProviderMissingAPIKey
	}
	if input.BatchID == "" && job != nil {
		input.BatchID = job.BatchID
	}
	if input.Model == "" && job != nil {
		input.Model = job.Model
	}

	jsonl, err := BuildGeminiBatchJSONL(input)
	if err != nil {
		return nil, err
	}

	displayName := strings.TrimSpace(input.DisplayName)
	if displayName == "" {
		displayName = strings.TrimSpace(input.BatchID)
	}

	uploaded, err := p.client.UploadJSONL(ctx, apiKey, displayName, bytes.NewReader(jsonl))
	if err != nil {
		return nil, mapGeminiClientError(err)
	}
	if uploaded == nil || strings.TrimSpace(uploaded.Name) == "" {
		return nil, geminiProviderError("GEMINI_INVALID_RESPONSE", "Gemini upload response is missing file name", nil)
	}

	batch, err := p.client.CreateBatch(ctx, apiKey, input.Model, uploaded.Name, displayName)
	if err != nil {
		return nil, mapGeminiClientError(err)
	}
	if batch == nil || strings.TrimSpace(batch.Name) == "" {
		return nil, geminiProviderError("GEMINI_INVALID_RESPONSE", "Gemini batch response is missing job name", nil)
	}

	return &BatchProviderJob{
		ProviderJobName:  batch.Name,
		ProviderInputRef: uploaded.Name,
		RawState:         batch.State,
	}, nil
}

func (p *GeminiAPIBatchImageProvider) Get(ctx context.Context, job *BatchImageJob, account *Account) (*BatchProviderStatus, error) {
	if account == nil || account.Platform != PlatformGemini || account.Type != AccountTypeAPIKey {
		return nil, ErrBatchImageProviderUnsupportedAccount
	}
	apiKey := batchImageProviderAPIKey(account)
	if apiKey == "" {
		return nil, ErrBatchImageProviderMissingAPIKey
	}
	jobName := batchImageProviderJobName(job)
	if jobName == "" {
		return nil, ErrBatchImageProviderMissingJobName
	}

	batch, err := p.client.GetBatch(ctx, apiKey, jobName)
	if err != nil {
		return nil, mapGeminiClientError(err)
	}
	if batch == nil {
		return nil, geminiProviderError("GEMINI_INVALID_RESPONSE", "Gemini batch response is empty", nil)
	}

	status := mapGeminiBatchState(batch)
	if status.InternalState == BatchProviderStateSucceeded {
		if geminiBatchHasInlineResults(batch) {
			return nil, ErrBatchImageProviderInlineResultUnsupported
		}
		outputRef := geminiBatchOutputRef(batch)
		if outputRef == "" {
			status.InternalState = BatchProviderStateFailed
			status.Done = true
			status.ErrorCode = "GEMINI_RESULT_FILE_MISSING"
			status.ErrorMessage = "Gemini batch succeeded without a result file reference"
		}
		status.ProviderOutputRef = outputRef
	}
	return status, nil
}

func (p *GeminiAPIBatchImageProvider) Cancel(ctx context.Context, job *BatchImageJob, account *Account) error {
	if account == nil || account.Platform != PlatformGemini || account.Type != AccountTypeAPIKey {
		return ErrBatchImageProviderUnsupportedAccount
	}
	apiKey := batchImageProviderAPIKey(account)
	if apiKey == "" {
		return ErrBatchImageProviderMissingAPIKey
	}
	jobName := batchImageProviderJobName(job)
	if jobName == "" {
		return ErrBatchImageProviderMissingJobName
	}
	return mapGeminiClientError(p.client.CancelBatch(ctx, apiKey, jobName))
}

func (p *GeminiAPIBatchImageProvider) OpenResult(ctx context.Context, job *BatchImageJob, account *Account) (io.ReadCloser, string, error) {
	if account == nil || account.Platform != PlatformGemini || account.Type != AccountTypeAPIKey {
		return nil, "", ErrBatchImageProviderUnsupportedAccount
	}
	apiKey := batchImageProviderAPIKey(account)
	if apiKey == "" {
		return nil, "", ErrBatchImageProviderMissingAPIKey
	}
	outputRef := batchImageProviderOutputRef(job)
	if outputRef == "" {
		return nil, "", ErrBatchImageProviderMissingResultRef
	}
	r, contentType, err := p.client.DownloadFile(ctx, apiKey, outputRef)
	return r, contentType, mapGeminiClientError(err)
}

func (p *GeminiAPIBatchImageProvider) Cleanup(ctx context.Context, job *BatchImageJob, account *Account, target CleanupTarget) error {
	if account == nil || account.Platform != PlatformGemini || account.Type != AccountTypeAPIKey {
		return ErrBatchImageProviderUnsupportedAccount
	}
	apiKey := batchImageProviderAPIKey(account)
	if apiKey == "" {
		return ErrBatchImageProviderMissingAPIKey
	}

	switch target {
	case CleanupTargetInput:
		return p.deleteGeminiFileIfPresent(ctx, apiKey, batchImageProviderInputRef(job))
	case CleanupTargetOutput:
		return p.deleteGeminiFileIfPresent(ctx, apiKey, batchImageProviderOutputRef(job))
	case CleanupTargetAll:
		if err := p.deleteGeminiFileIfPresent(ctx, apiKey, batchImageProviderInputRef(job)); err != nil {
			return err
		}
		return p.deleteGeminiFileIfPresent(ctx, apiKey, batchImageProviderOutputRef(job))
	default:
		return ErrUnsupportedCleanupTarget
	}
}

func (p *GeminiAPIBatchImageProvider) deleteGeminiFileIfPresent(ctx context.Context, apiKey, fileName string) error {
	if strings.TrimSpace(fileName) == "" {
		return nil
	}
	return mapGeminiClientError(p.client.DeleteFile(ctx, apiKey, fileName))
}

func BuildGeminiBatchJSONL(input BatchImageInput) ([]byte, error) {
	value := gemininative.BatchJSONLInput{Model: input.Model, Items: make([]gemininative.BatchJSONLItem, len(input.Items))}
	for i, item := range input.Items {
		value.Items[i] = gemininative.BatchJSONLItem{CustomID: item.CustomID, Prompt: item.Prompt, ReferenceImages: make([]gemininative.BatchReference, len(item.ReferenceImages))}
		for j, ref := range item.ReferenceImages {
			value.Items[i].ReferenceImages[j] = gemininative.BatchReference{MimeType: normalizeBatchImageReferenceMimeType(ref.MimeType), Data: ref.Data, FileURI: ref.FileURI}
		}
	}
	return gemininative.BuildGeminiBatchJSONL(value, batchImageProviderInputError)
}

func mapGeminiBatchState(batch *GeminiBatchJob) *BatchProviderStatus {
	state := strings.TrimSpace(batch.State)
	normalized := strings.ToUpper(state)
	status := &BatchProviderStatus{
		RawState:              state,
		InternalState:         BatchProviderStateRunning,
		SuggestedRequeueAfter: defaultGeminiBatchRequeueAfter,
	}

	switch normalized {
	case "JOB_STATE_PENDING", "JOB_STATE_QUEUED":
		status.InternalState = BatchProviderStateQueued
	case "JOB_STATE_RUNNING":
		status.InternalState = BatchProviderStateRunning
	case "JOB_STATE_SUCCEEDED":
		status.InternalState = BatchProviderStateSucceeded
		status.Done = true
	case "JOB_STATE_FAILED":
		status.InternalState = BatchProviderStateFailed
		status.Done = true
		status.ErrorCode = "GEMINI_BATCH_FAILED"
	case "JOB_STATE_CANCELLED":
		status.InternalState = BatchProviderStateCancelled
		status.Done = true
		status.ErrorCode = "GEMINI_BATCH_CANCELLED"
	case "JOB_STATE_EXPIRED":
		status.InternalState = BatchProviderStateExpired
		status.Done = true
		status.ErrorCode = "GEMINI_BATCH_EXPIRED"
	default:
		if batch.Error != nil && (strings.TrimSpace(batch.Error.Message) != "" || strings.TrimSpace(batch.Error.Code) != "") {
			status.InternalState = BatchProviderStateFailed
			status.Done = true
			status.ErrorCode = "GEMINI_BATCH_FAILED"
		}
	}

	if batch.Error != nil {
		if code := strings.TrimSpace(batch.Error.Code); code != "" {
			status.ErrorCode = code
		} else if status.ErrorCode == "" && strings.TrimSpace(batch.Error.Status) != "" {
			status.ErrorCode = strings.TrimSpace(batch.Error.Status)
		}
		status.ErrorMessage = strings.TrimSpace(batch.Error.Message)
	}
	return status
}

func geminiBatchOutputRef(batch *GeminiBatchJob) string {
	if batch == nil {
		return ""
	}
	if batch.Dest != nil {
		if v := strings.TrimSpace(batch.Dest.FileName); v != "" {
			return v
		}
		if v := strings.TrimSpace(batch.Dest.FileNameSnake); v != "" {
			return v
		}
	}
	if batch.Response != nil {
		if v := strings.TrimSpace(batch.Response.ResponsesFile); v != "" {
			return v
		}
		if v := strings.TrimSpace(batch.Response.ResponsesFileSnake); v != "" {
			return v
		}
	}
	return ""
}

func geminiBatchHasInlineResults(batch *GeminiBatchJob) bool {
	return batch != nil &&
		batch.Response != nil &&
		(len(batch.Response.InlinedResponses) > 0 || len(batch.Response.InlinedResponsesAlt) > 0)
}

func geminiProviderError(reason, message string, cause error) error {
	return gemininative.ProviderError(reason, message, cause)
}

func mapGeminiClientError(err error) error { return gemininative.MapClientError(err) }

type GeminiBatchHTTPClient = gemininative.GeminiBatchHTTPClient

func NewGeminiBatchHTTPClient(baseURL string, client *http.Client) *GeminiBatchHTTPClient {
	return gemininative.NewGeminiBatchHTTPClient(baseURL, client, ErrBatchImageProviderMissingAPIKey)
}

type GeminiAPIError = gemininative.GeminiAPIError

var _ BatchImageProvider = (*GeminiAPIBatchImageProvider)(nil)
var _ GeminiBatchClient = (*GeminiBatchHTTPClient)(nil)
