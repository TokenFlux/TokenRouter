// BillingRecovery 只恢复未提交供应商任务的资金，不改变已续心跳或已提交任务。
package batchimage

import (
	"context"
	"errors"
	"time"
)

type BillingRecovery struct {
	Now            func() time.Time
	Repo           BatchImageRepository
	Funding        Funding
	Queue          BatchImageQueue
	StaleAfter     time.Duration
	Limit          int
	InvalidateAuth func(context.Context, int64)
	Observe        func(string, ...any)
}

func (s *BillingRecovery) warn(event string, values ...any) {
	if s.Observe != nil {
		s.Observe(event, values...)
	}
}

const (
	defaultBatchImageBillingRecoveryStaleAfter = 10 * time.Minute
	defaultBatchImageBillingRecoveryLimit      = 100
)

func (s *BillingRecovery) ReleaseStaleUnsubmittedOnce(ctx context.Context) (int, error) {
	if s == nil || s.Repo == nil || s.Funding.Store == nil {
		return 0, nil
	}
	staleAfter := s.StaleAfter
	if staleAfter <= 0 {
		staleAfter = defaultBatchImageBillingRecoveryStaleAfter
	}
	limit := s.Limit
	if limit <= 0 {
		limit = defaultBatchImageBillingRecoveryLimit
	}
	cutoff := s.now().Add(-staleAfter)
	jobs, err := s.Repo.ListStaleUnsubmittedBatchImageJobs(ctx, cutoff, limit)
	if err != nil {
		return 0, err
	}
	released := 0
	var lastErr error
	for _, job := range jobs {
		if job == nil {
			continue
		}
		if err := ctx.Err(); err != nil {
			return released, err
		}
		msg := "batch image submission did not reach provider before recovery cutoff"
		// 原子转 failed 并复核 stale 条件：List 与转态之间 job 可能已被慢提交
		// 心跳续期或提交成功（provider_job_name 已写入），此时绝不能退款，
		// 否则上游任务照常产生成本而用户已拿回冻结余额。
		applied, err := s.Repo.FailStaleUnsubmittedBatchImageJob(ctx, job.BatchID, cutoff, "SUBMIT_STALE_BEFORE_PROVIDER", msg)
		if err != nil {
			// applied=true 时 UPDATE 已提交（仅审计事件写入失败）：必须继续释放，
			// 否则 job 已转 failed、不再出现在 stale 列表，冻结余额会永久泄漏。
			if !applied {
				lastErr = err
				continue
			}
			s.warn("batch_image.recovery_fail_event_append_failed",
				"batch_id", job.BatchID,
				"error", err,
			)
		}
		if !applied {
			continue
		}
		job.Status = BatchImageJobStatusFailed
		if err := s.Funding.Release(ctx, job, BatchImageDerefString(job.RequestHash)); err != nil {
			// job 已转 failed、不会再进入 stale 列表：必须给释放失败留下
			// 自动重试路径（入队后由 worker 的 releaseTerminalHold 兜底），
			// 否则冻结余额永久泄漏。
			s.warn("batch_image.recovery_release_failed",
				"batch_id", job.BatchID,
				"error", err,
			)
			s.EnqueueReleaseRetry(ctx, job.BatchID)
			lastErr = err
			continue
		}
		if billingUserID := BillingUserID(job); s.InvalidateAuth != nil && billingUserID > 0 {
			s.InvalidateAuth(ctx, billingUserID)
		}
		released++
	}
	return released, lastErr
}
func (s *BillingRecovery) EnqueueReleaseRetry(ctx context.Context, batchID string) {
	if s == nil || s.Queue == nil {
		return
	}
	if err := s.Queue.Enqueue(ctx, batchID); err != nil && !errors.Is(err, ErrBatchImageAlreadyQueued) {
		s.warn("batch_image.recovery_release_retry_enqueue_failed",
			"batch_id", batchID,
			"error", err,
		)
	}
}

// now 保持各原取时点，构造时可注入同一时钟来源。
func (s *BillingRecovery) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
