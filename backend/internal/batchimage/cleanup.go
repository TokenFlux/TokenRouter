// Cleanup 拥有资源删除规则，供应商引用仅从已验证的任务记录取得。
package batchimage

import (
	"context"
	"errors"
	"strings"
	"time"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

type CleanupOptions struct {
	InputRetention time.Duration
	Interval       time.Duration
	BatchSize      int
}
type Cleanup struct {
	Now             func() time.Time
	Repo            BatchImageRepository
	ResolveProvider func(context.Context, *BatchImageJob) (BoundProvider, error)
	Options         CleanupOptions
	Observe         func(string, ...any)
}

func (s *Cleanup) warn(event string, values ...any) {
	if s.Observe != nil {
		s.Observe(event, values...)
	}
}
func (s *Cleanup) Run(ctx context.Context) {
	ticker := time.NewTicker(s.CleanupInterval())
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		_, _ = s.RunOnce(ctx, s.now())
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

const (
	DefaultBatchImageInputRetentionAfterTerminal  = 24 * time.Hour
	DefaultBatchImageOutputRetentionAfterTerminal = 72 * time.Hour
	DefaultBatchImageCleanupInterval              = 30 * time.Minute
	DefaultBatchImageCleanupBatchSize             = 100
)

// AppendCleanupEvent 追加清理审计事件；事件写入失败不阻断清理流程，但必须留痕。
func (s *Cleanup) AppendCleanupEvent(ctx context.Context, batchID, eventType string, payload any) {
	if err := s.Repo.AppendBatchImageEvent(ctx, batchID, eventType, payload); err != nil {
		s.warn("batch_image.cleanup_event_failed",
			"batch_id", batchID,
			"event_type", eventType,
			"error", err,
		)
	}
}
func (s *Cleanup) DeleteOutputsForOwner(ctx context.Context, owner BatchImageOwner, batchID string) (*BatchImagePublicBatch, error) {
	job, err := s.Repo.GetBatchImageJobByBatchIDForOwner(ctx, owner.UserID, owner.APIKeyID, batchID)
	if err != nil {
		return nil, err
	}
	if job.Status == BatchImageJobStatusOutputDeleted || job.OutputDeletedAt != nil {
		return BatchImageJobToPublic(job), nil
	}
	if job.Status != BatchImageJobStatusCompleted {
		return nil, ErrBatchImageOutputDeleteNotReady
	}
	s.AppendCleanupEvent(ctx, job.BatchID, "manual_output_delete_requested", map[string]any{
		"batch_id":       job.BatchID,
		"cleanup_target": "output",
		"reason":         "manual",
	})
	if err := s.CleanupJob(ctx, job, CleanupTargetOutput, "manual"); err != nil {
		return nil, err
	}
	updated, err := s.Repo.GetBatchImageJobByBatchIDForOwner(ctx, owner.UserID, owner.APIKeyID, batchID)
	if err != nil {
		return nil, err
	}
	return BatchImageJobToPublic(updated), nil
}
func (s *Cleanup) CleanupInput(ctx context.Context, batchID string) error {
	job, err := s.Repo.GetBatchImageJobByBatchID(ctx, batchID)
	if err != nil {
		return err
	}
	return s.CleanupJob(ctx, job, CleanupTargetInput, "ttl")
}
func (s *Cleanup) CleanupOutput(ctx context.Context, batchID string, reason string) error {
	job, err := s.Repo.GetBatchImageJobByBatchID(ctx, batchID)
	if err != nil {
		return err
	}
	return s.CleanupJob(ctx, job, CleanupTargetOutput, reason)
}
func (s *Cleanup) RunOnce(ctx context.Context, now time.Time) (BatchImageCleanupRunResult, error) {
	if s == nil || s.Repo == nil {
		return BatchImageCleanupRunResult{}, ErrBatchImageCleanupFailed
	}
	if now.IsZero() {
		now = s.now()
	}
	limit := s.CleanupBatchSize()
	result := BatchImageCleanupRunResult{}
	inputCutoff := now.Add(-s.InputRetentionAfterTerminal())
	inputJobs, err := s.Repo.ListBatchImageJobsDueForInputCleanup(ctx, inputCutoff, limit)
	if err != nil {
		return result, err
	}
	for _, job := range inputJobs {
		if job == nil {
			continue
		}
		if err := s.CleanupJob(ctx, job, CleanupTargetInput, "ttl"); err != nil {
			result.Failures++
			continue
		}
		result.InputCleaned++
	}
	outputJobs, err := s.Repo.ListBatchImageJobsDueForOutputCleanup(ctx, now, limit)
	if err != nil {
		return result, err
	}
	for _, job := range outputJobs {
		if job == nil {
			continue
		}
		if err := s.CleanupJob(ctx, job, CleanupTargetOutput, "expired"); err != nil {
			result.Failures++
			continue
		}
		result.OutputCleaned++
	}
	return result, nil
}
func (s *Cleanup) CleanupJob(ctx context.Context, job *BatchImageJob, target CleanupTarget, reason string) error {
	if job == nil {
		return ErrBatchImageJobNotFound
	}
	switch target {
	case CleanupTargetInput:
		if job.InputDeletedAt != nil {
			return nil
		}
		if !IsTerminalBatchImageJobStatus(job.Status) {
			return ErrBatchImageCleanupFailed
		}
		s.AppendCleanupEvent(ctx, job.BatchID, "input_cleanup_started", CleanupEventPayload(job.BatchID, target, reason, nil))
	case CleanupTargetOutput:
		if job.OutputDeletedAt != nil || job.Status == BatchImageJobStatusOutputDeleted {
			return nil
		}
		if job.Status != BatchImageJobStatusCompleted && job.Status != BatchImageJobStatusFailed && job.Status != BatchImageJobStatusCancelled {
			return ErrBatchImageOutputDeleteNotReady
		}
		s.AppendCleanupEvent(ctx, job.BatchID, "output_cleanup_started", CleanupEventPayload(job.BatchID, target, reason, nil))
	default:
		return ErrUnsupportedCleanupTarget
	}

	if err := s.CallProviderCleanup(ctx, job, target); err != nil {
		code := CleanupFailureCode(err)
		msg := SanitizeBatchImagePublicMessage(err.Error())
		if recordErr := s.Repo.RecordBatchImageCleanupFailure(ctx, job.BatchID, code, msg); recordErr != nil {
			s.warn("batch_image.cleanup_failure_record_failed",
				"batch_id", job.BatchID,
				"error", recordErr,
			)
		}
		event := string(target) + "_cleanup_failed"
		s.AppendCleanupEvent(ctx, job.BatchID, event, map[string]any{"batch_id": job.BatchID, "cleanup_target": string(target), "reason": reason, "error_code": code})
		if errors.Is(err, ErrBatchImageProviderUnsafeCleanupPath) {
			return ErrBatchImageCleanupUnsafePath
		}
		return ErrBatchImageProviderCleanupFailed
	}

	deletedAt := s.now()
	if target == CleanupTargetInput {
		return s.Repo.MarkBatchImageInputDeleted(ctx, job.BatchID, deletedAt)
	}
	return s.Repo.MarkBatchImageOutputDeleted(ctx, job.BatchID, deletedAt)
}
func (s *Cleanup) CallProviderCleanup(ctx context.Context, job *BatchImageJob, target CleanupTarget) error {
	if s == nil || s.ResolveProvider == nil {
		return ErrBatchImageCleanupFailed
	}
	provider, err := s.ResolveProvider(ctx, job)
	if err != nil {
		return err
	}
	if err := provider.Cleanup(ctx, job, target); err != nil {
		if CleanupErrorIsNotFound(err) {
			return nil
		}
		return err
	}
	return nil
}
func (s *Cleanup) InputRetentionAfterTerminal() time.Duration {
	if s != nil && s.Options.InputRetention > 0 {
		return s.Options.InputRetention
	}
	return DefaultBatchImageInputRetentionAfterTerminal
}
func (s *Cleanup) CleanupInterval() time.Duration {
	if s != nil && s.Options.Interval > 0 {
		return s.Options.Interval
	}
	return DefaultBatchImageCleanupInterval
}
func (s *Cleanup) CleanupBatchSize() int {
	if s != nil && s.Options.BatchSize > 0 {
		return s.Options.BatchSize
	}
	return DefaultBatchImageCleanupBatchSize
}

type BatchImageCleanupRunResult struct {
	InputCleaned  int
	OutputCleaned int
	Failures      int
}

func CleanupEventPayload(batchID string, target CleanupTarget, reason string, deletedAt *time.Time) map[string]any {
	payload := map[string]any{
		"batch_id":       batchID,
		"cleanup_target": string(target),
		"reason":         reason,
	}
	if deletedAt != nil {
		payload["deleted_at"] = deletedAt.UTC().Format(time.RFC3339)
	}
	return payload
}
func CleanupErrorIsNotFound(err error) bool {
	if err == nil {
		return false
	}
	reason := strings.ToUpper(infraerrors.Reason(err))
	msg := strings.ToUpper(err.Error())
	return strings.Contains(reason, "NOT_FOUND") || strings.Contains(msg, "NOT FOUND") || strings.Contains(msg, "404")
}
func CleanupFailureCode(err error) string {
	if errors.Is(err, ErrBatchImageProviderUnsafeCleanupPath) {
		return "BATCH_IMAGE_CLEANUP_UNSAFE_PATH"
	}
	reason := strings.TrimSpace(infraerrors.Reason(err))
	if reason != "" {
		return reason
	}
	return "BATCH_IMAGE_PROVIDER_CLEANUP_FAILED"
}

// now 保持各原取时点，构造时可注入同一时钟来源。
func (s *Cleanup) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
