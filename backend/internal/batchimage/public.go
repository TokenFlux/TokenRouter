// Public 拥有批量任务提交、验证、查询及取消，供应商调用只使用已绑定的执行句柄。
package batchimage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/modelmap"
)

const PlatformGemini = "gemini"
const (
	BillingModelSourceRequested     = routing.BillingModelSourceRequested
	BillingModelSourceUpstream      = routing.BillingModelSourceUpstream
	BillingModelSourceChannelMapped = routing.BillingModelSourceChannelMapped
)

type ChannelMappingResult = routing.ChannelMappingResult
type ChannelReader interface {
	ResolveChannelMapping(context.Context, int64, string) ChannelMappingResult
	IsModelRestricted(context.Context, int64, string) bool
}
type CandidateRules interface {
	IsSchedulable() bool
	IsModelSupported(string) bool
	GetModelMapping() map[string]string
	ResolveMappedModel(string) (string, bool)
	BillingRateMultiplier() float64
}
type ExecutionProvider interface {
	BoundProvider
	Name() string
}
type Candidate struct {
	ID       int64
	Priority int
	CandidateRules
	ProtocolEnabled  func() bool
	SupportsProvider func(string) bool
	ResolveUpstream  func(context.Context, string) string
	Bind             func(string) ExecutionProvider
}
type AccountReader interface {
	GetByID(context.Context, int64) (*Candidate, error)
	ListSchedulableByPlatform(context.Context, string) ([]Candidate, error)
	ListSchedulableByGroupIDAndPlatform(context.Context, int64, string) ([]Candidate, error)
}
type GroupView struct {
	ID                                                                     int64
	Platform                                                               string
	AllowBatchImageGeneration                                              bool
	RateMultiplier, BatchImageDiscountMultiplier, BatchImageHoldMultiplier float64
	Price                                                                  billing.PriceGroup
}
type GroupReader interface {
	GetByIDLite(context.Context, int64) (*GroupView, error)
}
type BatchImagePriceInput struct {
	Model     string
	GroupID   *int64
	Group     *GroupView
	ImageSize string
}
type ImagePricer interface {
	BatchImageUnitPrice(context.Context, BatchImagePriceInput) (float64, error)
}
type PublicOptions struct {
	Enabled                                                                                                                                                                       bool
	StaleActiveAfterSeconds, MaxItemsPerJobDefault, MaxOutputImagesPerJob, MaxOutputImagesPerItem, MaxPromptCharsPerItem, MaxReferenceImagesPerJob, MaxReferenceInlineBytesPerJob int
	DefaultResponseMimeType, DefaultImageSize                                                                                                                                     string
}
type Public struct {
	Now                    func() time.Time
	Repo                   BatchImageRepository
	AccountRepo            AccountReader
	ChannelService         ChannelReader
	GroupRepo              GroupReader
	UserGroupRateRepo      BatchImageUserGroupRateRepository
	Queue                  BatchImageQueue
	Pricing                ImagePricer
	Funding                Funding
	Options                PublicOptions
	ProviderExists         func(string) bool
	InvalidateAuth         func(context.Context, int64)
	Observe                func(string, ...any)
	ClientModel            func(context.Context) string
	WithModelTrace         func(context.Context, ChannelMappingResult, string) ChannelMappingResult
	RegisterModel          func(context.Context, string)
	AutoSubscription       func(context.Context, int64, *int64) *billing.UserSubscription
	PreferredSubscription  func(context.Context, int64, int64, *int64) *billing.UserSubscription
	SubscriptionMultiplier func(context.Context, BatchImageOwner, *GroupView, float64, *billing.UserSubscription) float64
}

func (s *Public) warn(event string, values ...any) {
	if s.Observe != nil {
		s.Observe(event, values...)
	}
}
func mappedCandidateModel(c *Candidate, m string) string {
	if c == nil {
		return ""
	}
	value, matched := c.ResolveMappedModel(m)
	if !matched || strings.TrimSpace(value) == "" {
		return m
	}
	return strings.TrimSpace(value)
}

const (
	DefaultBatchImageMaxItems           = 200
	DefaultBatchImageMaxOutputImages    = 200
	DefaultBatchImageMaxOutputCount     = 4
	DefaultBatchImageMaxPromptChars     = 8000
	DefaultBatchImageResponseMime       = "image/png"
	DefaultBatchImageImageSize          = "1K"
	DefaultBatchImageDiscountMultiplier = 0.5
	DefaultBatchImageHoldMultiplier     = 0.6
	MaxBatchImagePublicErrorChars       = 500
	MaxBatchImageReferenceImageBytes    = 10 * 1024 * 1024
	DefaultBatchImageMaxReferenceImages = 1000
	DefaultBatchImageMaxReferenceBytes  = 128 * 1024 * 1024
)

// 批量图片作业的提交、预占和后续状态生命周期由对应工程文档维护。
// @project-doc docs/domains/batch_image_jobs.md#job_lifecycle
func (s *Public) Submit(ctx context.Context, owner BatchImageOwner, req BatchImageSubmitRequest, idempotencyKey string) (*BatchImagePublicBatch, error) {
	if !s.Enabled() {
		return nil, ErrBatchImageDisabled
	}
	normalized, err := s.ValidateSubmitRequest(req)
	if err != nil {
		return nil, err
	}
	// 与 ListModels 使用同一鉴权谓词（AllowBatchImageGeneration + Platform==Gemini），
	// 避免两个入口校验口径不一致留下防御纵深缺口。
	if err := s.EnsureGroupAllowsBatchImage(ctx, owner.GroupID); err != nil {
		return nil, err
	}
	requestedModel := normalized.Model
	if clientModel := s.ClientModel(ctx); strings.TrimSpace(clientModel) != "" {
		requestedModel = strings.TrimSpace(clientModel)
	}
	requestHash := HashBatchImageSubmitRequest(normalized)
	if requestedModel != normalized.Model {
		requestHash = HashCompositeBatchImageSubmitRequest(normalized, owner.GroupID)
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey != "" {
		existing, err := s.Repo.GetBatchImageJobByIdempotencyKey(ctx, owner.UserID, owner.APIKeyID, idempotencyKey)
		if err == nil {
			if BatchImageDerefString(existing.RequestHash) != requestHash {
				return nil, ErrBatchImageIdempotencyConflict
			}
			if existing.Status == BatchImageJobStatusSubmitted && s.Queue != nil {
				if enqueueErr := s.Queue.Enqueue(ctx, existing.BatchID); enqueueErr != nil && !errors.Is(enqueueErr, ErrBatchImageAlreadyQueued) {
					_ = s.Repo.RecordBatchImageJobSubmitFailure(ctx, existing.BatchID, "QUEUE_FAILED", SanitizeBatchImagePublicMessage(enqueueErr.Error()), false)
					return nil, ErrBatchImageQueueFailed
				}
			}
			return BatchImageJobToPublic(existing), nil
		}
		if !errors.Is(err, ErrBatchImageJobNotFound) {
			return nil, err
		}
	}

	channelMapping, routingModel, err := s.ResolveBatchImageChannelModel(ctx, owner.GroupID, normalized.Model)
	if err != nil {
		return nil, err
	}
	provider, account, upstreamModel, err := s.SelectProviderAndAccount(
		ctx,
		owner,
		normalized.Provider,
		routingModel,
		channelMapping,
	)
	if err != nil {
		return nil, err
	}
	pricingRequest := normalized
	pricingRequest.Model = BatchImagePricingModel(channelMapping, normalized.Model, routingModel, upstreamModel)
	pricingSnapshot, err := s.ResolvePricingSnapshot(ctx, owner, pricingRequest, provider.Name(), account)
	if err != nil {
		return nil, err
	}
	parentBatchID := BatchImageOptionalStringPtr(normalized.ParentBatchID)
	if parentBatchID != nil {
		parent, parentErr := s.Repo.GetBatchImageJobByBatchIDForOwner(ctx, owner.UserID, owner.APIKeyID, *parentBatchID)
		if parentErr != nil {
			return nil, parentErr
		}
		if parent.ParentBatchID != nil && strings.TrimSpace(*parent.ParentBatchID) != "" {
			parentBatchID = BatchImageOptionalStringPtr(*parent.ParentBatchID)
		}
	}
	batchID, err := NewBatchImageID()
	if err != nil {
		return nil, err
	}
	apiKeyID := owner.APIKeyID
	accountID := account.ID
	holdID := BatchImageHoldRequestID(batchID)
	holdAmount := pricingSnapshot.HoldAmount
	job, err := s.Repo.CreateBatchImageJob(ctx, CreateBatchImageJobParams{
		BatchID:                    batchID,
		UserID:                     owner.UserID,
		BillingUserID:              owner.BillingUserID,
		TeamID:                     owner.TeamID,
		APIKeyID:                   &apiKeyID,
		AccountID:                  &accountID,
		GroupID:                    owner.GroupID,
		BillingMode:                owner.BillingMode,
		PreferredSubscriptionID:    CloneInt64Ptr(owner.PreferredSubscriptionID),
		Provider:                   provider.Name(),
		Model:                      upstreamModel,
		RequestedModel:             requestedModel,
		InternalModel:              normalized.Model,
		TaskName:                   normalized.TaskName,
		ParentBatchID:              parentBatchID,
		Status:                     BatchImageJobStatusCreated,
		ItemCount:                  len(normalized.Items),
		EstimatedCost:              pricingSnapshot.EstimatedCost,
		HoldAmount:                 &holdAmount,
		BaseUnitPrice:              pricingSnapshot.BaseUnitPrice,
		GroupRateMultiplier:        pricingSnapshot.GroupRateMultiplier,
		SubscriptionRateMultiplier: pricingSnapshot.SubscriptionRateMultiplier,
		BalanceRateMultiplier:      pricingSnapshot.BalanceRateMultiplier,
		PlanGroupRateEnabled:       pricingSnapshot.PlanGroupRateEnabled,
		AccountRateMultiplier:      pricingSnapshot.AccountRateMultiplier,
		BatchDiscountMultiplier:    pricingSnapshot.BatchDiscountMultiplier,
		HoldMultiplier:             pricingSnapshot.HoldMultiplier,
		BillableUnitPrice:          pricingSnapshot.BillableUnitPrice,
		HoldUnitPrice:              pricingSnapshot.HoldUnitPrice,
		PricingSnapshotVersion:     3,
		Currency:                   "USD",
		HoldID:                     &holdID,
		IdempotencyKey:             BatchImageOptionalStringPtr(idempotencyKey),
		RequestHash:                BatchImageStringPtr(requestHash),
		SessionID:                  normalized.SessionID,
	})
	if err != nil {
		return nil, err
	}
	if err := s.Funding.Reserve(ctx, job, owner.GroupID, requestHash); err != nil {
		code := BatchImageBillingHoldFailureCode(err)
		_ = s.Repo.RecordBatchImageJobSubmitFailure(ctx, job.BatchID, code, SanitizeBatchImagePublicMessage(err.Error()), true)
		s.HidePreUpstreamSubmitFailure(ctx, owner, job)
		return nil, err
	}
	s.InvalidateAuthCache(ctx, owner.EffectiveBillingUserID())
	if err := s.CreatePendingItems(ctx, job.BatchID, requestHash, normalized.Items); err != nil {
		if releaseErr := s.ReleaseFailedSubmitHold(ctx, job, requestHash); releaseErr != nil {
			return nil, releaseErr
		}
		_ = s.Repo.RecordBatchImageJobSubmitFailure(ctx, job.BatchID, "ITEM_CREATE_FAILED", SanitizeBatchImagePublicMessage(err.Error()), true)
		s.HidePreUpstreamSubmitFailure(ctx, owner, job)
		return nil, ErrBatchImageQueueFailed
	}

	input := BatchImageInput{
		BatchID:          job.BatchID,
		Model:            upstreamModel,
		DisplayName:      job.BatchID,
		ResponseMimeType: normalized.ResponseMimeType,
		AspectRatio:      normalized.AspectRatio,
		ImageSize:        normalized.ImageSize,
		Metadata:         normalized.Metadata,
		Items:            make([]BatchImageInputItem, 0, len(normalized.Items)),
	}
	for _, item := range normalized.Items {
		refs := make([]BatchImageReference, 0, len(item.ReferenceImages))
		for _, ref := range item.ReferenceImages {
			refs = append(refs, BatchImageReference(ref))
		}
		input.Items = append(input.Items, BatchImageInputItem{
			CustomID:        item.CustomID,
			Prompt:          item.Prompt,
			ReferenceImages: refs,
		})
	}

	// 上游提交（上传参考图 + 创建批任务）可能长达数分钟且不刷新 updated_at，
	// 会被 stale 恢复扫描误判为滞留并退款。提交前转入 uploading 刷新时间戳，
	// 提交期间用心跳持续续期。
	if err := s.Repo.TransitionBatchImageJobStatus(ctx, job.BatchID, BatchImageJobStatusUploading, BatchImageTransitionOptions{
		EventType:    "upload_started",
		EventPayload: map[string]any{"batch_id": job.BatchID},
	}); err != nil {
		if releaseErr := s.ReleaseFailedSubmitHold(ctx, job, requestHash); releaseErr != nil {
			return nil, releaseErr
		}
		// 并发 Cancel 等导致的非法转换：job 已处于终态，不再覆盖其状态。
		if !errors.Is(err, ErrBatchImageInvalidTransition) {
			_ = s.Repo.RecordBatchImageJobSubmitFailure(ctx, job.BatchID, "UPLOAD_TRANSITION_FAILED", SanitizeBatchImagePublicMessage(err.Error()), true)
			s.HidePreUpstreamSubmitFailure(ctx, owner, job)
		}
		return nil, err
	}
	job.Status = BatchImageJobStatusUploading

	hbCtx, hbCancel := context.WithCancel(ctx)
	hbDone := make(chan struct{})
	go s.RunSubmitHeartbeat(hbCtx, job.BatchID, hbDone)
	providerJob, err := provider.Submit(ctx, job, input)
	hbCancel()
	<-hbDone
	if err != nil {
		if releaseErr := s.ReleaseFailedSubmitHold(ctx, job, requestHash); releaseErr != nil {
			return nil, releaseErr
		}
		publicErr := BatchImageProviderSubmitPublicError(err)
		reason := BatchImageProviderSubmitRecordCode(publicErr)
		_ = s.Repo.RecordBatchImageJobSubmitFailure(ctx, job.BatchID, reason, SanitizeBatchImagePublicMessage(err.Error()), true)
		s.HidePreUpstreamSubmitFailure(ctx, owner, job)
		return nil, publicErr
	}
	if providerJob == nil || strings.TrimSpace(providerJob.ProviderJobName) == "" {
		if releaseErr := s.ReleaseFailedSubmitHold(ctx, job, requestHash); releaseErr != nil {
			return nil, releaseErr
		}
		_ = s.Repo.RecordBatchImageJobSubmitFailure(ctx, job.BatchID, "PROVIDER_SUBMIT_FAILED", "provider job name missing", true)
		s.HidePreUpstreamSubmitFailure(ctx, owner, job)
		return nil, ErrBatchImageProviderSubmitFailed
	}

	if err := s.Repo.UpdateBatchImageJobProviderSubmit(ctx, UpdateBatchImageJobProviderSubmitParams{
		BatchID:           job.BatchID,
		ProviderJobName:   providerJob.ProviderJobName,
		ProviderInputRef:  providerJob.ProviderInputRef,
		ProviderOutputRef: providerJob.ProviderOutputRef,
		GCSInputURI:       BatchImageGCSRef(provider.Name(), providerJob.ProviderInputRef),
		GCSOutputURI:      BatchImageGCSRef(provider.Name(), providerJob.ProviderOutputRef),
		EventPayload:      map[string]any{"provider": provider.Name()},
	}); err != nil {
		// job 可能已被恢复扫描转 failed 并退款：上游批任务已创建成功，
		// 必须尽力取消并清理输入，否则上游照常产生成本（孤儿任务）。
		s.AbortOrphanProviderJob(ctx, provider, job, providerJob)
		return nil, err
	}

	if s.Queue != nil {
		if err := s.Queue.Enqueue(ctx, job.BatchID); err != nil && !errors.Is(err, ErrBatchImageAlreadyQueued) {
			_ = s.Repo.RecordBatchImageJobSubmitFailure(ctx, job.BatchID, "QUEUE_FAILED", SanitizeBatchImagePublicMessage(err.Error()), false)
			return nil, ErrBatchImageQueueFailed
		}
	}

	created, err := s.Repo.GetBatchImageJobByBatchID(ctx, job.BatchID)
	if err != nil {
		return nil, err
	}
	return BatchImageJobToPublic(created), nil
}

// BatchImageBillingHoldFailureCode 将冻结失败映射为可诊断且稳定的任务错误码。
func BatchImageBillingHoldFailureCode(err error) string {
	switch {
	case errors.Is(err, ErrBatchImageInsufficientBalance):
		return "INSUFFICIENT_BALANCE"
	case errors.Is(err, billing.ErrPreferredSubscriptionInvalid):
		return "PREFERRED_SUBSCRIPTION_INVALID"
	case errors.Is(err, billing.ErrPreferredSubscriptionGroup):
		return "PREFERRED_SUBSCRIPTION_GROUP_NOT_ALLOWED"
	case errors.Is(err, billing.ErrPreferredSubscriptionInsufficient):
		return "PREFERRED_SUBSCRIPTION_EXHAUSTED"
	default:
		return "BILLING_HOLD_FAILED"
	}
}
func (s *Public) ReleaseFailedSubmitHold(ctx context.Context, job *BatchImageJob, requestHash string) error {
	if err := s.Funding.Release(ctx, job, requestHash); err != nil {
		_ = s.Repo.RecordBatchImageJobSubmitFailure(ctx, job.BatchID, "BILLING_RELEASE_FAILED", SanitizeBatchImagePublicMessage(err.Error()), true)
		s.EnqueueBillingRetry(ctx, job.BatchID)
		return ErrBatchImageBillingHoldFailed
	}
	s.InvalidateAuthCache(ctx, BillingUserID(job))
	return nil
}

// RunSubmitHeartbeat 在 provider.Submit 期间周期性刷新 job 的 updated_at，
// 使 stale 恢复扫描能区分"仍在慢提交"与"进程死亡后的滞留"。
func (s *Public) RunSubmitHeartbeat(ctx context.Context, batchID string, done chan<- struct{}) {
	defer close(done)
	interval := s.SubmitHeartbeatInterval()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.Repo.TouchBatchImageJobSubmitting(ctx, batchID); err != nil && ctx.Err() == nil {
				s.warn("batch_image.submit_heartbeat_failed",
					"batch_id", batchID,
					"error", err,
				)
			}
		}
	}
}
func (s *Public) SubmitHeartbeatInterval() time.Duration {
	staleAfter := 10 * time.Minute
	if s != nil && s.Options.StaleActiveAfterSeconds > 0 {
		staleAfter = time.Duration(s.Options.StaleActiveAfterSeconds) * time.Second
	}
	interval := staleAfter / 3
	if interval < 15*time.Second {
		interval = 15 * time.Second
	}
	return interval
}

// AbortOrphanProviderJob 在上游任务创建成功但本地状态推进失败时，
// 尽力取消上游批任务并清理已上传的输入文件，避免孤儿任务持续产生成本。
func (s *Public) AbortOrphanProviderJob(ctx context.Context, provider ExecutionProvider, job *BatchImageJob, providerJob *BatchProviderJob) {
	if s == nil || provider == nil || job == nil || providerJob == nil {
		return
	}
	orphan := *job
	orphan.ProviderJobName = BatchImageOptionalStringPtr(providerJob.ProviderJobName)
	orphan.ProviderInputRef = BatchImageOptionalStringPtr(providerJob.ProviderInputRef)
	orphan.GCSInputURI = BatchImageOptionalStringPtr(BatchImageGCSRef(provider.Name(), providerJob.ProviderInputRef))
	if err := provider.Cancel(ctx, &orphan); err != nil {
		s.warn("batch_image.orphan_provider_job_cancel_failed",
			"batch_id", job.BatchID,
			"provider", provider.Name(),
			"error", err,
		)
	}
	if err := provider.Cleanup(ctx, &orphan, CleanupTargetInput); err != nil {
		s.warn("batch_image.orphan_provider_job_cleanup_failed",
			"batch_id", job.BatchID,
			"provider", provider.Name(),
			"error", err,
		)
	}
	if err := s.Repo.AppendBatchImageEvent(ctx, job.BatchID, "provider_job_aborted_after_submit", map[string]any{
		"batch_id": job.BatchID,
		"provider": provider.Name(),
	}); err != nil {
		s.warn("batch_image.orphan_provider_job_event_failed",
			"batch_id", job.BatchID,
			"error", err,
		)
	}
}
func (s *Public) CreatePendingItems(ctx context.Context, batchID, requestHash string, items []BatchImageSubmitItem) error {
	if s == nil || s.Repo == nil || len(items) == 0 {
		return nil
	}
	params := make([]CreateBatchImageItemParams, 0, len(items))
	for _, item := range items {
		preview := TruncateBatchImageMessage(item.Prompt, s.MaxPromptChars())
		params = append(params, CreateBatchImageItemParams{
			JobID:         batchID,
			CustomID:      item.CustomID,
			Status:        BatchImageItemStatusPending,
			RequestHash:   BatchImageStringPtr(requestHash),
			PromptPreview: BatchImageStringPtr(preview),
			ImageCount:    0,
		})
	}
	return s.Repo.BulkCreateBatchImageItems(ctx, params)
}
func (s *Public) EnqueueBillingRetry(ctx context.Context, batchID string) {
	if s == nil || s.Queue == nil {
		return
	}
	if err := s.Queue.Enqueue(ctx, batchID); err != nil && !errors.Is(err, ErrBatchImageAlreadyQueued) {
		s.warn("batch_image.billing_retry_enqueue_failed",
			"batch_id", batchID,
			"error", err,
		)
		if eventErr := s.Repo.AppendBatchImageEvent(ctx, batchID, "billing_retry_enqueue_failed", map[string]any{
			"batch_id": batchID,
			"error":    SanitizeBatchImagePublicMessage(err.Error()),
		}); eventErr != nil {
			s.warn("batch_image.billing_retry_event_failed",
				"batch_id", batchID,
				"error", eventErr,
			)
		}
	}
}
func (s *Public) HidePreUpstreamSubmitFailure(ctx context.Context, owner BatchImageOwner, job *BatchImageJob) {
	if s == nil || s.Repo == nil || job == nil || job.ProviderJobName != nil {
		return
	}
	if err := s.Repo.MarkBatchImageJobUserDeleted(ctx, owner.UserID, owner.APIKeyID, job.BatchID, s.now()); err != nil {
		s.warn("batch_image.hide_pre_upstream_failure_failed",
			"batch_id", job.BatchID,
			"error", err,
		)
	}
}
func (s *Public) Get(ctx context.Context, owner BatchImageOwner, batchID string) (*BatchImagePublicBatch, error) {
	job, err := s.Repo.GetBatchImageJobByBatchIDForOwner(ctx, owner.UserID, owner.APIKeyID, batchID)
	if err != nil {
		return nil, err
	}
	return BatchImageJobToPublic(job), nil
}
func (s *Public) List(ctx context.Context, owner BatchImageOwner, query BatchImageJobsQuery) (*BatchImagePublicListResponse, error) {
	filter := BatchImageJobFilter{Limit: query.Limit, Offset: ParseBatchImageCursor(query.Cursor), ExcludeDeleted: true}
	filter.TaskNameLike = strings.TrimSpace(query.TaskName)
	switch strings.TrimSpace(query.Status) {
	case "", "all":
	case "queued":
		filter.Status = BatchImageJobStatusSubmitted
	case "processing_results":
		filter.Status = BatchImageJobStatusIndexing
	case "completed":
		filter.Status = BatchImageJobStatusCompleted
	case "failed":
		filter.Status = BatchImageJobStatusFailed
	case "cancelled":
		filter.Status = BatchImageJobStatusCancelled
	case "output_deleted":
		filter.Status = BatchImageJobStatusOutputDeleted
	default:
		filter.Status = strings.TrimSpace(query.Status)
	}
	switch strings.TrimSpace(strings.ToLower(query.Downloaded)) {
	case "", "all":
	case "true", "1", "yes", "downloaded":
		downloaded := true
		filter.Downloaded = &downloaded
	case "false", "0", "no", "not_downloaded":
		downloaded := false
		filter.Downloaded = &downloaded
	default:
		return nil, ErrBatchImageInvalidItems
	}
	if from := ParseBatchImageListTime(query.From); from != nil {
		filter.CreatedAfter = from
	}
	if to := ParseBatchImageListTime(query.To); to != nil {
		filter.CreatedBefore = to
	}
	if filter.Limit <= 0 || filter.Limit > 100 {
		filter.Limit = 20
	}
	jobs, err := s.Repo.ListBatchImageJobsForOwner(ctx, owner.UserID, owner.APIKeyID, filter)
	if err != nil {
		return nil, err
	}
	data := make([]*BatchImagePublicBatch, 0, len(jobs))
	for _, job := range jobs {
		data = append(data, BatchImageJobToPublic(job))
	}
	return &BatchImagePublicListResponse{
		Object:  "list",
		Data:    data,
		HasMore: len(data) == filter.Limit,
	}, nil
}
func (s *Public) MarkDownloaded(ctx context.Context, owner BatchImageOwner, batchID string) error {
	job, err := s.Repo.GetBatchImageJobByBatchIDForOwner(ctx, owner.UserID, owner.APIKeyID, batchID)
	if err != nil {
		return err
	}
	return s.Repo.MarkBatchImageDownloaded(ctx, job.BatchID, s.now())
}
func (s *Public) DeleteRecord(ctx context.Context, owner BatchImageOwner, batchID string) error {
	job, err := s.Repo.GetBatchImageJobByBatchIDForOwner(ctx, owner.UserID, owner.APIKeyID, batchID)
	if err != nil {
		return err
	}
	if !IsBatchImageProcessorDoneStatus(job.Status) {
		return ErrBatchImageRecordDeleteNotReady
	}
	return s.Repo.MarkBatchImageJobUserDeleted(ctx, owner.UserID, owner.APIKeyID, job.BatchID, s.now())
}
func (s *Public) ListModels(ctx context.Context, owner BatchImageOwner) (*BatchImagePublicModelsResponse, error) {
	if !s.Enabled() {
		return nil, ErrBatchImageDisabled
	}
	if s.Pricing == nil {
		return nil, ErrBatchImageSettlementPricingMissing
	}
	if err := s.EnsureGroupAllowsBatchImage(ctx, owner.GroupID); err != nil {
		return nil, err
	}

	modelsByProvider := make(map[string]map[string]struct{})
	for _, providerName := range BatchImageProviderSelectionOrder("") {
		if !s.ProviderExists(providerName) {
			continue
		}
		accounts, err := s.ListCandidateAccounts(ctx, owner.GroupID, BatchImageProviderPlatform(providerName))
		if err != nil {
			return nil, err
		}
		for i := range accounts {
			account := accounts[i]
			if !account.IsSchedulable() || !account.SupportsProvider(providerName) {
				continue
			}
			for _, model := range BatchImageModelsFromAccountMapping(&account) {
				mapping, routingModel, err := s.ResolveBatchImageChannelModel(ctx, owner.GroupID, model)
				if err != nil {
					continue
				}
				upstreamModel := mappedCandidateModel(&account, routingModel)
				pricingModel := BatchImagePricingModel(mapping, model, routingModel, upstreamModel)
				if _, err := s.Pricing.BatchImageUnitPrice(ctx, BatchImagePriceInput{Model: pricingModel, GroupID: owner.GroupID, ImageSize: "1K"}); err != nil {
					continue
				}
				if !account.IsModelSupported(model) {
					continue
				}
				if modelsByProvider[providerName] == nil {
					modelsByProvider[providerName] = make(map[string]struct{})
				}
				modelsByProvider[providerName][model] = struct{}{}
			}
		}
	}

	out := make([]BatchImagePublicModel, 0)
	for _, providerName := range BatchImageProviderSelectionOrder("") {
		models := make([]string, 0, len(modelsByProvider[providerName]))
		for model := range modelsByProvider[providerName] {
			models = append(models, model)
		}
		sort.Strings(models)
		for _, model := range models {
			out = append(out, BatchImagePublicModel{
				ID:       model,
				Object:   "image.batch.model",
				Provider: providerName,
			})
		}
	}
	return &BatchImagePublicModelsResponse{Object: "list", Data: out}, nil
}
func (s *Public) ListItems(ctx context.Context, owner BatchImageOwner, batchID string, query BatchImageItemsQuery) (*BatchImagePublicItemsResponse, error) {
	filter := BatchImageItemFilter{Limit: query.Limit, Offset: ParseBatchImageCursor(query.Cursor)}
	switch strings.TrimSpace(query.Status) {
	case "", "all":
	case "succeeded", "success":
		filter.Status = BatchImageItemStatusSuccess
	case "pending":
		filter.Status = BatchImageItemStatusPending
	case "failed":
		filter.Status = BatchImageItemStatusFailed
	default:
		return nil, ErrBatchImageInvalidItems
	}
	if filter.Limit <= 0 || filter.Limit > 500 {
		filter.Limit = 100
	}
	items, err := s.Repo.ListBatchImageItemsForOwner(ctx, owner.UserID, owner.APIKeyID, batchID, filter)
	if err != nil {
		return nil, err
	}
	data := make([]BatchImagePublicItem, 0, len(items))
	for _, item := range items {
		data = append(data, BatchImageItemToPublic(item))
	}
	return &BatchImagePublicItemsResponse{
		Object:  "list",
		Data:    data,
		HasMore: len(data) == filter.Limit,
	}, nil
}
func (s *Public) Cancel(ctx context.Context, owner BatchImageOwner, batchID string) (*BatchImagePublicBatch, error) {
	job, err := s.Repo.GetBatchImageJobByBatchIDForOwner(ctx, owner.UserID, owner.APIKeyID, batchID)
	if err != nil {
		return nil, err
	}
	if IsBatchImageProcessorDoneStatus(job.Status) {
		if job.Status == BatchImageJobStatusFailed || job.Status == BatchImageJobStatusCancelled {
			if err := s.Funding.Release(ctx, job, BatchImageDerefString(job.RequestHash)); err != nil {
				s.EnqueueBillingRetry(ctx, job.BatchID)
				return nil, ErrBatchImageCancelFailed
			}
			s.InvalidateAuthCache(ctx, owner.EffectiveBillingUserID())
		}
		return BatchImageJobToPublic(job), nil
	}
	if job.ProviderJobName != nil && strings.TrimSpace(*job.ProviderJobName) != "" {
		if !s.ProviderExists(job.Provider) {
			return nil, ErrBatchImageUnsupportedProvider
		}
		if job.AccountID == nil {
			return nil, ErrBatchImageCancelFailed
		}
		account, err := s.AccountRepo.GetByID(ctx, *job.AccountID)
		if err != nil {
			return nil, ErrBatchImageCancelFailed
		}
		if err := account.Bind(job.Provider).Cancel(ctx, job); err != nil {
			return nil, ErrBatchImageCancelFailed
		}
		if eventErr := s.Repo.AppendBatchImageEvent(ctx, job.BatchID, "job_cancel_requested", map[string]any{"batch_id": job.BatchID}); eventErr != nil {
			s.warn("batch_image.cancel_event_failed",
				"batch_id", job.BatchID,
				"error", eventErr,
			)
		}
		if s.Queue != nil {
			if err := s.Queue.Enqueue(ctx, job.BatchID); err != nil && !errors.Is(err, ErrBatchImageAlreadyQueued) {
				return nil, ErrBatchImageCancelFailed
			}
		}
		updated, err := s.Repo.GetBatchImageJobByBatchIDForOwner(ctx, owner.UserID, owner.APIKeyID, batchID)
		if err != nil {
			return nil, err
		}
		return BatchImageJobToPublic(updated), nil
	}
	if err := s.Repo.TransitionBatchImageJobStatus(ctx, job.BatchID, BatchImageJobStatusCancelled, BatchImageTransitionOptions{
		EventType:    "job_cancelled",
		EventPayload: map[string]any{"batch_id": job.BatchID},
	}); err != nil {
		return nil, err
	}
	if err := s.Funding.Release(ctx, job, BatchImageDerefString(job.RequestHash)); err != nil {
		s.EnqueueBillingRetry(ctx, job.BatchID)
		return nil, ErrBatchImageCancelFailed
	}
	s.InvalidateAuthCache(ctx, owner.EffectiveBillingUserID())
	updated, err := s.Repo.GetBatchImageJobByBatchIDForOwner(ctx, owner.UserID, owner.APIKeyID, batchID)
	if err != nil {
		return nil, err
	}
	return BatchImageJobToPublic(updated), nil
}
func (s *Public) ValidateSubmitRequest(req BatchImageSubmitRequest) (BatchImageSubmitRequest, error) {
	req.Model = strings.TrimSpace(req.Model)
	req.TaskName = strings.TrimSpace(req.TaskName)
	req.ParentBatchID = strings.TrimSpace(req.ParentBatchID)
	req.Provider = strings.TrimSpace(req.Provider)
	req.ResponseMimeType = strings.TrimSpace(req.ResponseMimeType)
	req.AspectRatio = strings.TrimSpace(req.AspectRatio)
	req.ImageSize = strings.TrimSpace(req.ImageSize)
	if req.Model == "" {
		return req, ErrBatchImageInvalidModel
	}
	if req.TaskName == "" {
		req.TaskName = DefaultBatchImageTaskName(s.now())
	}
	if len(req.TaskName) > 255 {
		req.TaskName = TruncateBatchImageMessage(req.TaskName, 255)
	}
	if req.Provider != "" && !IsSupportedBatchImageProvider(req.Provider) {
		return req, ErrBatchImageUnsupportedProvider
	}
	if len(req.Items) == 0 {
		return req, ErrBatchImageInvalidItems
	}
	MaxItems := s.MaxItems()
	if len(req.Items) > MaxItems {
		return req, ErrBatchImageInvalidItems
	}
	if req.ResponseMimeType == "" {
		req.ResponseMimeType = s.DefaultResponseMimeType()
	}
	if req.ImageSize == "" {
		req.ImageSize = s.DefaultImageSize()
	}
	if !strings.EqualFold(req.ImageSize, DefaultBatchImageImageSize) {
		return req, ErrBatchImageInvalidItems
	}
	req.ImageSize = DefaultBatchImageImageSize
	req.Metadata = SanitizeBatchImageMetadata(req.Metadata)

	seen := make(map[string]struct{}, len(req.Items))
	totalReferenceImages := 0
	totalInlineReferenceBytes := 0
	totalOutputImages := 0
	expandedItems := make([]BatchImageSubmitItem, 0, len(req.Items))
	for i := range req.Items {
		req.Items[i].CustomID = strings.TrimSpace(req.Items[i].CustomID)
		if req.Items[i].CustomID == "" {
			req.Items[i].CustomID = fmt.Sprintf("item_%06d", i+1)
		}
		outputCount := req.Items[i].OutputCount
		if outputCount == 0 {
			outputCount = 1
		}
		if outputCount < 1 || outputCount > s.MaxOutputImagesPerItem() {
			return req, ErrBatchImageInvalidItems
		}
		totalOutputImages += outputCount
		if totalOutputImages > s.MaxOutputImagesPerJob() {
			return req, ErrBatchImageTooManyOutputImages
		}
		req.Items[i].Prompt = strings.TrimSpace(req.Items[i].Prompt)
		if req.Items[i].Prompt == "" {
			return req, ErrBatchImageInvalidItems
		}
		if len(req.Items[i].Prompt) > s.MaxPromptChars() {
			return req, ErrBatchImagePromptTooLong
		}
		referenceCount, inlineReferenceBytes, err := NormalizeBatchImageReferenceInputs(req.Model, &req.Items[i])
		if err != nil {
			return req, err
		}
		totalReferenceImages += referenceCount * outputCount
		if totalReferenceImages > s.MaxReferenceImagesPerJob() {
			return req, ErrBatchImageTooManyReferenceImages
		}
		totalInlineReferenceBytes += inlineReferenceBytes * outputCount
		if totalInlineReferenceBytes > s.MaxReferenceInlineBytesPerJob() {
			return req, ErrBatchImageReferenceImagesTooLarge
		}
		for repeatIndex := 1; repeatIndex <= outputCount; repeatIndex++ {
			expanded := req.Items[i]
			expanded.OutputCount = 0
			if outputCount > 1 {
				expanded.CustomID = fmt.Sprintf("%s_%0*d", req.Items[i].CustomID, BatchImageRepeatSuffixWidth(outputCount), repeatIndex)
			}
			if _, ok := seen[expanded.CustomID]; ok {
				return req, ErrBatchImageDuplicateCustomIDInRequest
			}
			seen[expanded.CustomID] = struct{}{}
			expandedItems = append(expandedItems, expanded)
		}
	}
	req.Items = expandedItems
	return req, nil
}
func NormalizeBatchImageReferenceInputs(model string, item *BatchImageSubmitItem) (int, int, error) {
	if item == nil || len(item.ReferenceImages) == 0 {
		return 0, 0, nil
	}
	maxRefs := MaxBatchImageReferenceImagesForModel(model)
	if maxRefs <= 0 || len(item.ReferenceImages) > maxRefs {
		return 0, 0, ErrBatchImageTooManyReferenceImages
	}
	out := make([]BatchImageReferenceInput, 0, len(item.ReferenceImages))
	inlineBytes := 0
	for _, ref := range item.ReferenceImages {
		ref.ID = TruncateBatchImageMessage(strings.TrimSpace(ref.ID), 80)
		ref.Type = TruncateBatchImageMessage(strings.TrimSpace(ref.Type), 40)
		ref.MimeType = NormalizeBatchImageReferenceMimeType(ref.MimeType)
		ref.FileURI = strings.TrimSpace(ref.FileURI)
		if ref.MimeType == "" {
			return 0, 0, ErrBatchImageInvalidReferenceImage
		}
		if len(ref.Data) == 0 && ref.FileURI == "" {
			return 0, 0, ErrBatchImageInvalidReferenceImage
		}
		if len(ref.Data) > 0 && ref.FileURI != "" {
			return 0, 0, ErrBatchImageInvalidReferenceImage
		}
		if len(ref.Data) > MaxBatchImageReferenceImageBytes {
			return 0, 0, ErrBatchImageInvalidReferenceImage
		}
		if ref.FileURI != "" && !strings.HasPrefix(ref.FileURI, "gs://") {
			return 0, 0, ErrBatchImageInvalidReferenceImage
		}
		inlineBytes += len(ref.Data)
		out = append(out, ref)
	}
	item.ReferenceImages = out
	return len(out), inlineBytes, nil
}
func BatchImageRepeatSuffixWidth(count int) int {
	if count < 10 {
		return 2
	}
	return len(strconv.Itoa(count))
}
func MaxBatchImageReferenceImagesForModel(model string) int {
	model = strings.ToLower(strings.TrimSpace(model))
	if strings.Contains(model, "pro-image") {
		return 14
	}
	if strings.Contains(model, "flash-image") {
		return 3
	}
	return 0
}

// ResolveBatchImageChannelModel 执行批量图片的渠道映射和渠道计费模型限制。
func (s *Public) ResolveBatchImageChannelModel(ctx context.Context, groupID *int64, requestedModel string) (ChannelMappingResult, string, error) {
	mapping := s.WithModelTrace(ctx, ChannelMappingResult{MappedModel: requestedModel}, requestedModel)
	if s == nil || s.ChannelService == nil || groupID == nil || *groupID <= 0 {
		return mapping, requestedModel, nil
	}
	mapping = s.WithModelTrace(ctx, s.ChannelService.ResolveChannelMapping(ctx, *groupID, requestedModel), requestedModel)
	routingModel := strings.TrimSpace(mapping.MappedModel)
	if routingModel == "" {
		routingModel = requestedModel
	}
	billingModel := routing.BillingModelForRestriction(mapping.BillingModelSource, requestedModel, routingModel)
	if billingModel != "" && s.ChannelService.IsModelRestricted(ctx, *groupID, billingModel) {
		return mapping, routingModel, ErrBatchImageNoAccountAvailable
	}
	return mapping, routingModel, nil
}

func BatchImagePricingModel(mapping ChannelMappingResult, requestedModel, channelMappedModel, upstreamModel string) string {
	return routing.BillingModelForPrice(mapping, requestedModel, channelMappedModel, upstreamModel)
}

// SelectProviderAndAccount 按渠道模型选择账号，并返回账号映射后的实际上游模型。
func (s *Public) SelectProviderAndAccount(
	ctx context.Context,
	owner BatchImageOwner,
	requestedProvider, routingModel string,
	mapping ChannelMappingResult,
) (ExecutionProvider, *Candidate, string, error) {
	providers := BatchImageProviderSelectionOrder(requestedProvider)
	for _, providerName := range providers {
		if !s.ProviderExists(providerName) {
			continue
		}
		accounts, err := s.ListCandidateAccounts(ctx, owner.GroupID, BatchImageProviderPlatform(providerName))
		if err != nil {
			return nil, nil, "", err
		}
		sort.SliceStable(accounts, func(i, j int) bool {
			if accounts[i].Priority != accounts[j].Priority {
				return accounts[i].Priority > accounts[j].Priority
			}
			return accounts[i].ID < accounts[j].ID
		})
		for i := range accounts {
			account := accounts[i]
			if !account.IsSchedulable() || !account.IsModelSupported(routingModel) {
				continue
			}
			if !account.ProtocolEnabled() {
				continue
			}
			if !account.SupportsProvider(providerName) {
				continue
			}
			upstreamModel := strings.TrimSpace(account.ResolveUpstream(ctx, routingModel))
			if upstreamModel == "" {
				continue
			}
			if owner.GroupID != nil && *owner.GroupID > 0 && s.ChannelService != nil &&
				mapping.BillingModelSource == BillingModelSourceUpstream &&
				s.ChannelService.IsModelRestricted(ctx, *owner.GroupID, upstreamModel) {
				continue
			}
			s.RegisterModel(ctx, upstreamModel)
			return account.Bind(providerName), &account, upstreamModel, nil
		}
	}
	if requestedProvider != "" {
		return nil, nil, "", ErrBatchImageNoAccountAvailable
	}
	return nil, nil, "", ErrBatchImageNoAccountAvailable
}
func (s *Public) ListCandidateAccounts(ctx context.Context, groupID *int64, platform string) ([]Candidate, error) {
	if s.AccountRepo == nil {
		return nil, ErrBatchImageNoAccountAvailable
	}
	if groupID != nil && *groupID > 0 {
		return s.AccountRepo.ListSchedulableByGroupIDAndPlatform(ctx, *groupID, platform)
	}
	return s.AccountRepo.ListSchedulableByPlatform(ctx, platform)
}
func (s *Public) EnsureGroupAllowsBatchImage(ctx context.Context, groupID *int64) error {
	if groupID == nil || *groupID <= 0 {
		return nil
	}
	if s.GroupRepo == nil {
		return ErrBatchImageSettlementPricingMissing
	}
	group, err := s.GroupRepo.GetByIDLite(ctx, *groupID)
	if err != nil || group == nil {
		return ErrBatchImageSettlementPricingMissing
	}
	if !group.AllowBatchImageGeneration {
		return ErrBatchImageGroupDisabled
	}
	if group.Platform != PlatformGemini {
		return ErrBatchImageGroupDisabled
	}
	return nil
}
func (s *Public) ResolvePricingSnapshot(ctx context.Context, owner BatchImageOwner, req BatchImageSubmitRequest, provider string, account *Candidate) (*BatchImagePricingSnapshot, error) {
	billingMode, ok := billing.NormalizeAPIKeyBillingMode(owner.BillingMode)
	if !ok {
		return nil, apikey.ErrInvalidAPIKeyBillingMode
	}
	if billingMode == billing.APIKeyBillingModeSubscription && (owner.PreferredSubscriptionID == nil || *owner.PreferredSubscriptionID <= 0) {
		return nil, apikey.ErrPreferredSubscriptionRequired
	}
	// 同一次提交复用同一份分组配置，避免并发修改时混用旧倍率与新价格。
	var pricingGroup *GroupView
	unit := -1.0
	groupMultiplier := 1.0
	subscriptionRateMultiplier := 1.0
	balanceRateMultiplier := 1.0
	planGroupRateEnabled := true
	discountMultiplier := DefaultBatchImageDiscountMultiplier
	holdMultiplier := DefaultBatchImageHoldMultiplier
	if owner.GroupID != nil && *owner.GroupID > 0 {
		if s.GroupRepo == nil {
			return nil, ErrBatchImageSettlementPricingMissing
		}
		group, err := s.GroupRepo.GetByIDLite(ctx, *owner.GroupID)
		if err != nil || group == nil {
			return nil, ErrBatchImageSettlementPricingMissing
		}
		pricingGroup = group
		if !group.AllowBatchImageGeneration {
			return nil, ErrBatchImageGroupDisabled
		}
		groupDefaultMultiplier := group.RateMultiplier
		if groupDefaultMultiplier < 0 {
			groupDefaultMultiplier = 0
		}
		subscriptionRateMultiplier = groupDefaultMultiplier
		balanceRateMultiplier = groupDefaultMultiplier
		if s.UserGroupRateRepo != nil {
			userRate, rateErr := s.UserGroupRateRepo.GetByUserAndGroup(ctx, owner.EffectiveBillingUserID(), group.ID)
			if rateErr != nil {
				return nil, ErrBatchImageSettlementPricingMissing
			}
			if userRate != nil {
				balanceRateMultiplier = *userRate
			}
		}
		effectiveGroupMultiplier := balanceRateMultiplier
		// 自动模式沿用套餐优先；指定订阅只读取该订阅，余额模式完全跳过套餐。
		var subscription *billing.UserSubscription
		switch billingMode {
		case billing.APIKeyBillingModeSubscription:
			subscription = s.PreferredSubscription(
				ctx,
				owner.EffectiveBillingUserID(),
				*owner.PreferredSubscriptionID,
				owner.GroupID,
			)
			if subscription == nil {
				return nil, billing.ErrPreferredSubscriptionInvalid
			}
		case billing.APIKeyBillingModeAuto:
			subscription = s.AutoSubscription(
				ctx,
				owner.EffectiveBillingUserID(),
				owner.GroupID,
			)
		}
		if subscription != nil {
			effectiveGroupMultiplier = s.SubscriptionMultiplier(ctx, owner, group, groupDefaultMultiplier, subscription)
		}
		groupMultiplier = effectiveGroupMultiplier
		if groupMultiplier < 0 {
			groupMultiplier = 0
		}
		discountMultiplier = group.BatchImageDiscountMultiplier
		if discountMultiplier < 0 {
			discountMultiplier = 0
		}
		if group.BatchImageHoldMultiplier >= 0 {
			holdMultiplier = group.BatchImageHoldMultiplier
		}
	}
	if unit < 0 {
		if s.Pricing == nil {
			return nil, ErrBatchImageSettlementPricingMissing
		}
		resolvedUnit, err := s.Pricing.BatchImageUnitPrice(ctx, BatchImagePriceInput{Model: req.Model, GroupID: owner.GroupID, Group: pricingGroup, ImageSize: req.ImageSize})
		if err != nil || resolvedUnit < 0 {
			return nil, ErrBatchImageSettlementPricingMissing
		}
		unit = resolvedUnit
	}
	// 定价不变式：hold 比例不得低于 discount 比例，否则成功率足够高时
	// actualCost > holdAmount，结算永远失败、冻结余额无法解冻。
	// 管理端已校验新配置，此处兜底钳制存量脏数据。
	if holdMultiplier < discountMultiplier {
		s.warn("batch_image.hold_multiplier_below_discount_clamped",
			"hold_multiplier", holdMultiplier,
			"discount_multiplier", discountMultiplier,
		)
		holdMultiplier = discountMultiplier
	}
	accountMultiplier := 1.0
	if account != nil {
		accountMultiplier = account.BillingRateMultiplier()
	}
	if accountMultiplier < 0 {
		accountMultiplier = 0
	}
	standardUnitPrice := unit * groupMultiplier * accountMultiplier
	billableUnitPrice := standardUnitPrice * discountMultiplier
	holdUnitPrice := standardUnitPrice * holdMultiplier
	return &BatchImagePricingSnapshot{
		BaseUnitPrice:              unit,
		GroupRateMultiplier:        groupMultiplier,
		SubscriptionRateMultiplier: subscriptionRateMultiplier,
		BalanceRateMultiplier:      balanceRateMultiplier,
		PlanGroupRateEnabled:       planGroupRateEnabled,
		AccountRateMultiplier:      accountMultiplier,
		BatchDiscountMultiplier:    discountMultiplier,
		HoldMultiplier:             holdMultiplier,
		BillableUnitPrice:          billableUnitPrice,
		HoldUnitPrice:              holdUnitPrice,
		EstimatedCost:              billableUnitPrice * float64(len(req.Items)),
		HoldAmount:                 holdUnitPrice * float64(len(req.Items)),
	}, nil
}
func (s *Public) Enabled() bool {
	return s != nil && s.Repo != nil && s.AccountRepo != nil && s.Options.Enabled
}
func (s *Public) InvalidateAuthCache(ctx context.Context, userID int64) {
	if s != nil && s.InvalidateAuth != nil && userID > 0 {
		s.InvalidateAuth(ctx, userID)
	}
}
func (s *Public) MaxItems() int {
	if s != nil && s.Options.MaxItemsPerJobDefault > 0 {
		return s.Options.MaxItemsPerJobDefault
	}
	return DefaultBatchImageMaxItems
}
func (s *Public) MaxOutputImagesPerJob() int {
	if s != nil && s.Options.MaxOutputImagesPerJob > 0 {
		return s.Options.MaxOutputImagesPerJob
	}
	return DefaultBatchImageMaxOutputImages
}
func (s *Public) MaxOutputImagesPerItem() int {
	if s != nil && s.Options.MaxOutputImagesPerItem > 0 {
		return s.Options.MaxOutputImagesPerItem
	}
	return DefaultBatchImageMaxOutputCount
}
func (s *Public) MaxPromptChars() int {
	if s != nil && s.Options.MaxPromptCharsPerItem > 0 {
		return s.Options.MaxPromptCharsPerItem
	}
	return DefaultBatchImageMaxPromptChars
}
func (s *Public) MaxReferenceImagesPerJob() int {
	if s != nil && s.Options.MaxReferenceImagesPerJob > 0 {
		return s.Options.MaxReferenceImagesPerJob
	}
	return DefaultBatchImageMaxReferenceImages
}
func (s *Public) MaxReferenceInlineBytesPerJob() int {
	if s != nil && s.Options.MaxReferenceInlineBytesPerJob > 0 {
		return s.Options.MaxReferenceInlineBytesPerJob
	}
	return DefaultBatchImageMaxReferenceBytes
}
func (s *Public) DefaultResponseMimeType() string {
	if s != nil && strings.TrimSpace(s.Options.DefaultResponseMimeType) != "" {
		return strings.TrimSpace(s.Options.DefaultResponseMimeType)
	}
	return DefaultBatchImageResponseMime
}
func (s *Public) DefaultImageSize() string {
	if s != nil && strings.TrimSpace(s.Options.DefaultImageSize) != "" {
		return strings.TrimSpace(s.Options.DefaultImageSize)
	}
	return DefaultBatchImageImageSize
}
func HashBatchImageSubmitRequest(req BatchImageSubmitRequest) string {
	req.Metadata = SanitizeBatchImageMetadata(req.Metadata)
	b, _ := json.Marshal(req)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// HashCompositeBatchImageSubmitRequest 将复合路由分组纳入幂等指纹，前缀大小写不改变请求身份。
func HashCompositeBatchImageSubmitRequest(req BatchImageSubmitRequest, groupID *int64) string {
	payload := struct {
		Request BatchImageSubmitRequest `json:"request"`
		GroupID *int64                  `json:"group_id"`
	}{
		Request: req,
		GroupID: CloneInt64Ptr(groupID),
	}
	payload.Request.Metadata = SanitizeBatchImageMetadata(payload.Request.Metadata)
	b, _ := json.Marshal(payload)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
func BatchImageProviderPlatform(provider string) string {
	switch provider {
	case BatchImageProviderGeminiAPI, BatchImageProviderVertex:
		return PlatformGemini
	default:
		return PlatformGemini
	}
}
func BatchImageProviderSelectionOrder(requestedProvider string) []string {
	if strings.TrimSpace(requestedProvider) != "" {
		return []string{strings.TrimSpace(requestedProvider)}
	}
	return []string{BatchImageProviderGeminiAPI, BatchImageProviderVertex}
}
func BatchImageModelsFromAccountMapping(account *Candidate) []string {
	if account == nil {
		return nil
	}
	mapping := account.GetModelMapping()
	if len(mapping) == 0 {
		return nil
	}
	models := make(map[string]struct{})
	for model := range mapping {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if strings.ContainsAny(model, "*?") {
			for _, candidate := range DefaultBatchImageModelCandidates() {
				if modelmap.Matches(model, candidate) {
					models[candidate] = struct{}{}
				}
			}
			continue
		}
		models[model] = struct{}{}
	}
	out := make([]string, 0, len(models))
	for model := range models {
		out = append(out, model)
	}
	sort.Strings(out)
	return out
}
func BatchImageProviderSubmitPublicError(err error) error {
	reason := strings.TrimSpace(infraerrors.Reason(err))
	switch reason {
	case "VERTEX_MANAGED_GCS_BUCKET_MISSING":
		return ErrBatchImageVertexGCSBucketMissing
	case "BATCH_IMAGE_PROVIDER_MISSING_API_KEY":
		return ErrBatchImageProviderMissingAPIKey
	case "BATCH_IMAGE_PROVIDER_MISSING_SERVICE_ACCOUNT":
		return ErrBatchImageProviderMissingServiceAccount
	case "BATCH_IMAGE_PROVIDER_UNSUPPORTED_ACCOUNT":
		return ErrBatchImageProviderUnsupportedAccount
	default:
		return ErrBatchImageProviderSubmitFailed
	}
}
func BatchImageProviderSubmitRecordCode(err error) string {
	reason := strings.TrimSpace(infraerrors.Reason(err))
	if reason == "" || reason == "BATCH_IMAGE_PROVIDER_SUBMIT_FAILED" {
		return "PROVIDER_SUBMIT_FAILED"
	}
	return reason
}
func ParseBatchImageListTime(raw string) *time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if unix, err := strconv.ParseInt(raw, 10, 64); err == nil && unix > 0 {
		t := time.Unix(unix, 0)
		return &t
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return &t
	}
	if t, err := time.Parse("2006-01-02", raw); err == nil {
		return &t
	}
	return nil
}
func SanitizeBatchImageMetadata(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make(map[string]string, len(keys))
	for _, k := range keys {
		key := strings.TrimSpace(k)
		if key == "" || len(key) > 64 {
			continue
		}
		value := strings.TrimSpace(in[k])
		if len(value) > 256 {
			value = value[:256]
		}
		out[key] = value
		if len(out) >= 20 {
			break
		}
	}
	return out
}
func ParseBatchImageCursor(cursor string) int {
	offset, err := strconv.Atoi(strings.TrimSpace(cursor))
	if err != nil || offset < 0 {
		return 0
	}
	return offset
}

// now 保持各原取时点，构造时可注入同一时钟来源。
func (s *Public) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
