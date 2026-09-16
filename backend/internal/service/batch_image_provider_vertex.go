package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/domain"
	vertex "github.com/TokenFlux/TokenRouter/internal/upstream/vertex"

	"github.com/TokenFlux/TokenRouter/internal/config"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
)

const (
	defaultVertexBatchRequeueAfter = 30 * time.Second
	defaultVertexBatchLocation     = "global"
	defaultVertexManagedGCSPrefix  = "batch-image/{env}/{batch_id}"
)

type VertexBatchImageProviderOptions struct {
	Enabled                bool
	ProjectID              string
	Location               string
	ManagedGCSBucket       string
	ManagedGCSPrefix       string
	Environment            string
	InputRetentionHours    int
	OutputRetentionHours   int
	BatchPredictionBaseURL string
	GCSBaseURL             string
}

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

type VertexBatchImageProvider struct {
	opts        VertexBatchImageProviderOptions
	client      VertexBatchClient
	objectStore VertexBatchObjectStore
	tokenCache  GeminiTokenCache
}

func NewVertexBatchImageProvider(opts VertexBatchImageProviderOptions, client VertexBatchClient, objectStore VertexBatchObjectStore, tokenCache GeminiTokenCache) *VertexBatchImageProvider {
	opts = normalizeVertexBatchImageProviderOptions(opts)
	if client == nil {
		client = NewVertexBatchHTTPClient(opts.BatchPredictionBaseURL, nil)
	}
	if objectStore == nil {
		objectStore = NewVertexGCSObjectStore(opts.GCSBaseURL, nil)
	}
	return &VertexBatchImageProvider{
		opts:        opts,
		client:      client,
		objectStore: objectStore,
		tokenCache:  tokenCache,
	}
}

func NewVertexBatchImageProviderFromConfig(cfg *config.Config, client VertexBatchClient, objectStore VertexBatchObjectStore, tokenCache GeminiTokenCache) *VertexBatchImageProvider {
	return NewVertexBatchImageProvider(NewVertexBatchImageProviderOptionsFromConfig(cfg), client, objectStore, tokenCache)
}

func normalizeVertexBatchImageProviderOptions(opts VertexBatchImageProviderOptions) VertexBatchImageProviderOptions {
	opts.ProjectID = strings.TrimSpace(opts.ProjectID)
	opts.Location = strings.TrimSpace(opts.Location)
	if opts.Location == "" {
		opts.Location = defaultVertexBatchLocation
	}
	opts.ManagedGCSBucket = strings.Trim(strings.TrimSpace(opts.ManagedGCSBucket), "/")
	opts.ManagedGCSPrefix = strings.Trim(strings.TrimSpace(opts.ManagedGCSPrefix), "/")
	if opts.ManagedGCSPrefix == "" {
		opts.ManagedGCSPrefix = defaultVertexManagedGCSPrefix
	}
	opts.Environment = strings.TrimSpace(opts.Environment)
	if opts.Environment == "" {
		opts.Environment = "default"
	}
	opts.BatchPredictionBaseURL = strings.TrimRight(strings.TrimSpace(opts.BatchPredictionBaseURL), "/")
	opts.GCSBaseURL = strings.TrimRight(strings.TrimSpace(opts.GCSBaseURL), "/")
	return opts
}

func (p *VertexBatchImageProvider) Name() string {
	return BatchImageProviderVertex
}

func (p *VertexBatchImageProvider) SupportsAccount(account *Account) bool {
	if account == nil || account.Platform != PlatformGemini || account.Type != AccountTypeServiceAccount {
		return false
	}
	_, err := parseVertexServiceAccountKey(account)
	return err == nil
}

func (p *VertexBatchImageProvider) Submit(ctx context.Context, job *BatchImageJob, account *Account, input BatchImageInput) (*BatchProviderJob, error) {
	if _, enabled := ResolveProtocolRoute(account, nil, domain.ProtocolImageBatches); !enabled {
		return nil, ErrBatchImageProviderUnsupportedAccount
	}
	if err := p.validateAccount(account); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.opts.ManagedGCSBucket) == "" {
		return nil, vertexProviderError("VERTEX_MANAGED_GCS_BUCKET_MISSING", "Vertex managed GCS bucket is not configured", nil)
	}
	if input.BatchID == "" && job != nil {
		input.BatchID = job.BatchID
	}
	if input.Model == "" && job != nil {
		input.Model = job.Model
	}

	jsonl, err := BuildVertexBatchJSONL(input)
	if err != nil {
		return nil, err
	}
	refs, err := p.managedRefs(input.BatchID)
	if err != nil {
		return nil, err
	}

	accessToken, err := p.accessToken(ctx, account)
	if err != nil {
		return nil, mapVertexClientError(err)
	}
	if err := p.objectStore.UploadJSONL(ctx, accessToken, refs.InputURI, bytes.NewReader(jsonl)); err != nil {
		return nil, vertexProviderError("VERTEX_GCS_UPLOAD_FAILED", "Vertex managed GCS upload failed", nil)
	}

	projectID := strings.TrimSpace(p.opts.ProjectID)
	if projectID == "" {
		projectID = account.VertexProjectID()
	}
	if projectID == "" {
		return nil, vertexProviderError("VERTEX_PROJECT_ID_MISSING", "Vertex project id is not configured", nil)
	}
	location := strings.TrimSpace(p.opts.Location)
	if location == "" {
		location = account.VertexLocation(input.Model)
	}

	req := VertexCreateBatchPredictionJobRequest{
		ProjectID:      projectID,
		Location:       location,
		DisplayName:    vertexBatchDisplayName(input),
		Model:          NormalizeVertexBatchModelPath(input.Model),
		InputConfig:    VertexBatchInputConfig{InstancesFormat: "jsonl", GCSSource: VertexBatchGCSSource{URIs: []string{refs.InputURI}}},
		OutputConfig:   VertexBatchOutputConfig{PredictionsFormat: "jsonl", GCSDestination: VertexBatchGCSDestination{OutputURIPrefix: refs.OutputPrefixURI}},
		InstanceConfig: &VertexBatchInstanceConfig{KeyField: "key"},
	}
	created, err := p.client.CreateBatchPredictionJob(ctx, accessToken, req)
	if err != nil {
		return nil, mapVertexClientError(err)
	}
	if created == nil || strings.TrimSpace(created.Name) == "" {
		return nil, vertexProviderError("VERTEX_INVALID_RESPONSE", "Vertex batch response is missing job name", nil)
	}
	return &BatchProviderJob{
		ProviderJobName:   created.Name,
		ProviderInputRef:  refs.InputURI,
		ProviderOutputRef: refs.OutputPrefixURI,
		RawState:          created.State,
	}, nil
}

func (p *VertexBatchImageProvider) Get(ctx context.Context, job *BatchImageJob, account *Account) (*BatchProviderStatus, error) {
	if err := p.validateAccount(account); err != nil {
		return nil, err
	}
	jobName := batchImageProviderJobName(job)
	if jobName == "" {
		return nil, ErrBatchImageProviderMissingJobName
	}
	accessToken, err := p.accessToken(ctx, account)
	if err != nil {
		return nil, mapVertexClientError(err)
	}
	vertexJob, err := p.client.GetBatchPredictionJob(ctx, accessToken, jobName)
	if err != nil {
		return nil, mapVertexClientError(err)
	}
	if vertexJob == nil {
		return nil, vertexProviderError("VERTEX_INVALID_RESPONSE", "Vertex batch response is empty", nil)
	}
	status := mapVertexBatchState(vertexJob)
	outputRef := strings.TrimSpace(vertexJob.OutputConfig.GCSDestination.OutputURIPrefix)
	if outputRef == "" {
		outputRef = batchImageProviderOutputRef(job)
	}
	if outputRef == "" && job != nil && job.GCSOutputURI != nil {
		outputRef = strings.TrimSpace(*job.GCSOutputURI)
	}
	status.ProviderOutputRef = outputRef
	return status, nil
}

func (p *VertexBatchImageProvider) Cancel(ctx context.Context, job *BatchImageJob, account *Account) error {
	if err := p.validateAccount(account); err != nil {
		return err
	}
	jobName := batchImageProviderJobName(job)
	if jobName == "" {
		return ErrBatchImageProviderMissingJobName
	}
	accessToken, err := p.accessToken(ctx, account)
	if err != nil {
		return mapVertexClientError(err)
	}
	return mapVertexClientError(p.client.CancelBatchPredictionJob(ctx, accessToken, jobName))
}

func (p *VertexBatchImageProvider) OpenResult(ctx context.Context, job *BatchImageJob, account *Account) (io.ReadCloser, string, error) {
	if err := p.validateAccount(account); err != nil {
		return nil, "", err
	}
	outputRef := batchImageProviderOutputRef(job)
	if outputRef == "" && job != nil && job.GCSOutputURI != nil {
		outputRef = strings.TrimSpace(*job.GCSOutputURI)
	}
	if outputRef == "" {
		return nil, "", ErrBatchImageProviderMissingResultRef
	}
	accessToken, err := p.accessToken(ctx, account)
	if err != nil {
		return nil, "", mapVertexClientError(err)
	}
	objects, err := p.objectStore.ListJSONLObjects(ctx, accessToken, outputRef)
	if err != nil {
		return nil, "", vertexProviderError("VERTEX_GCS_LIST_FAILED", "Vertex managed GCS list failed", nil)
	}
	sort.Strings(objects)
	if len(objects) == 0 {
		return nil, "", vertexProviderError("VERTEX_RESULT_OBJECTS_MISSING", "Vertex result objects are missing", nil)
	}
	return vertex.NewCombinedJSONLReadCloser(ctx, accessToken, objects, p.objectStore), "application/jsonl", nil
}

func (p *VertexBatchImageProvider) Cleanup(ctx context.Context, job *BatchImageJob, account *Account, target CleanupTarget) error {
	if err := p.validateAccount(account); err != nil {
		return err
	}
	accessToken, err := p.accessToken(ctx, account)
	if err != nil {
		return mapVertexClientError(err)
	}
	inputRef := batchImageProviderInputRef(job)
	outputRef := batchImageProviderOutputRef(job)
	if job != nil {
		if inputRef == "" && job.GCSInputURI != nil {
			inputRef = strings.TrimSpace(*job.GCSInputURI)
		}
		if outputRef == "" && job.GCSOutputURI != nil {
			outputRef = strings.TrimSpace(*job.GCSOutputURI)
		}
	}

	switch target {
	case CleanupTargetInput:
		return p.deleteManagedInput(ctx, accessToken, job, inputRef)
	case CleanupTargetOutput:
		return p.deleteManagedOutput(ctx, accessToken, job, outputRef)
	case CleanupTargetAll:
		if err := p.deleteManagedInput(ctx, accessToken, job, inputRef); err != nil {
			return err
		}
		return p.deleteManagedOutput(ctx, accessToken, job, outputRef)
	default:
		return ErrUnsupportedCleanupTarget
	}
}

func (p *VertexBatchImageProvider) validateAccount(account *Account) error {
	if account == nil || account.Platform != PlatformGemini || account.Type != AccountTypeServiceAccount {
		return ErrBatchImageProviderUnsupportedAccount
	}
	if _, err := parseVertexServiceAccountKey(account); err != nil {
		return ErrBatchImageProviderMissingServiceAccount
	}
	return nil
}

func (p *VertexBatchImageProvider) accessToken(ctx context.Context, account *Account) (string, error) {
	return getVertexServiceAccountAccessToken(ctx, p.tokenCache, account)
}

func (p *VertexBatchImageProvider) deleteManagedInput(ctx context.Context, accessToken string, job *BatchImageJob, uri string) error {
	if strings.TrimSpace(uri) == "" {
		return nil
	}
	if !p.isSafeManagedInput(job, uri) {
		return ErrBatchImageProviderUnsafeCleanupPath
	}
	return mapVertexClientError(p.objectStore.DeleteObject(ctx, accessToken, uri))
}

func (p *VertexBatchImageProvider) deleteManagedOutput(ctx context.Context, accessToken string, job *BatchImageJob, uri string) error {
	if strings.TrimSpace(uri) == "" {
		return nil
	}
	if !p.isSafeManagedOutput(job, uri) {
		return ErrBatchImageProviderUnsafeCleanupPath
	}
	return mapVertexClientError(p.objectStore.DeletePrefix(ctx, accessToken, uri))
}

func (p *VertexBatchImageProvider) isSafeManagedInput(job *BatchImageJob, uri string) bool {
	if job == nil || strings.TrimSpace(job.BatchID) == "" {
		return false
	}
	refs, err := p.managedRefs(job.BatchID)
	return err == nil && strings.TrimSpace(uri) == refs.InputURI
}

func (p *VertexBatchImageProvider) isSafeManagedOutput(job *BatchImageJob, uri string) bool {
	if job == nil || strings.TrimSpace(job.BatchID) == "" {
		return false
	}
	refs, err := p.managedRefs(job.BatchID)
	return err == nil && strings.HasPrefix(strings.TrimSpace(uri), refs.OutputPrefixURI)
}

type vertexManagedRefs struct {
	Prefix          string
	InputURI        string
	OutputPrefixURI string
}

func (p *VertexBatchImageProvider) managedRefs(batchID string) (vertexManagedRefs, error) {
	batchID = strings.TrimSpace(batchID)
	if !IsValidBatchImageID(batchID) {
		return vertexManagedRefs{}, batchImageProviderInputError("valid batch_id is required")
	}
	bucket := strings.Trim(strings.TrimSpace(p.opts.ManagedGCSBucket), "/")
	if bucket == "" || strings.Contains(bucket, "://") {
		return vertexManagedRefs{}, vertexProviderError("VERTEX_MANAGED_GCS_BUCKET_MISSING", "Vertex managed GCS bucket is not configured", nil)
	}
	prefix := buildVertexManagedGCSPrefix(p.opts.ManagedGCSPrefix, p.opts.Environment, batchID)
	if !strings.Contains(prefix, batchID) {
		return vertexManagedRefs{}, batchImageProviderInputError("managed GCS prefix must contain batch_id")
	}
	base := "gs://" + bucket + "/" + strings.Trim(prefix, "/")
	return vertexManagedRefs{
		Prefix:          strings.Trim(prefix, "/"),
		InputURI:        base + "/input/requests.jsonl",
		OutputPrefixURI: base + "/output/",
	}, nil
}

func buildVertexManagedGCSPrefix(template, env, batchID string) string {
	template = strings.Trim(strings.TrimSpace(template), "/")
	if template == "" {
		template = defaultVertexManagedGCSPrefix
	}
	env = sanitizeVertexGCSPathSegment(env)
	batchID = sanitizeVertexGCSPathSegment(batchID)
	prefix := strings.ReplaceAll(template, "{env}", env)
	prefix = strings.ReplaceAll(prefix, "{batch_id}", batchID)
	return strings.Trim(prefix, "/")
}

func sanitizeVertexGCSPathSegment(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return "default"
	}
	var b strings.Builder
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			_, _ = b.WriteRune(r)
		case r == '-', r == '_', r == '.':
			_, _ = b.WriteRune(r)
		default:
			_ = b.WriteByte('-')
		}
	}
	return b.String()
}

func vertexBatchDisplayName(input BatchImageInput) string {
	if v := strings.TrimSpace(input.DisplayName); v != "" {
		return v
	}
	if v := strings.TrimSpace(input.BatchID); v != "" {
		return "sub2api-" + v
	}
	return "sub2api-image-batch"
}

func mapVertexBatchState(job *VertexBatchPredictionJob) *BatchProviderStatus {
	state := strings.TrimSpace(job.State)
	status := &BatchProviderStatus{
		RawState:              state,
		InternalState:         BatchProviderStateRunning,
		SuggestedRequeueAfter: defaultVertexBatchRequeueAfter,
	}
	switch strings.ToUpper(state) {
	case "JOB_STATE_PENDING", "JOB_STATE_QUEUED":
		status.InternalState = BatchProviderStateQueued
	case "JOB_STATE_RUNNING", "JOB_STATE_PAUSED":
		status.InternalState = BatchProviderStateRunning
	case "JOB_STATE_SUCCEEDED":
		status.InternalState = BatchProviderStateSucceeded
		status.Done = true
		status.SuggestedRequeueAfter = 0
	case "JOB_STATE_FAILED":
		status.InternalState = BatchProviderStateFailed
		status.Done = true
		status.ErrorCode = "VERTEX_BATCH_FAILED"
		status.SuggestedRequeueAfter = 0
	case "JOB_STATE_CANCELLED":
		status.InternalState = BatchProviderStateCancelled
		status.Done = true
		status.ErrorCode = "VERTEX_BATCH_CANCELLED"
		status.SuggestedRequeueAfter = 0
	case "JOB_STATE_EXPIRED":
		status.InternalState = BatchProviderStateExpired
		status.Done = true
		status.ErrorCode = "VERTEX_BATCH_EXPIRED"
		status.SuggestedRequeueAfter = 0
	default:
		if job.Error != nil && strings.TrimSpace(job.Error.Message) != "" {
			status.InternalState = BatchProviderStateFailed
			status.Done = true
			status.ErrorCode = "VERTEX_BATCH_FAILED"
			status.SuggestedRequeueAfter = 0
		}
	}
	if job.Error != nil {
		if code := strings.TrimSpace(job.Error.Status); code != "" {
			status.ErrorCode = code
		}
		status.ErrorMessage = strings.TrimSpace(job.Error.Message)
	}
	return status
}

func vertexProviderError(reason, message string, cause error) error {
	err := infraerrors.New(http.StatusBadGateway, reason, message)
	if cause != nil {
		return err.WithCause(cause)
	}
	return err
}

func mapVertexClientError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrBatchImageProviderMissingServiceAccount) ||
		errors.Is(err, ErrBatchImageProviderMissingJobName) ||
		errors.Is(err, ErrBatchImageProviderMissingResultRef) ||
		errors.Is(err, ErrBatchImageProviderUnsafeCleanupPath) ||
		errors.Is(err, ErrUnsupportedCleanupTarget) {
		return err
	}
	var apiErr *VertexAPIError
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusUnauthorized:
			return vertexProviderError("VERTEX_AUTH_FAILED", "Vertex authentication failed", nil)
		case http.StatusForbidden:
			return vertexProviderError("VERTEX_PERMISSION_DENIED", "Vertex permission denied", nil)
		case http.StatusTooManyRequests:
			return vertexProviderError("VERTEX_RATE_LIMITED", "Vertex rate limit exceeded", nil)
		case http.StatusNotFound:
			return vertexProviderError("VERTEX_BATCH_NOT_FOUND", "Vertex batch resource was not found", nil)
		default:
			return vertexProviderError("VERTEX_INVALID_RESPONSE", "Vertex API request failed", nil)
		}
	}
	return vertexProviderError("VERTEX_INVALID_RESPONSE", "Vertex API request failed", err)
}

var _ BatchImageProvider = (*VertexBatchImageProvider)(nil)
var _ VertexBatchClient = (*VertexBatchHTTPClient)(nil)
var _ VertexBatchObjectStore = (*VertexGCSObjectStore)(nil)

type VertexBatchClient = vertex.VertexBatchClient

type VertexBatchObjectStore = vertex.VertexBatchObjectStore

type VertexCreateBatchPredictionJobRequest = vertex.VertexCreateBatchPredictionJobRequest

type VertexBatchInputConfig = vertex.VertexBatchInputConfig

type VertexBatchGCSSource = vertex.VertexBatchGCSSource

type VertexBatchOutputConfig = vertex.VertexBatchOutputConfig

type VertexBatchGCSDestination = vertex.VertexBatchGCSDestination

type VertexBatchInstanceConfig = vertex.VertexBatchInstanceConfig

type VertexBatchPredictionJob = vertex.VertexBatchPredictionJob

type VertexBatchJobError = vertex.VertexBatchJobError

func NormalizeVertexBatchModelPath(model string) string {
	return vertex.NormalizeVertexBatchModelPath(model)
}

func BuildVertexBatchPredictionJobsEndpoint(baseURL, projectID, location string) (string, error) {
	return vertex.BuildVertexBatchPredictionJobsEndpoint(baseURL, projectID, location)
}

type VertexBatchHTTPClient = vertex.VertexBatchHTTPClient

type VertexGCSObjectStore = vertex.VertexGCSObjectStore

type VertexAPIError = vertex.VertexAPIError

func NewVertexBatchHTTPClient(baseURL string, client *http.Client) *VertexBatchHTTPClient {
	return vertex.NewVertexBatchHTTPClient(baseURL, client)
}
func NewVertexGCSObjectStore(baseURL string, client *http.Client) *VertexGCSObjectStore {
	return vertex.NewVertexGCSObjectStore(baseURL, client)
}

// BuildVertexBatchJSONL 仅投影任务 wire 字段；MIME 规范化仍在任务输入边界执行。
func BuildVertexBatchJSONL(input BatchImageInput) ([]byte, error) {
	value := vertex.BatchJSONLInput{Model: input.Model, Items: make([]vertex.BatchJSONLItem, len(input.Items))}
	for i, item := range input.Items {
		value.Items[i] = vertex.BatchJSONLItem{CustomID: item.CustomID, Prompt: item.Prompt, ReferenceImages: make([]vertex.BatchReference, len(item.ReferenceImages))}
		for j, ref := range item.ReferenceImages {
			value.Items[i].ReferenceImages[j] = vertex.BatchReference{MimeType: normalizeBatchImageReferenceMimeType(ref.MimeType), Data: ref.Data, FileURI: ref.FileURI}
		}
	}
	return vertex.BuildVertexBatchJSONL(value, batchImageProviderInputError)
}
