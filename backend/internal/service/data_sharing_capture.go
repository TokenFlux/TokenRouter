package service

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"log/slog"
	"strings"
	"syscall"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
)

// CaptureClaudeRequest 采集 Claude/Gemini 兼容协议成功请求。
func (s *DataSharingService) CaptureClaudeRequest(ctx context.Context, input DataShareCaptureInput) error {
	session := s.buildBufferedCaptureSession(ctx, input)
	if session == nil {
		return nil
	}
	return s.submitCaptureSessionToBuffer(ctx, session)
}

// CaptureClaudeRequestAsync 异步提交 Claude/Gemini 兼容协议数据共享采集。
func (s *DataSharingService) CaptureClaudeRequestAsync(input DataShareCaptureInput) DataSharingCaptureSubmitMode {
	return s.submitCaptureJob(DataSharingCaptureProtocolClaude, input)
}

// CaptureOpenAIRequest 采集 OpenAI 协议成功请求。
func (s *DataSharingService) CaptureOpenAIRequest(ctx context.Context, input DataShareCaptureInput) error {
	session := s.buildBufferedCaptureSession(ctx, input)
	if session == nil {
		return nil
	}
	return s.submitCaptureSessionToBuffer(ctx, session)
}

func (s *DataSharingService) buildBufferedCaptureSession(ctx context.Context, input DataShareCaptureInput) *DataShareSession {
	// 缓冲池最终只落库合并后的快照，入缓冲时跳过质量评估和 payload marshal，降低热点 session CPU 消耗。
	start := time.Now()
	defer func() {
		if s != nil && s.captureDurations != nil {
			s.captureDurations.Observe(DataShareCaptureDurationPartCaptureBuild, time.Since(start))
		}
	}()
	return s.buildCaptureSessionWithOptions(ctx, input, dataShareBuildSessionOptions{FinalizeQuality: false})
}

func (s *DataSharingService) buildCaptureSessionWithOptions(ctx context.Context, input DataShareCaptureInput, opts dataShareBuildSessionOptions) *DataShareSession {
	if input.APIKey == nil || input.APIKey.Group == nil || !input.APIKey.Group.DataSharingEnabled {
		return nil
	}
	if s == nil || s.repo == nil {
		return nil
	}
	if s.shouldSkipDataShareCapture(ctx, input) {
		return nil
	}
	if input.Model == "" && input.UpstreamModel != "" {
		input.Model = input.UpstreamModel
	}
	if dataShareCaptureInputIsOpenAIResponses(input) && !opts.FinalizeQuality {
		return s.buildOpenAIResponsesRawCaptureSession(input)
	}
	if dataShareCaptureInputIsMessagesRaw(input) && !opts.FinalizeQuality {
		return s.buildMessagesRawCaptureSession(input)
	}
	return s.buildSessionWithOptions(input, opts)
}

// CaptureOpenAIRequestAsync 异步提交 OpenAI 协议数据共享采集。
func (s *DataSharingService) CaptureOpenAIRequestAsync(input DataShareCaptureInput) DataSharingCaptureSubmitMode {
	return s.submitCaptureJob(DataSharingCaptureProtocolOpenAI, input)
}

func (s *DataSharingService) submitCaptureJob(protocol DataSharingCaptureProtocol, input DataShareCaptureInput) DataSharingCaptureSubmitMode {
	if s == nil || s.repo == nil {
		return DataSharingCaptureSubmitModeDropped
	}
	if input.APIKey == nil || input.APIKey.Group == nil || !input.APIKey.Group.DataSharingEnabled {
		return DataSharingCaptureSubmitModeDropped
	}
	if input.Model == "" && input.UpstreamModel != "" {
		input.Model = input.UpstreamModel
	}
	metadata := dataShareCaptureMetadata(input)
	if s.captureWorker == nil {
		s.recordMissingCaptureWorkerDrop(metadata)
		return DataSharingCaptureSubmitModeDropped
	}
	return s.captureWorker.Submit(DataSharingCaptureJob{
		Protocol: protocol,
		Input:    input,
		Metadata: metadata,
	})
}

func (s *DataSharingService) handleCaptureJob(ctx context.Context, job DataSharingCaptureJob) error {
	if s == nil {
		return nil
	}
	return s.captureRequestFromJob(ctx, job)
}

func (s *DataSharingService) captureRequestFromJob(ctx context.Context, job DataSharingCaptureJob) error {
	session := s.buildBufferedCaptureSession(ctx, job.Input)
	if session == nil {
		return nil
	}
	// 只把解析后的轻量增量放入进程内缓冲，容量检查和压缩落库延后到 flush 阶段执行。
	return s.submitCaptureSessionToBuffer(ctx, session)
}

func (s *DataSharingService) flushBufferedCaptureSession(ctx context.Context, session *DataShareSession) error {
	if s == nil || s.repo == nil {
		return nil
	}
	return s.withDataSharePersistenceRetry(ctx, "flush", func(attemptCtx context.Context) error {
		return s.repo.SaveCaptureSnapshot(attemptCtx, session, s.captureStorageLimitOption(attemptCtx))
	})
}

func (s *DataSharingService) hydrateBufferedCaptureSession(ctx context.Context, key string) (*DataShareSession, error) {
	if s == nil || s.repo == nil {
		return nil, ErrDataShareSessionNotFound
	}
	var session *DataShareSession
	err := s.withDataSharePersistenceRetry(ctx, "hydrate", func(attemptCtx context.Context) error {
		var err error
		session, err = s.repo.GetCaptureByTrajectoryIDWithPayload(attemptCtx, key)
		return err
	})
	return session, err
}

func (s *DataSharingService) scheduleBufferedCaptureFlush(job DataSharingCaptureJob) DataSharingCaptureSubmitMode {
	if s == nil || s.captureWorker == nil {
		return DataSharingCaptureSubmitModeDropped
	}
	return s.captureWorker.SubmitFlush(job)
}

func (s *DataSharingService) submitCaptureSessionToBuffer(ctx context.Context, session *DataShareSession) error {
	if s == nil {
		return nil
	}
	if s.captureBuffer == nil {
		return s.flushBufferedCaptureSession(ctx, finalizeBufferedDataShareSession(session))
	}
	return s.captureBuffer.Submit(ctx, session)
}

func (s *DataSharingService) withDataSharePersistenceRetry(ctx context.Context, operation string, fn func(context.Context) error) error {
	if fn == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	delay := dataSharePersistenceRetryInitialDelay
	for attempt := 1; attempt <= dataSharePersistenceRetryAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := fn(ctx)
		if err == nil {
			return nil
		}
		if attempt == dataSharePersistenceRetryAttempts || !isDataShareTransientPersistenceError(err) {
			return err
		}
		// 数据共享采集是后台 fail-open 链路，数据库旧连接被重置时短暂退避后重试完整幂等 upsert。
		slog.Warn("data sharing: retry transient persistence error",
			"operation", operation,
			"attempt", attempt,
			"error", err,
		)
		if sleepErr := sleepWithContext(ctx, delay); sleepErr != nil {
			return sleepErr
		}
		delay *= 2
	}
	return nil
}

func isDataShareTransientPersistenceError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, driver.ErrBadConn) ||
		errors.Is(err, sql.ErrConnDone) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EPIPE) {
		return true
	}
	msg := strings.ToLower(err.Error())
	for _, pattern := range []string{
		"connection reset by peer",
		"broken pipe",
		"driver: bad connection",
		"bad connection",
		"server closed the connection unexpectedly",
		"terminating connection due to administrator command",
		"connection refused",
		"use of closed network connection",
		"unexpected eof",
	} {
		if strings.Contains(msg, pattern) {
			return true
		}
	}
	return false
}

// Stop 停止数据共享采集缓冲池，正常退出时尽量把内存中的增量落库。
func (s *DataSharingService) Stop(ctx context.Context) {
	if s == nil {
		return
	}
	if s.captureWorker != nil {
		s.captureWorker.Stop()
	}
	if s.captureBuffer != nil {
		s.captureBuffer.Stop(ctx)
	}
}

func (s *DataSharingService) recordMissingCaptureWorkerDrop(metadata DataSharingCaptureJobMetadata) {
	if s == nil {
		return
	}
	s.captureWorkerNilDropped.Add(1)
	now := time.Now().UnixNano()
	last := s.captureWorkerNilLogNanos.Load()
	if now-last < int64(dataSharingCaptureDropLogInterval) {
		return
	}
	if !s.captureWorkerNilLogNanos.CompareAndSwap(last, now) {
		return
	}
	slog.Warn(
		"data_sharing.capture_dropped",
		"reason", "missing_worker",
		"provider", metadata.Provider,
		"model", metadata.Model,
		"request_id", metadata.RequestID,
		"api_key_id", metadata.APIKeyID,
		"account_id", metadata.AccountID,
		"group_id", metadata.GroupID,
		"dropped_total", s.captureWorkerNilDropped.Load(),
	)
}

func dataShareCaptureMetadata(input DataShareCaptureInput) DataSharingCaptureJobMetadata {
	metadata := DataSharingCaptureJobMetadata{
		Provider:  input.Provider,
		Model:     firstNonBlank(input.UpstreamModel, input.Model),
		RequestID: input.RequestID,
	}
	if input.APIKey != nil {
		metadata.APIKeyID = input.APIKey.ID
		if input.APIKey.GroupID != nil {
			metadata.GroupID = *input.APIKey.GroupID
		} else if input.APIKey.Group != nil {
			metadata.GroupID = input.APIKey.Group.ID
		}
	}
	if input.Account != nil {
		metadata.AccountID = input.Account.ID
	}
	return metadata
}

// ListSessions 查询数据共享 session。
func (s *DataSharingService) ListSessions(ctx context.Context, params pagination.PaginationParams, filters DataShareSessionFilters) ([]DataShareSession, *pagination.PaginationResult, error) {
	return s.repo.List(ctx, params, filters)
}

// GetSession 查询单条 session，并可选限制 userID。
func (s *DataSharingService) GetSession(ctx context.Context, id int64, userID int64) (*DataShareSession, error) {
	session, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if userID > 0 && session.UserID != userID {
		return nil, ErrDataShareSessionNotFound
	}
	return session, nil
}

func (s *DataSharingService) DeleteSession(ctx context.Context, id int64) error {
	return s.repo.Delete(ctx, id)
}

func (s *DataSharingService) BatchDeleteSessions(ctx context.Context, ids []int64, filters DataShareSessionFilters) (int64, error) {
	return s.repo.BatchDelete(ctx, ids, filters)
}

func (s *DataSharingService) Stats(ctx context.Context, filters DataShareSessionFilters) (*DataShareStats, error) {
	stats, err := s.repo.Stats(ctx, filters)
	if err != nil {
		return nil, err
	}
	if stats == nil {
		stats = &DataShareStats{}
	}
	stats.CaptureWorker = s.CaptureWorkerStats()
	stats.CaptureBuffer = s.CaptureBufferStats()
	stats.CaptureDurations = s.CaptureDurationStats()
	stats.ExportDurations = s.ExportDurationStats()
	return stats, nil
}

// CaptureWorkerStats 返回数据共享采集池运行时统计。
func (s *DataSharingService) CaptureWorkerStats() DataSharingCaptureWorkerPoolStats {
	if s == nil {
		return DataSharingCaptureWorkerPoolStats{}
	}
	if s.captureWorker == nil {
		return DataSharingCaptureWorkerPoolStats{
			DroppedTotal: s.captureWorkerNilDropped.Load(),
		}
	}
	return s.captureWorker.Stats()
}

// CaptureBufferStats 返回数据共享采集缓冲池运行时统计。
func (s *DataSharingService) CaptureBufferStats() DataSharingCaptureBufferStats {
	if s == nil || s.captureBuffer == nil {
		return DataSharingCaptureBufferStats{}
	}
	return s.captureBuffer.Stats()
}

// CaptureDurationStats 返回数据共享采集链路的进程内耗时统计。
func (s *DataSharingService) CaptureDurationStats() DataShareCaptureDurationStats {
	if s == nil || s.captureDurations == nil {
		return DataShareCaptureDurationStats{WindowSize: defaultDataSharingCaptureDurationWindowSize}
	}
	return s.captureDurations.Snapshot()
}

// ExportDurationStats 返回预生成导出链路的进程内耗时统计。
func (s *DataSharingService) ExportDurationStats() DataShareExportDurationStats {
	if s == nil || s.exportDurations == nil {
		return DataShareExportDurationStats{WindowSize: defaultDataSharingCaptureDurationWindowSize}
	}
	return s.exportDurations.Snapshot()
}

func (s *DataSharingService) FilterOptions(ctx context.Context, filters DataShareSessionFilters) (*DataShareSessionFilterOptions, error) {
	return s.repo.FilterOptions(ctx, filters)
}
