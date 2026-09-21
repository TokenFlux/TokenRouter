//go:build unit

package batchimage_test

import (
	"context"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/batchimage"
)

type fakeBatchImageRepository struct {
	jobs          map[string]*batchimage.BatchImageJob
	items         map[string][]batchimage.CreateBatchImageItemParams
	counts        map[string]batchimage.BatchImageCounts
	transitions   map[string][]string
	events        map[string][]string
	transitionErr error
	replaceCalls  int
}

func newFakeBatchImageRepository() *fakeBatchImageRepository {
	return &fakeBatchImageRepository{
		jobs:        make(map[string]*batchimage.BatchImageJob),
		items:       make(map[string][]batchimage.CreateBatchImageItemParams),
		counts:      make(map[string]batchimage.BatchImageCounts),
		transitions: make(map[string][]string),
		events:      make(map[string][]string),
	}
}

func (r *fakeBatchImageRepository) CreateBatchImageJob(_ context.Context, params batchimage.CreateBatchImageJobParams) (*batchimage.BatchImageJob, error) {
	job := &batchimage.BatchImageJob{
		BatchID:                     params.BatchID,
		UserID:                      params.UserID,
		APIKeyID:                    params.APIKeyID,
		AccountID:                   params.AccountID,
		GroupID:                     params.GroupID,
		Status:                      params.Status,
		Provider:                    params.Provider,
		Model:                       params.Model,
		RequestedModel:              params.RequestedModel,
		InternalModel:               params.InternalModel,
		TaskName:                    params.TaskName,
		ProviderJobName:             params.ProviderJobName,
		ItemCount:                   params.ItemCount,
		EstimatedCost:               params.EstimatedCost,
		HoldAmount:                  params.HoldAmount,
		BalanceHoldAmount:           params.BalanceHoldAmount,
		SubscriptionHoldAllocations: cloneResultAllocations(params.SubscriptionHoldAllocations),
		SubscriptionRateMultiplier:  params.SubscriptionRateMultiplier,
		BalanceRateMultiplier:       params.BalanceRateMultiplier,
		PlanGroupRateEnabled:        params.PlanGroupRateEnabled,
		HoldID:                      params.HoldID,
		BaseUnitPrice:               params.BaseUnitPrice,
		GroupRateMultiplier:         params.GroupRateMultiplier,
		AccountRateMultiplier:       params.AccountRateMultiplier,
		BatchDiscountMultiplier:     params.BatchDiscountMultiplier,
		HoldMultiplier:              params.HoldMultiplier,
		BillableUnitPrice:           params.BillableUnitPrice,
		HoldUnitPrice:               params.HoldUnitPrice,
		PricingSnapshotVersion:      params.PricingSnapshotVersion,
		Currency:                    params.Currency,
		IdempotencyKey:              params.IdempotencyKey,
		RequestHash:                 params.RequestHash,
		SessionID:                   params.SessionID,
		CreatedAt:                   time.Now(),
	}
	r.jobs[job.BatchID] = job
	return job, nil
}

func (r *fakeBatchImageRepository) GetBatchImageJobByBatchID(_ context.Context, batchID string) (*batchimage.BatchImageJob, error) {
	job, ok := r.jobs[batchID]
	if !ok {
		return nil, batchimage.ErrBatchImageJobNotFound
	}
	return job, nil
}

func (r *fakeBatchImageRepository) GetBatchImageJobByIdempotencyKey(_ context.Context, userID, apiKeyID int64, key string) (*batchimage.BatchImageJob, error) {
	for _, job := range r.jobs {
		if job.UserID == userID && job.APIKeyID != nil && *job.APIKeyID == apiKeyID && batchimage.BatchImageDerefString(job.IdempotencyKey) == key {
			return job, nil
		}
	}
	return nil, batchimage.ErrBatchImageJobNotFound
}

func (r *fakeBatchImageRepository) GetBatchImageJobByBatchIDForOwner(_ context.Context, userID, apiKeyID int64, batchID string) (*batchimage.BatchImageJob, error) {
	job, ok := r.jobs[batchID]
	if !ok || job.UserID != userID || job.APIKeyID == nil || *job.APIKeyID != apiKeyID {
		return nil, batchimage.ErrBatchImageJobNotFound
	}
	return job, nil
}

func (r *fakeBatchImageRepository) ListBatchImageJobsForOwner(_ context.Context, userID, apiKeyID int64, filter batchimage.BatchImageJobFilter) ([]*batchimage.BatchImageJob, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	var jobs []*batchimage.BatchImageJob
	for _, job := range r.jobs {
		if job.UserID != userID || job.APIKeyID == nil || *job.APIKeyID != apiKeyID {
			continue
		}
		if filter.Status != "" && job.Status != filter.Status {
			continue
		}
		if filter.TaskNameLike != "" && !strings.Contains(strings.ToLower(job.TaskName), strings.ToLower(filter.TaskNameLike)) {
			continue
		}
		if filter.ExcludeDeleted && job.UserDeletedAt != nil {
			continue
		}
		if filter.Downloaded != nil {
			downloaded := job.DownloadedAt != nil
			if downloaded != *filter.Downloaded {
				continue
			}
		}
		if filter.CreatedAfter != nil && job.CreatedAt.Before(*filter.CreatedAfter) {
			continue
		}
		if filter.CreatedBefore != nil && !job.CreatedAt.Before(*filter.CreatedBefore) {
			continue
		}
		if offset > 0 {
			offset--
			continue
		}
		jobs = append(jobs, job)
		if len(jobs) >= limit {
			break
		}
	}
	return jobs, nil
}

func (r *fakeBatchImageRepository) GetBatchImageJobByID(_ context.Context, id int64) (*batchimage.BatchImageJob, error) {
	for _, job := range r.jobs {
		if job.ID == id {
			return job, nil
		}
	}
	return nil, batchimage.ErrBatchImageJobNotFound
}

func (r *fakeBatchImageRepository) TransitionBatchImageJobStatus(_ context.Context, batchID, toStatus string, opts batchimage.BatchImageTransitionOptions) error {
	job, ok := r.jobs[batchID]
	if !ok {
		return batchimage.ErrBatchImageJobNotFound
	}
	if !batchimage.CanTransitionBatchImageJob(job.Status, toStatus) {
		return batchimage.ErrBatchImageInvalidTransition
	}
	if r.transitionErr != nil {
		return r.transitionErr
	}
	job.Status = toStatus
	job.LastErrorCode = opts.ErrorCode
	job.LastErrorMessage = opts.ErrorMessage
	r.transitions[batchID] = append(r.transitions[batchID], toStatus)
	if opts.EventType != "" {
		r.events[batchID] = append(r.events[batchID], opts.EventType)
	}
	return nil
}

func (r *fakeBatchImageRepository) TouchBatchImageJobSubmitting(_ context.Context, batchID string) error {
	job, ok := r.jobs[batchID]
	if !ok {
		return batchimage.ErrBatchImageJobNotFound
	}
	if job.Status == batchimage.BatchImageJobStatusCreated || job.Status == batchimage.BatchImageJobStatusUploading {
		job.UpdatedAt = time.Now()
	}
	return nil
}

func (r *fakeBatchImageRepository) FailStaleUnsubmittedBatchImageJob(_ context.Context, batchID string, cutoff time.Time, code, message string) (bool, error) {
	job, ok := r.jobs[batchID]
	if !ok {
		return false, batchimage.ErrBatchImageJobNotFound
	}
	if job.Status != batchimage.BatchImageJobStatusCreated && job.Status != batchimage.BatchImageJobStatusUploading {
		return false, nil
	}
	if batchimage.BatchImageDerefString(job.ProviderJobName) != "" || job.UpdatedAt.After(cutoff) {
		return false, nil
	}
	job.Status = batchimage.BatchImageJobStatusFailed
	job.LastErrorCode = batchimage.BatchImageStringPtr(code)
	job.LastErrorMessage = batchimage.BatchImageStringPtr(message)
	job.UpdatedAt = time.Now()
	r.transitions[batchID] = append(r.transitions[batchID], batchimage.BatchImageJobStatusFailed)
	r.events[batchID] = append(r.events[batchID], "billing_hold_recovery_failed_unsubmitted")
	return true, nil
}

func (r *fakeBatchImageRepository) UpdateBatchImageJobProviderOutputRef(_ context.Context, batchID, providerOutputRef string) error {
	job, ok := r.jobs[batchID]
	if !ok {
		return batchimage.ErrBatchImageJobNotFound
	}
	job.ProviderOutputRef = &providerOutputRef
	return nil
}

func (r *fakeBatchImageRepository) UpdateBatchImageJobProviderSubmit(_ context.Context, params batchimage.UpdateBatchImageJobProviderSubmitParams) error {
	job, ok := r.jobs[params.BatchID]
	if !ok {
		return batchimage.ErrBatchImageJobNotFound
	}
	if !batchimage.CanTransitionBatchImageJob(job.Status, batchimage.BatchImageJobStatusSubmitted) {
		return batchimage.ErrBatchImageInvalidTransition
	}
	job.Status = batchimage.BatchImageJobStatusSubmitted
	job.ProviderJobName = batchimage.BatchImageOptionalStringPtr(params.ProviderJobName)
	job.ProviderInputRef = batchimage.BatchImageOptionalStringPtr(params.ProviderInputRef)
	job.ProviderOutputRef = batchimage.BatchImageOptionalStringPtr(params.ProviderOutputRef)
	job.GCSInputURI = batchimage.BatchImageOptionalStringPtr(params.GCSInputURI)
	job.GCSOutputURI = batchimage.BatchImageOptionalStringPtr(params.GCSOutputURI)
	now := time.Now()
	job.SubmittedAt = &now
	r.transitions[params.BatchID] = append(r.transitions[params.BatchID], batchimage.BatchImageJobStatusSubmitted)
	r.events[params.BatchID] = append(r.events[params.BatchID], "provider_submitted")
	return nil
}

func (r *fakeBatchImageRepository) RecordBatchImageJobSubmitFailure(_ context.Context, batchID, code, message string, markFailed bool) error {
	job, ok := r.jobs[batchID]
	if !ok {
		return batchimage.ErrBatchImageJobNotFound
	}
	if markFailed {
		job.Status = batchimage.BatchImageJobStatusFailed
	}
	job.LastErrorCode = batchimage.BatchImageOptionalStringPtr(code)
	job.LastErrorMessage = batchimage.BatchImageOptionalStringPtr(message)
	eventType := "submit_failed"
	if !markFailed {
		eventType = "queue_failed"
	}
	r.events[batchID] = append(r.events[batchID], eventType)
	return nil
}

func (r *fakeBatchImageRepository) MarkBatchImageJobSettled(_ context.Context, params batchimage.MarkBatchImageJobSettledParams) error {
	job, ok := r.jobs[params.BatchID]
	if !ok {
		return batchimage.ErrBatchImageJobNotFound
	}
	if job.Status != batchimage.BatchImageJobStatusSettling {
		if job.Status == batchimage.BatchImageJobStatusCompleted {
			return batchimage.ErrBatchImageAlreadySettled
		}
		return batchimage.ErrBatchImageSettlementInvalidStatus
	}
	if batchimage.BatchImageDerefString(job.ManifestHash) != "" && batchimage.BatchImageDerefString(job.ManifestHash) != params.ManifestHash {
		return batchimage.ErrBatchImageSettlementManifestConflict
	}
	now := time.Now()
	job.Status = batchimage.BatchImageJobStatusCompleted
	job.ActualCost = &params.ActualCost
	job.ManifestHash = &params.ManifestHash
	job.SettledAt = &now
	if job.OutputExpiresAt == nil && params.OutputExpiresAt != nil {
		job.OutputExpiresAt = params.OutputExpiresAt
	}
	r.transitions[params.BatchID] = append(r.transitions[params.BatchID], batchimage.BatchImageJobStatusCompleted)
	r.events[params.BatchID] = append(r.events[params.BatchID], "settlement_completed")
	return nil
}

func (r *fakeBatchImageRepository) SetBatchImageJobSettlementFailed(_ context.Context, batchID, code, message string) (int, error) {
	job, ok := r.jobs[batchID]
	if !ok {
		return 0, batchimage.ErrBatchImageJobNotFound
	}
	job.LastErrorCode = batchimage.BatchImageStringPtr(code)
	job.LastErrorMessage = batchimage.BatchImageOptionalStringPtr(message)
	job.RetryCount++
	r.events[batchID] = append(r.events[batchID], "settlement_failed")
	return job.RetryCount, nil
}

func (r *fakeBatchImageRepository) CreateBatchImageItem(_ context.Context, params batchimage.CreateBatchImageItemParams) (*batchimage.BatchImageItem, error) {
	r.items[params.JobID] = append(r.items[params.JobID], params)
	return &batchimage.BatchImageItem{JobID: params.JobID, CustomID: params.CustomID, Status: params.Status}, nil
}

func (r *fakeBatchImageRepository) BulkCreateBatchImageItems(ctx context.Context, params []batchimage.CreateBatchImageItemParams) error {
	for _, param := range params {
		if _, err := r.CreateBatchImageItem(ctx, param); err != nil {
			return err
		}
	}
	return nil
}

func (r *fakeBatchImageRepository) ReplaceBatchImageItemsForJob(_ context.Context, batchID string, items []batchimage.CreateBatchImageItemParams, counts batchimage.BatchImageCounts) error {
	// 与真实实现一致：仅 indexing 状态允许重建 item 表（未注册的 job 保持宽松，
	// 供直接构造 job 的单测使用）。
	if job, ok := r.jobs[batchID]; ok && job.Status != batchimage.BatchImageJobStatusIndexing {
		return batchimage.ErrBatchImageIndexStateConflict
	}
	r.replaceCalls++
	copied := append([]batchimage.CreateBatchImageItemParams(nil), items...)
	for idx := range copied {
		copied[idx].JobID = batchID
	}
	r.items[batchID] = copied
	r.counts[batchID] = counts
	if job, ok := r.jobs[batchID]; ok {
		job.SuccessCount = counts.SuccessCount
		job.FailCount = counts.FailCount
		job.ItemCount = len(copied)
	}
	return nil
}

func (r *fakeBatchImageRepository) ListBatchImageItems(_ context.Context, batchID string, filter batchimage.BatchImageItemFilter) ([]*batchimage.BatchImageItem, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	var result []*batchimage.BatchImageItem
	for _, item := range r.items[batchID] {
		if filter.Status != "" && item.Status != filter.Status {
			continue
		}
		if offset > 0 {
			offset--
			continue
		}
		result = append(result, &batchimage.BatchImageItem{
			JobID:                item.JobID,
			CustomID:             item.CustomID,
			Status:               item.Status,
			RequestHash:          item.RequestHash,
			PromptPreview:        item.PromptPreview,
			ProviderSourceObject: item.ProviderSourceObject,
			SourceLineNumber:     item.SourceLineNumber,
			SourceByteOffset:     item.SourceByteOffset,
			SourceByteLength:     item.SourceByteLength,
			MimeType:             item.MimeType,
			FileExtension:        item.FileExtension,
			ImageCount:           item.ImageCount,
			ErrorCode:            item.ErrorCode,
			ErrorMessage:         item.ErrorMessage,
			BilledAmount:         item.BilledAmount,
			IndexedAt:            item.IndexedAt,
		})
		if len(result) >= limit {
			break
		}
	}
	return result, nil
}

func (r *fakeBatchImageRepository) ListBatchImageItemsForOwner(ctx context.Context, userID, apiKeyID int64, batchID string, filter batchimage.BatchImageItemFilter) ([]*batchimage.BatchImageItem, error) {
	if _, err := r.GetBatchImageJobByBatchIDForOwner(ctx, userID, apiKeyID, batchID); err != nil {
		return nil, err
	}
	return r.ListBatchImageItems(ctx, batchID, filter)
}

func (r *fakeBatchImageRepository) GetBatchImageJobForDownload(ctx context.Context, userID, apiKeyID int64, batchID string) (*batchimage.BatchImageJob, error) {
	return r.GetBatchImageJobByBatchIDForOwner(ctx, userID, apiKeyID, batchID)
}

func (r *fakeBatchImageRepository) GetBatchImageItemForDownload(_ context.Context, batchID, customID string) (*batchimage.BatchImageItem, error) {
	for _, item := range r.items[batchID] {
		if item.CustomID != customID {
			continue
		}
		return &batchimage.BatchImageItem{
			JobID:                item.JobID,
			CustomID:             item.CustomID,
			Status:               item.Status,
			RequestHash:          item.RequestHash,
			PromptPreview:        item.PromptPreview,
			ProviderSourceObject: item.ProviderSourceObject,
			SourceLineNumber:     item.SourceLineNumber,
			SourceByteOffset:     item.SourceByteOffset,
			SourceByteLength:     item.SourceByteLength,
			MimeType:             item.MimeType,
			FileExtension:        item.FileExtension,
			ImageCount:           item.ImageCount,
			ErrorCode:            item.ErrorCode,
			ErrorMessage:         item.ErrorMessage,
			BilledAmount:         item.BilledAmount,
			IndexedAt:            item.IndexedAt,
		}, nil
	}
	return nil, batchimage.ErrBatchImageItemNotFound
}

func (r *fakeBatchImageRepository) ListBatchImageItemsForDownload(ctx context.Context, batchID string, status string, limit int) ([]*batchimage.BatchImageItem, error) {
	return r.ListBatchImageItems(ctx, batchID, batchimage.BatchImageItemFilter{Status: status, Limit: limit})
}

func (r *fakeBatchImageRepository) ListBatchImageJobsDueForInputCleanup(_ context.Context, cutoff time.Time, limit int) ([]*batchimage.BatchImageJob, error) {
	if limit <= 0 {
		limit = 100
	}
	var jobs []*batchimage.BatchImageJob
	for _, job := range r.jobs {
		if job.InputDeletedAt != nil || batchimage.BatchImageDerefString(job.ProviderInputRef) == "" || !batchimage.IsTerminalBatchImageJobStatus(job.Status) {
			continue
		}
		at := job.FinishedAt
		if at == nil {
			at = job.SettledAt
		}
		if at == nil {
			at = &job.UpdatedAt
		}
		if at != nil && at.After(cutoff) {
			continue
		}
		jobs = append(jobs, job)
		if len(jobs) >= limit {
			break
		}
	}
	return jobs, nil
}

func (r *fakeBatchImageRepository) ListBatchImageJobsDueForOutputCleanup(_ context.Context, now time.Time, limit int) ([]*batchimage.BatchImageJob, error) {
	if limit <= 0 {
		limit = 100
	}
	var jobs []*batchimage.BatchImageJob
	for _, job := range r.jobs {
		if job.OutputDeletedAt != nil || batchimage.BatchImageDerefString(job.ProviderOutputRef) == "" || job.Status != batchimage.BatchImageJobStatusCompleted || job.OutputExpiresAt == nil || job.OutputExpiresAt.After(now) {
			continue
		}
		jobs = append(jobs, job)
		if len(jobs) >= limit {
			break
		}
	}
	return jobs, nil
}

func (r *fakeBatchImageRepository) ListStaleUnsubmittedBatchImageJobs(_ context.Context, cutoff time.Time, limit int) ([]*batchimage.BatchImageJob, error) {
	if limit <= 0 {
		limit = 100
	}
	jobs := make([]*batchimage.BatchImageJob, 0, limit)
	for _, job := range r.jobs {
		if len(jobs) >= limit {
			break
		}
		if job.Status != batchimage.BatchImageJobStatusCreated && job.Status != batchimage.BatchImageJobStatusUploading {
			continue
		}
		if batchimage.BatchImageDerefString(job.ProviderJobName) != "" {
			continue
		}
		holdAmount := job.EstimatedCost
		if job.HoldAmount != nil {
			holdAmount = *job.HoldAmount
		}
		if holdAmount <= 0 || job.UpdatedAt.After(cutoff) {
			continue
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

func (r *fakeBatchImageRepository) MarkBatchImageInputDeleted(_ context.Context, batchID string, deletedAt time.Time) error {
	job, ok := r.jobs[batchID]
	if !ok {
		return batchimage.ErrBatchImageJobNotFound
	}
	if job.InputDeletedAt == nil {
		job.InputDeletedAt = &deletedAt
	}
	r.events[batchID] = append(r.events[batchID], "input_cleanup_completed")
	return nil
}

func (r *fakeBatchImageRepository) MarkBatchImageOutputDeleted(_ context.Context, batchID string, deletedAt time.Time) error {
	job, ok := r.jobs[batchID]
	if !ok {
		return batchimage.ErrBatchImageJobNotFound
	}
	if job.OutputDeletedAt == nil {
		job.OutputDeletedAt = &deletedAt
	}
	if job.Status == batchimage.BatchImageJobStatusCompleted {
		job.Status = batchimage.BatchImageJobStatusOutputDeleted
	}
	r.events[batchID] = append(r.events[batchID], "output_cleanup_completed")
	return nil
}

func (r *fakeBatchImageRepository) MarkBatchImageDownloaded(_ context.Context, batchID string, downloadedAt time.Time) error {
	job, ok := r.jobs[batchID]
	if !ok {
		return batchimage.ErrBatchImageJobNotFound
	}
	if job.DownloadedAt == nil {
		job.DownloadedAt = &downloadedAt
	}
	r.events[batchID] = append(r.events[batchID], "download_completed")
	return nil
}

func (r *fakeBatchImageRepository) MarkBatchImageJobUserDeleted(_ context.Context, userID, apiKeyID int64, batchID string, deletedAt time.Time) error {
	job, ok := r.jobs[batchID]
	if !ok || job.UserID != userID || job.APIKeyID == nil || *job.APIKeyID != apiKeyID {
		return batchimage.ErrBatchImageJobNotFound
	}
	if !batchimage.IsBatchImageProcessorDoneStatus(job.Status) {
		return batchimage.ErrBatchImageRecordDeleteNotReady
	}
	if job.UserDeletedAt == nil {
		job.UserDeletedAt = &deletedAt
	}
	r.events[batchID] = append(r.events[batchID], "user_record_deleted")
	return nil
}

func (r *fakeBatchImageRepository) SetBatchImageOutputExpiresAt(_ context.Context, batchID string, expiresAt time.Time) error {
	job, ok := r.jobs[batchID]
	if !ok {
		return batchimage.ErrBatchImageJobNotFound
	}
	if job.OutputExpiresAt == nil {
		job.OutputExpiresAt = &expiresAt
	}
	return nil
}

func (r *fakeBatchImageRepository) RecordBatchImageCleanupFailure(_ context.Context, batchID, code, message string) error {
	job, ok := r.jobs[batchID]
	if !ok {
		return batchimage.ErrBatchImageJobNotFound
	}
	job.LastErrorCode = batchimage.BatchImageStringPtr(code)
	job.LastErrorMessage = batchimage.BatchImageOptionalStringPtr(message)
	r.events[batchID] = append(r.events[batchID], "output_cleanup_failed")
	return nil
}

func (r *fakeBatchImageRepository) AppendBatchImageEvent(_ context.Context, batchID, eventType string, _ any) error {
	r.events[batchID] = append(r.events[batchID], eventType)
	return nil
}
