package provider

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"

	core "github.com/TokenFlux/TokenRouter/internal/batchimage"

	vertex "github.com/TokenFlux/TokenRouter/internal/upstream/vertex"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

const (
	DefaultVertexBatchRequeueAfter = 30 * time.Second
	DefaultVertexBatchLocation     = "global"
	DefaultVertexManagedGCSPrefix  = "batch-image/{env}/{batch_id}"
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

type VertexBatchImageProvider struct {
	opts        VertexBatchImageProviderOptions
	client      VertexBatchClient
	objectStore VertexBatchObjectStore
	tokenCache  GeminiTokenCache
}

func NewVertexBatchImageProvider(opts VertexBatchImageProviderOptions, client VertexBatchClient, objectStore VertexBatchObjectStore, tokenCache GeminiTokenCache) *VertexBatchImageProvider {
	opts = NormalizeVertexBatchImageProviderOptions(opts)
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

func NormalizeVertexBatchImageProviderOptions(opts VertexBatchImageProviderOptions) VertexBatchImageProviderOptions {
	opts.ProjectID = strings.TrimSpace(opts.ProjectID)
	opts.Location = strings.TrimSpace(opts.Location)
	if opts.Location == "" {
		opts.Location = DefaultVertexBatchLocation
	}
	opts.ManagedGCSBucket = strings.Trim(strings.TrimSpace(opts.ManagedGCSBucket), "/")
	opts.ManagedGCSPrefix = strings.Trim(strings.TrimSpace(opts.ManagedGCSPrefix), "/")
	if opts.ManagedGCSPrefix == "" {
		opts.ManagedGCSPrefix = DefaultVertexManagedGCSPrefix
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
	return core.BatchImageProviderVertex
}

func (p *VertexBatchImageProvider) SupportsAccount(account *Account) bool {
	if account == nil || account.Platform != PlatformGemini || account.Type != AccountTypeServiceAccount {
		return false
	}
	_, err := accountprovider.ParseVertexServiceAccountKey(account)
	return err == nil
}

func (p *VertexBatchImageProvider) Submit(ctx context.Context, job *core.BatchImageJob, account *Account, input core.BatchImageInput) (*core.BatchProviderJob, error) {
	if _, enabled := resolveBatchProtocol(account); !enabled {
		return nil, core.ErrBatchImageProviderUnsupportedAccount
	}
	if err := p.ValidateAccount(account); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.opts.ManagedGCSBucket) == "" {
		return nil, VertexProviderError("VERTEX_MANAGED_GCS_BUCKET_MISSING", "Vertex managed GCS bucket is not configured", nil)
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
	refs, err := p.ManagedRefs(input.BatchID)
	if err != nil {
		return nil, err
	}

	AccessToken, err := p.AccessToken(ctx, account)
	if err != nil {
		return nil, MapVertexClientError(err)
	}
	if err := p.objectStore.UploadJSONL(ctx, AccessToken, refs.InputURI, bytes.NewReader(jsonl)); err != nil {
		return nil, VertexProviderError("VERTEX_GCS_UPLOAD_FAILED", "Vertex managed GCS upload failed", nil)
	}

	projectID := strings.TrimSpace(p.opts.ProjectID)
	if projectID == "" {
		projectID = vertexProjectID(account)
	}
	if projectID == "" {
		return nil, VertexProviderError("VERTEX_PROJECT_ID_MISSING", "Vertex project id is not configured", nil)
	}
	location := strings.TrimSpace(p.opts.Location)
	if location == "" {
		location = account.VertexLocation(input.Model)
	}

	req := VertexCreateBatchPredictionJobRequest{
		ProjectID:      projectID,
		Location:       location,
		DisplayName:    VertexBatchDisplayName(input),
		Model:          NormalizeVertexBatchModelPath(input.Model),
		InputConfig:    VertexBatchInputConfig{InstancesFormat: "jsonl", GCSSource: VertexBatchGCSSource{URIs: []string{refs.InputURI}}},
		OutputConfig:   VertexBatchOutputConfig{PredictionsFormat: "jsonl", GCSDestination: VertexBatchGCSDestination{OutputURIPrefix: refs.OutputPrefixURI}},
		InstanceConfig: &VertexBatchInstanceConfig{KeyField: "key"},
	}
	created, err := p.client.CreateBatchPredictionJob(ctx, AccessToken, req)
	if err != nil {
		return nil, MapVertexClientError(err)
	}
	if created == nil || strings.TrimSpace(created.Name) == "" {
		return nil, VertexProviderError("VERTEX_INVALID_RESPONSE", "Vertex batch response is missing job name", nil)
	}
	return &core.BatchProviderJob{
		ProviderJobName:   created.Name,
		ProviderInputRef:  refs.InputURI,
		ProviderOutputRef: refs.OutputPrefixURI,
		RawState:          created.State,
	}, nil
}

func (p *VertexBatchImageProvider) Get(ctx context.Context, job *core.BatchImageJob, account *Account) (*core.BatchProviderStatus, error) {
	if err := p.ValidateAccount(account); err != nil {
		return nil, err
	}
	jobName := core.BatchImageProviderJobName(job)
	if jobName == "" {
		return nil, core.ErrBatchImageProviderMissingJobName
	}
	AccessToken, err := p.AccessToken(ctx, account)
	if err != nil {
		return nil, MapVertexClientError(err)
	}
	vertexJob, err := p.client.GetBatchPredictionJob(ctx, AccessToken, jobName)
	if err != nil {
		return nil, MapVertexClientError(err)
	}
	if vertexJob == nil {
		return nil, VertexProviderError("VERTEX_INVALID_RESPONSE", "Vertex batch response is empty", nil)
	}
	status := MapVertexBatchState(vertexJob)
	outputRef := strings.TrimSpace(vertexJob.OutputConfig.GCSDestination.OutputURIPrefix)
	if outputRef == "" {
		outputRef = core.BatchImageProviderOutputRef(job)
	}
	if outputRef == "" && job != nil && job.GCSOutputURI != nil {
		outputRef = strings.TrimSpace(*job.GCSOutputURI)
	}
	status.ProviderOutputRef = outputRef
	return status, nil
}

func (p *VertexBatchImageProvider) Cancel(ctx context.Context, job *core.BatchImageJob, account *Account) error {
	if err := p.ValidateAccount(account); err != nil {
		return err
	}
	jobName := core.BatchImageProviderJobName(job)
	if jobName == "" {
		return core.ErrBatchImageProviderMissingJobName
	}
	AccessToken, err := p.AccessToken(ctx, account)
	if err != nil {
		return MapVertexClientError(err)
	}
	return MapVertexClientError(p.client.CancelBatchPredictionJob(ctx, AccessToken, jobName))
}

func (p *VertexBatchImageProvider) OpenResult(ctx context.Context, job *core.BatchImageJob, account *Account) (io.ReadCloser, string, error) {
	if err := p.ValidateAccount(account); err != nil {
		return nil, "", err
	}
	outputRef := core.BatchImageProviderOutputRef(job)
	if outputRef == "" && job != nil && job.GCSOutputURI != nil {
		outputRef = strings.TrimSpace(*job.GCSOutputURI)
	}
	if outputRef == "" {
		return nil, "", core.ErrBatchImageProviderMissingResultRef
	}
	AccessToken, err := p.AccessToken(ctx, account)
	if err != nil {
		return nil, "", MapVertexClientError(err)
	}
	objects, err := p.objectStore.ListJSONLObjects(ctx, AccessToken, outputRef)
	if err != nil {
		return nil, "", VertexProviderError("VERTEX_GCS_LIST_FAILED", "Vertex managed GCS list failed", nil)
	}
	sort.Strings(objects)
	if len(objects) == 0 {
		return nil, "", VertexProviderError("VERTEX_RESULT_OBJECTS_MISSING", "Vertex result objects are missing", nil)
	}
	return vertex.NewCombinedJSONLReadCloser(ctx, AccessToken, objects, p.objectStore), "application/jsonl", nil
}

func (p *VertexBatchImageProvider) Cleanup(ctx context.Context, job *core.BatchImageJob, account *Account, target core.CleanupTarget) error {
	if err := p.ValidateAccount(account); err != nil {
		return err
	}
	AccessToken, err := p.AccessToken(ctx, account)
	if err != nil {
		return MapVertexClientError(err)
	}
	inputRef := core.BatchImageProviderInputRef(job)
	outputRef := core.BatchImageProviderOutputRef(job)
	if job != nil {
		if inputRef == "" && job.GCSInputURI != nil {
			inputRef = strings.TrimSpace(*job.GCSInputURI)
		}
		if outputRef == "" && job.GCSOutputURI != nil {
			outputRef = strings.TrimSpace(*job.GCSOutputURI)
		}
	}

	switch target {
	case core.CleanupTargetInput:
		return p.DeleteManagedInput(ctx, AccessToken, job, inputRef)
	case core.CleanupTargetOutput:
		return p.DeleteManagedOutput(ctx, AccessToken, job, outputRef)
	case core.CleanupTargetAll:
		if err := p.DeleteManagedInput(ctx, AccessToken, job, inputRef); err != nil {
			return err
		}
		return p.DeleteManagedOutput(ctx, AccessToken, job, outputRef)
	default:
		return core.ErrUnsupportedCleanupTarget
	}
}

func (p *VertexBatchImageProvider) ValidateAccount(account *Account) error {
	if account == nil || account.Platform != PlatformGemini || account.Type != AccountTypeServiceAccount {
		return core.ErrBatchImageProviderUnsupportedAccount
	}
	if _, err := accountprovider.ParseVertexServiceAccountKey(account); err != nil {
		return core.ErrBatchImageProviderMissingServiceAccount
	}
	return nil
}

func (p *VertexBatchImageProvider) AccessToken(ctx context.Context, account *Account) (string, error) {
	return accountprovider.VertexServiceAccountAccessToken(ctx, p.tokenCache, account)
}

func (p *VertexBatchImageProvider) DeleteManagedInput(ctx context.Context, AccessToken string, job *core.BatchImageJob, uri string) error {
	if strings.TrimSpace(uri) == "" {
		return nil
	}
	if !p.IsSafeManagedInput(job, uri) {
		return core.ErrBatchImageProviderUnsafeCleanupPath
	}
	return MapVertexClientError(p.objectStore.DeleteObject(ctx, AccessToken, uri))
}

func (p *VertexBatchImageProvider) DeleteManagedOutput(ctx context.Context, AccessToken string, job *core.BatchImageJob, uri string) error {
	if strings.TrimSpace(uri) == "" {
		return nil
	}
	if !p.IsSafeManagedOutput(job, uri) {
		return core.ErrBatchImageProviderUnsafeCleanupPath
	}
	return MapVertexClientError(p.objectStore.DeletePrefix(ctx, AccessToken, uri))
}

func (p *VertexBatchImageProvider) IsSafeManagedInput(job *core.BatchImageJob, uri string) bool {
	if job == nil || strings.TrimSpace(job.BatchID) == "" {
		return false
	}
	refs, err := p.ManagedRefs(job.BatchID)
	return err == nil && strings.TrimSpace(uri) == refs.InputURI
}

func (p *VertexBatchImageProvider) IsSafeManagedOutput(job *core.BatchImageJob, uri string) bool {
	if job == nil || strings.TrimSpace(job.BatchID) == "" {
		return false
	}
	refs, err := p.ManagedRefs(job.BatchID)
	return err == nil && strings.HasPrefix(strings.TrimSpace(uri), refs.OutputPrefixURI)
}

type VertexManagedRefs struct {
	Prefix          string
	InputURI        string
	OutputPrefixURI string
}

func (p *VertexBatchImageProvider) ManagedRefs(batchID string) (VertexManagedRefs, error) {
	batchID = strings.TrimSpace(batchID)
	if !core.IsValidBatchImageID(batchID) {
		return VertexManagedRefs{}, core.BatchImageProviderInputError("valid batch_id is required")
	}
	bucket := strings.Trim(strings.TrimSpace(p.opts.ManagedGCSBucket), "/")
	if bucket == "" || strings.Contains(bucket, "://") {
		return VertexManagedRefs{}, VertexProviderError("VERTEX_MANAGED_GCS_BUCKET_MISSING", "Vertex managed GCS bucket is not configured", nil)
	}
	prefix := BuildVertexManagedGCSPrefix(p.opts.ManagedGCSPrefix, p.opts.Environment, batchID)
	if !strings.Contains(prefix, batchID) {
		return VertexManagedRefs{}, core.BatchImageProviderInputError("managed GCS prefix must contain batch_id")
	}
	base := "gs://" + bucket + "/" + strings.Trim(prefix, "/")
	return VertexManagedRefs{
		Prefix:          strings.Trim(prefix, "/"),
		InputURI:        base + "/input/requests.jsonl",
		OutputPrefixURI: base + "/output/",
	}, nil
}

func BuildVertexManagedGCSPrefix(template, env, batchID string) string {
	template = strings.Trim(strings.TrimSpace(template), "/")
	if template == "" {
		template = DefaultVertexManagedGCSPrefix
	}
	env = SanitizeVertexGCSPathSegment(env)
	batchID = SanitizeVertexGCSPathSegment(batchID)
	prefix := strings.ReplaceAll(template, "{env}", env)
	prefix = strings.ReplaceAll(prefix, "{batch_id}", batchID)
	return strings.Trim(prefix, "/")
}

func SanitizeVertexGCSPathSegment(v string) string {
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

func VertexBatchDisplayName(input core.BatchImageInput) string {
	if v := strings.TrimSpace(input.DisplayName); v != "" {
		return v
	}
	if v := strings.TrimSpace(input.BatchID); v != "" {
		return "sub2api-" + v
	}
	return "sub2api-image-batch"
}

func MapVertexBatchState(job *VertexBatchPredictionJob) *core.BatchProviderStatus {
	state := strings.TrimSpace(job.State)
	status := &core.BatchProviderStatus{
		RawState:              state,
		InternalState:         core.BatchProviderStateRunning,
		SuggestedRequeueAfter: DefaultVertexBatchRequeueAfter,
	}
	switch strings.ToUpper(state) {
	case "JOB_STATE_PENDING", "JOB_STATE_QUEUED":
		status.InternalState = core.BatchProviderStateQueued
	case "JOB_STATE_RUNNING", "JOB_STATE_PAUSED":
		status.InternalState = core.BatchProviderStateRunning
	case "JOB_STATE_SUCCEEDED":
		status.InternalState = core.BatchProviderStateSucceeded
		status.Done = true
		status.SuggestedRequeueAfter = 0
	case "JOB_STATE_FAILED":
		status.InternalState = core.BatchProviderStateFailed
		status.Done = true
		status.ErrorCode = "VERTEX_BATCH_FAILED"
		status.SuggestedRequeueAfter = 0
	case "JOB_STATE_CANCELLED":
		status.InternalState = core.BatchProviderStateCancelled
		status.Done = true
		status.ErrorCode = "VERTEX_BATCH_CANCELLED"
		status.SuggestedRequeueAfter = 0
	case "JOB_STATE_EXPIRED":
		status.InternalState = core.BatchProviderStateExpired
		status.Done = true
		status.ErrorCode = "VERTEX_BATCH_EXPIRED"
		status.SuggestedRequeueAfter = 0
	default:
		if job.Error != nil && strings.TrimSpace(job.Error.Message) != "" {
			status.InternalState = core.BatchProviderStateFailed
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

func VertexProviderError(reason, message string, cause error) error {
	err := infraerrors.New(infraerrors.CategoryBadGateway, reason, message)
	if cause != nil {
		return err.WithCause(cause)
	}
	return err
}

func MapVertexClientError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, core.ErrBatchImageProviderMissingServiceAccount) ||
		errors.Is(err, core.ErrBatchImageProviderMissingJobName) ||
		errors.Is(err, core.ErrBatchImageProviderMissingResultRef) ||
		errors.Is(err, core.ErrBatchImageProviderUnsafeCleanupPath) ||
		errors.Is(err, core.ErrUnsupportedCleanupTarget) {
		return err
	}
	var apiErr *VertexAPIError
	if errors.As(err, &apiErr) {
		switch apiErr.StatusCode {
		case http.StatusUnauthorized:
			return VertexProviderError("VERTEX_AUTH_FAILED", "Vertex authentication failed", nil)
		case http.StatusForbidden:
			return VertexProviderError("VERTEX_PERMISSION_DENIED", "Vertex permission denied", nil)
		case http.StatusTooManyRequests:
			return VertexProviderError("VERTEX_RATE_LIMITED", "Vertex rate limit exceeded", nil)
		case http.StatusNotFound:
			return VertexProviderError("VERTEX_BATCH_NOT_FOUND", "Vertex batch resource was not found", nil)
		default:
			return VertexProviderError("VERTEX_INVALID_RESPONSE", "Vertex API request failed", nil)
		}
	}
	return VertexProviderError("VERTEX_INVALID_RESPONSE", "Vertex API request failed", err)
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
func BuildVertexBatchJSONL(input core.BatchImageInput) ([]byte, error) {
	value := vertex.BatchJSONLInput{Model: input.Model, Items: make([]vertex.BatchJSONLItem, len(input.Items))}
	for i, item := range input.Items {
		value.Items[i] = vertex.BatchJSONLItem{CustomID: item.CustomID, Prompt: item.Prompt, ReferenceImages: make([]vertex.BatchReference, len(item.ReferenceImages))}
		for j, ref := range item.ReferenceImages {
			value.Items[i].ReferenceImages[j] = vertex.BatchReference{MimeType: core.NormalizeBatchImageReferenceMimeType(ref.MimeType), Data: ref.Data, FileURI: ref.FileURI}
		}
	}
	return vertex.BuildVertexBatchJSONL(value, core.BatchImageProviderInputError)
}
