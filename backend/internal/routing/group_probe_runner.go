// 本文件维护 routing 的所属能力；兼容入口复用唯一实现。
package routing

import (
	context "context"
	fmt "fmt"
	strings "strings"
	sync "sync"
	time "time"
)

const (
	groupAvailabilityProbeDefaultMaxWorkers = 5
	// 最长运行时间覆盖合法配置下的全部尝试，并为账号选择和结果保存预留一分钟。
	groupAvailabilityProbeMaxRunDuration     = time.Duration(MaxGroupAvailabilityProbeMaxRetries+1)*time.Duration(MaxGroupAvailabilityProbeTimeoutSeconds)*time.Second + time.Minute
	groupAvailabilityProbeMaintenanceTimeout = time.Minute
	groupAvailabilityProbeClaimTimeout       = time.Minute
	groupAvailabilityProbeLockDuration       = groupAvailabilityProbeMaxRunDuration + groupAvailabilityProbeClaimTimeout + time.Minute
	groupAvailabilityProbeCleanupRetention   = 90 * 24 * time.Hour
	groupAvailabilityProbeCleanupMinInterval = 12 * time.Hour
)

// @project-doc docs/interfaces/model_catalog_and_marketplace.md#group_availability_probe
func (s *GroupAvailabilityProbeRunnerService) runDue() {
	// cron 每分钟触发一次；上一轮尚未结束时跳过本轮，避免突破实例级 worker 上限。
	if !s.runMu.TryLock() {
		return
	}
	defer s.runMu.Unlock()
	parent, ok := s.beginRun()
	if !ok {
		return
	}
	defer s.endRun()

	// 清理与领取使用独立短上下文，不能消耗分组探测本身的重试预算。
	maintenanceCtx, maintenanceCancel := context.WithTimeout(parent, groupAvailabilityProbeMaintenanceTimeout)
	s.cleanupIfNeeded(maintenanceCtx, s.now())
	maintenanceCancel()
	if parent.Err() != nil {
		return
	}

	// 只领取能够立即交给 worker 的分组，确保领取租约不会在排队期间过期。
	claimCtx, claimCancel := context.WithTimeout(parent, groupAvailabilityProbeClaimTimeout)
	claimNow := s.now()
	dueGroups, err := s.repo.ClaimDue(claimCtx, claimNow, claimNow.Add(groupAvailabilityProbeLockDuration), s.instanceID, groupAvailabilityProbeDefaultMaxWorkers)
	claimCancel()
	if err != nil {
		s.observe("[GroupAvailabilityProbe] ClaimDue error: %v", err)
		return
	}
	if parent.Err() != nil || len(dueGroups) == 0 {
		return
	}

	sem := make(chan struct{}, groupAvailabilityProbeDefaultMaxWorkers)
	var wg sync.WaitGroup
	for i := range dueGroups {
		group := dueGroups[i]
		select {
		case sem <- struct{}{}:
		case <-parent.Done():
			wg.Wait()
			return
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			// 每个分组独立计时，数据库维护和其它分组不会挤占它的合法重试窗口。
			probeCtx, probeCancel := context.WithTimeout(parent, groupAvailabilityProbeMaxRunDuration)
			defer probeCancel()
			s.runOne(probeCtx, group)
		}()
	}
	wg.Wait()
}

func (s *GroupAvailabilityProbeRunnerService) cleanupIfNeeded(ctx context.Context, now time.Time) {
	if now.Sub(s.lastCleanupAt) < groupAvailabilityProbeCleanupMinInterval {
		return
	}
	s.lastCleanupAt = now
	if err := s.repo.CleanupOldResults(ctx, now.Add(-groupAvailabilityProbeCleanupRetention)); err != nil {
		s.observe("[GroupAvailabilityProbe] cleanup error: %v", err)
	}
}

func (s *GroupAvailabilityProbeRunnerService) runOne(ctx context.Context, due GroupAvailabilityProbeDueGroup) {
	probeConfig, err := NormalizeGroupAvailabilityProbeConfig(due.Config)
	if err != nil {
		s.saveFailure(ctx, due, probeConfig, fmt.Sprintf("invalid probe config: %v", err), nil)
		return
	}
	if !probeConfig.Enabled {
		return
	}

	maxRetries := DefaultGroupAvailabilityProbeMaxRetries
	if probeConfig.MaxRetries != nil {
		maxRetries = *probeConfig.MaxRetries
	}
	probeResult := runGroupAvailabilityProbeAttempts(
		ctx,
		maxRetries,
		time.Duration(probeConfig.TimeoutSeconds)*time.Second,
		func(attemptCtx context.Context) *GroupAvailabilityProbeResult {
			return s.runProbeAttempt(attemptCtx, due, probeConfig)
		},
	)
	if probeResult == nil {
		s.saveFailure(ctx, due, probeConfig, "probe did not produce a result", nil)
		return
	}

	s.saveResult(ctx, due, probeConfig, probeResult)
}

// runGroupAvailabilityProbeAttempts 执行首次探测及后续重试，任一尝试成功即结束。
// 每次尝试使用独立超时，避免前一次超时后的已取消上下文阻断后续重试。
func runGroupAvailabilityProbeAttempts(
	ctx context.Context,
	maxRetries int,
	timeout time.Duration,
	runAttempt func(context.Context) *GroupAvailabilityProbeResult,
) *GroupAvailabilityProbeResult {
	if maxRetries < 0 {
		maxRetries = 0
	}

	var lastResult *GroupAvailabilityProbeResult
	for retryCount := 0; retryCount <= maxRetries; retryCount++ {
		if ctx.Err() != nil {
			break
		}
		attemptCtx := ctx
		cancel := func() {}
		if timeout > 0 {
			attemptCtx, cancel = context.WithTimeout(ctx, timeout)
		}
		lastResult = runAttempt(attemptCtx)
		cancel()

		if lastResult != nil && lastResult.Success {
			return lastResult
		}
		if ctx.Err() != nil {
			break
		}
	}
	return lastResult
}

// runProbeAttempt 完成一次账号选择和真实请求，并转换为统一的分组探测结果。
func (s *GroupAvailabilityProbeRunnerService) runProbeAttempt(ctx context.Context, due GroupAvailabilityProbeDueGroup, probeConfig GroupAvailabilityProbeConfig) *GroupAvailabilityProbeResult {
	startedAt := s.now()
	accountID, err := s.executor.Select(ctx, due, probeConfig.ModelID)
	if err != nil {
		finishedAt := s.now()
		return &GroupAvailabilityProbeResult{
			GroupID:      due.GroupID,
			ModelID:      probeConfig.ModelID,
			Status:       GroupAvailabilityProbeStatusFailed,
			Success:      false,
			LatencyMs:    finishedAt.Sub(startedAt).Milliseconds(),
			ErrorMessage: err.Error(),
			StartedAt:    startedAt,
			FinishedAt:   finishedAt,
		}
	}

	result, err := s.executor.Test(ctx, accountID, probeConfig.ModelID, probeConfig.Prompt, probeConfig.UserAgent)
	finishedAt := s.now()
	probeResult := &GroupAvailabilityProbeResult{
		GroupID:    due.GroupID,
		AccountID:  &accountID,
		ModelID:    probeConfig.ModelID,
		Status:     GroupAvailabilityProbeStatusSuccess,
		Success:    true,
		LatencyMs:  finishedAt.Sub(startedAt).Milliseconds(),
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
	}
	if result != nil {
		probeResult.Status = result.Status
		probeResult.Success = result.Status == GroupAvailabilityProbeStatusSuccess
		probeResult.LatencyMs = result.LatencyMs
		probeResult.ErrorMessage = result.ErrorMessage
		probeResult.StartedAt = result.StartedAt
		probeResult.FinishedAt = result.FinishedAt
	}
	if err != nil {
		probeResult.Status = GroupAvailabilityProbeStatusFailed
		probeResult.Success = false
		if probeResult.ErrorMessage == "" {
			probeResult.ErrorMessage = err.Error()
		}
	}
	if ctx.Err() != nil && probeResult.Success {
		probeResult.Status = GroupAvailabilityProbeStatusFailed
		probeResult.Success = false
		probeResult.ErrorMessage = ctx.Err().Error()
	}

	return probeResult
}

func (s *GroupAvailabilityProbeRunnerService) saveFailure(ctx context.Context, due GroupAvailabilityProbeDueGroup, cfg GroupAvailabilityProbeConfig, message string, accountID *int64) {
	now := s.now()
	modelID := strings.TrimSpace(cfg.ModelID)
	if modelID == "" {
		modelID = strings.TrimSpace(due.Config.ModelID)
	}
	s.saveResult(ctx, due, cfg, &GroupAvailabilityProbeResult{
		GroupID:      due.GroupID,
		AccountID:    accountID,
		ModelID:      modelID,
		Status:       GroupAvailabilityProbeStatusFailed,
		Success:      false,
		ErrorMessage: message,
		StartedAt:    now,
		FinishedAt:   now,
	})
}

func (s *GroupAvailabilityProbeRunnerService) saveResult(ctx context.Context, due GroupAvailabilityProbeDueGroup, cfg GroupAvailabilityProbeConfig, result *GroupAvailabilityProbeResult) {
	interval := time.Duration(cfg.IntervalMinutes) * time.Minute
	if interval <= 0 {
		interval = time.Duration(DefaultGroupAvailabilityProbeIntervalMinutes) * time.Minute
	}
	nextRunAt := s.now().Add(interval)
	if err := s.repo.SaveResultAndScheduleNext(ctx, result, nextRunAt); err != nil {
		s.observe("[GroupAvailabilityProbe] group=%d save result error: %v", due.GroupID, err)
	}
}
