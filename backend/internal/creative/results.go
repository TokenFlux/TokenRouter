// 结果推进与补偿属于创作台；资金和分析事实通过独立端口提交。
package creative

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

var ErrCreativeSettlementAccountMissing = apperror.BadRequest("BATCH_IMAGE_SETTLEMENT_MISSING_ACCOUNT_ID", "batch image settlement account id is missing")

type Results struct {
	Now            func() time.Time
	Repo           CreativeRunRepository
	TransientStore CreativeTransientStore
	Queue          CreativeRunQueue
	Outbox         CreativeRunOutboxRepository
	Funding        Funding
	TransientTTL   time.Duration
	RecordUsage    func(context.Context, *usage.UsageLog)
	InvalidateAuth func(context.Context, int64)
	Observe        func(string, ...any)
}

func (s *Results) warn(event string, values ...any) {
	if s.Observe != nil {
		s.Observe(event, values...)
	}
}

// sanitizeCreativeMessage 截断错误消息，避免把上游细节原样抛给客户端。
func SanitizeCreativeMessage(message string) string {
	message = strings.TrimSpace(message)
	message = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' || (r >= 0x20 && r != 0x7f) {
			return r
		}
		return ' '
	}, message)
	if !utf8.ValidString(message) {
		message = strings.ToValidUTF8(message, "?")
	}
	runes := []rune(message)
	if len(runes) > maxCreativeErrorMessageChars {
		message = string(runes[:maxCreativeErrorMessageChars])
	}
	return message
}
func firstAllocatedSubscriptionID(allocations []billing.BillingAllocation) *int64 {
	for i := range allocations {
		if allocations[i].Type != billing.BillingAllocationTypeSubscription || allocations[i].SubscriptionID == nil {
			continue
		}
		subscriptionID := *allocations[i].SubscriptionID
		return &subscriptionID
	}
	return nil
}

// ReconcileCreativeTransientOnce 清理已进入终态且客户端已无须继续读取的 transient 数据。
// 删除失败只保留日志，下一轮扫描会再次尝试；run 状态不会被清理动作改写。
func (s *Results) ReconcileCreativeTransientOnce(ctx context.Context) (int, error) {
	if s == nil || s.Repo == nil || s.TransientStore == nil {
		return 0, nil
	}
	runs, err := s.Repo.ListCreativeRunsDueForTransientCleanup(ctx, s.now().Add(-creativeTransientCleanupAge), creativeTransientCleanupLimit)
	if err != nil {
		return 0, err
	}
	cleaned := 0
	for _, run := range runs {
		if run == nil {
			continue
		}
		if err := s.TransientStore.DeleteRunTransient(ctx, run.RunID, 0, run.RequestedOutputCount); err != nil {
			s.warn("creative.transient_cleanup_failed", "run_id", run.RunID, "error", err)
			continue
		}
		cleaned++
	}
	return cleaned, nil
}

// RunCreativeTransientReconciler 周期性清理终态任务的 Redis 临时数据。
func (s *Results) RunCreativeTransientReconciler(ctx context.Context) {
	if s == nil || s.Repo == nil || s.TransientStore == nil {
		return
	}
	ticker := time.NewTicker(creativeTransientCleanupInterval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		_, _ = s.ReconcileCreativeTransientOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// ReconcileCreativeOutboxOnce 领取并处理一批创作台后台动作。
// 图片仍只从 Redis 临时存储读取，outbox 本身只负责恢复入队和结算触发。
func (s *Results) ReconcileCreativeOutboxOnce(ctx context.Context) (int, error) {
	if s == nil || s.Outbox == nil || s.Queue == nil || s.Repo == nil {
		return 0, nil
	}
	events, err := s.Outbox.Claim(ctx, "creative-reconciler", creativeOutboxClaimLimit, creativeOutboxLease)
	if err != nil {
		return 0, err
	}
	processed := 0
	var lastErr error
	for _, event := range events {
		if err := ctx.Err(); err != nil {
			return processed, err
		}
		if err := s.ReconcileCreativeOutboxEvent(ctx, event); err != nil {
			lastErr = err
			if retryErr := s.Outbox.Retry(ctx, event.ID, event.LeaseToken, s.now().Add(creativeOutboxRetry), SanitizeCreativeMessage(err.Error())); retryErr != nil {
				s.warn("creative.outbox_retry_failed", "outbox_id", event.ID, "error", retryErr)
			}
			continue
		}
		if err := s.Outbox.Complete(ctx, event.ID, event.LeaseToken); err != nil {
			lastErr = err
			continue
		}
		processed++
	}
	return processed, lastErr
}
func (s *Results) ReconcileCreativeOutboxEvent(ctx context.Context, event CreativeRunOutbox) error {
	run, err := s.Repo.GetCreativeRunByRunID(ctx, event.RunID)
	if err != nil {
		if errors.Is(err, ErrCreativeRunNotFound) {
			return nil
		}
		return err
	}
	switch event.Operation {
	case CreativeRunOutboxProvision:
		return s.ReconcileCreativeProvision(ctx, run)
	case CreativeRunOutboxSettle, CreativeRunOutboxRelease:
		if IsTerminalCreativeRunStatus(run.Status) {
			return nil
		}
		if err := s.Queue.Enqueue(ctx, run.RunID); err != nil && !errors.Is(err, ErrCreativeAlreadyQueued) {
			return err
		}
		return nil
	default:
		return fmt.Errorf("unknown creative outbox operation %q", event.Operation)
	}
}
func (s *Results) ReconcileCreativeProvision(ctx context.Context, run *CreativeRun) error {
	if run == nil {
		return nil
	}
	if run.ProvisioningPhase == CreativeProvisioningPhaseEnqueued || run.ProvisioningPhase == CreativeProvisioningPhaseComplete {
		if err := s.Queue.Enqueue(ctx, run.RunID); err != nil && !errors.Is(err, ErrCreativeAlreadyQueued) {
			return err
		}
		return s.Repo.SetCreativeRunProvisioningPhase(ctx, run.RunID, CreativeProvisioningPhaseComplete)
	}
	// 只有 transient 已保存时才能在不持久化图片的前提下继续入队。
	if run.ProvisioningPhase != CreativeProvisioningPhaseTransientSaved || s.TransientStore == nil {
		return s.FailRun(ctx, run.RunID, "PROVISIONING_INCOMPLETE", "creative provisioning could not be recovered without transient input")
	}
	if _, err := s.TransientStore.LoadPayload(ctx, run.RunID); err != nil {
		return s.MarkResultLost(ctx, run.RunID, false)
	}
	if err := s.Queue.Enqueue(ctx, run.RunID); err != nil && !errors.Is(err, ErrCreativeAlreadyQueued) {
		return err
	}
	if err := s.Repo.SetCreativeRunProvisioningPhase(ctx, run.RunID, CreativeProvisioningPhaseEnqueued); err != nil {
		return err
	}
	return nil
}

// RunCreativeOutboxReconciler 启动创作台 outbox 周期恢复循环。
func (s *Results) RunCreativeOutboxReconciler(ctx context.Context) {
	if s == nil || s.Outbox == nil || s.Queue == nil {
		return
	}
	ticker := time.NewTicker(creativeOutboxPoll)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		_, _ = s.ReconcileCreativeOutboxOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// EnsureCreativeOutbox 创建或恢复一个后台补偿动作；没有配置 outbox 时保持测试替身兼容。
func (s *Results) EnsureCreativeOutbox(ctx context.Context, runID string, operation CreativeRunOutboxOperation) error {
	if s == nil || s.Outbox == nil {
		return nil
	}
	return s.Outbox.Ensure(ctx, runID, operation, s.now())
}
func (s *Results) GetRunPublic(ctx context.Context, runID string) (*CreativeRunPublic, error) {
	run, err := s.Repo.GetCreativeRunByRunID(ctx, runID)
	if err != nil {
		return nil, err
	}
	outputs, err := s.Repo.ListCreativeRunOutputs(ctx, runID)
	if err != nil {
		return nil, err
	}
	return CreativeRunToPublic(run, outputs), nil
}
func (s *Results) InvalidateCreativeAuthCache(ctx context.Context, userID int64) {
	if s != nil && s.InvalidateAuth != nil && userID > 0 {
		s.InvalidateAuth(ctx, userID)
	}
}

// MarkRunning 把任务从 queued 推进到 running 并回填账号；重复调用幂等。
func (s *Results) MarkRunning(ctx context.Context, runID string, accountID int64) error {
	if s == nil || s.Repo == nil {
		return errors.New("creative service is not configured")
	}
	run, err := s.Repo.GetCreativeRunByRunID(ctx, runID)
	if err != nil {
		return err
	}
	if run.Status == CreativeRunStatusRunning {
		return nil
	}
	if run.Status != CreativeRunStatusQueued {
		if IsTerminalCreativeRunStatus(run.Status) {
			return nil
		}
		return ErrCreativeInvalidTransition
	}
	return s.Repo.MarkCreativeRunRunning(ctx, runID, accountID, s.now())
}
func (s *Results) SucceedRun(ctx context.Context, runID string, accountID int64, results []ProviderOutput) (*CreativeRunPublic, error) {
	if s == nil || s.Repo == nil {
		return nil, errors.New("creative service is not configured")
	}
	run, err := s.Repo.GetCreativeRunByRunID(ctx, runID)
	if err != nil {
		return nil, err
	}
	if IsTerminalCreativeRunStatus(run.Status) && run.Status != CreativeRunStatusCancelled {
		return s.GetRunPublic(ctx, runID)
	}
	outputs := make([]ProviderOutput, 0, len(results))
	for _, result := range results {
		outputs = append(outputs, ProviderOutput{Index: result.Index, Success: result.Success, Bytes: result.Bytes, Mime: result.Mime, ErrorCode: result.ErrorCode, ErrorMessage: SanitizeCreativeMessage(result.ErrorMessage)})
	}
	if err := s.CreativeDelivery().Record(ctx, runID, accountID, outputs); err != nil {
		return nil, err
	}
	if s.Outbox == nil {
		if err := s.SettleRun(ctx, runID); err != nil {
			return nil, err
		}
	}
	return s.GetRunPublic(ctx, runID)
}

// CreativeDelivery 仅投影兼容依赖，不保留另一份结果状态。
func (s *Results) CreativeDelivery() ResultDelivery {
	outcomes, _ := s.Repo.(ProviderOutcomeStore)
	return ResultDelivery{Now: s.Now, Repo: s.Repo, Outcomes: outcomes, Store: s.TransientStore, TTL: s.TransientTTL}
}

// SettleRun 捕获 provider 已成功的结果并写 usage log；失败时保持 settlement_pending。
func (s *Results) SettleRun(ctx context.Context, runID string) error {
	if s == nil || s.Repo == nil {
		return errors.New("creative service is not configured")
	}
	run, err := s.Repo.GetCreativeRunByRunID(ctx, runID)
	if err != nil {
		return err
	}
	if run == nil {
		return nil
	}
	if !IsCreativeRunSettlementPending(run.Status) && (run.Status != CreativeRunStatusCancelled || run.ProviderResultRecordedAt == nil) {
		return nil
	}
	if run.Status == CreativeRunStatusReleasePending {
		return nil
	}
	if run.Status == CreativeRunStatusCancelled && run.ActualCost != nil {
		return nil
	}
	if run.Status == CreativeRunStatusProviderSucceeded {
		if err := s.Repo.TransitionCreativeRunStatus(ctx, runID, CreativeRunStatusSettlementPending, CreativeRunTransitionOptions{}); err != nil && !errors.Is(err, ErrCreativeInvalidTransition) {
			return err
		}
		run.Status = CreativeRunStatusSettlementPending
	}
	if run.AccountID == nil || *run.AccountID <= 0 {
		return ErrCreativeSettlementAccountMissing
	}
	outputs, err := s.Repo.ListCreativeRunOutputs(ctx, runID)
	if err != nil {
		return err
	}
	successCount := 0
	for _, output := range outputs {
		if output != nil && output.Status == CreativeRunOutputStatusSucceeded {
			successCount++
		}
	}
	billingResult, err := s.Funding.Capture(ctx, run, successCount)
	if err != nil {
		return err
	}
	if marker, ok := s.Repo.(CreativeRunAllowanceMarker); ok && run.AllowanceReserved {
		if err := marker.SetCreativeRunAllowanceReserved(ctx, runID, false); err != nil {
			return err
		}
		run.AllowanceReserved = false
	}
	s.InvalidateCreativeAuthCache(ctx, run.UserID)
	actualCost := 0.0
	if run.ActualCost != nil {
		actualCost = *run.ActualCost
	}
	lost, deliveryErr := s.CreativeDelivery().Lost(ctx, runID, outputs)
	if deliveryErr != nil {
		return deliveryErr
	}
	outcomes := s.CreativeDelivery().Outcomes
	if outcomes == nil {
		return errors.New("creative provider outcome store is not configured")
	}
	if err := outcomes.CompleteProviderOutcome(ctx, runID, actualCost, lost, s.now()); err != nil {
		if !errors.Is(err, ErrCreativeInvalidTransition) {
			return err
		}
	}
	// cancelled 竞态仍需写 usage log，但保持 cancelled 终态。
	s.RecordCreativeUsageLog(ctx, run, actualCost, successCount, billingResult, s.now())
	return nil
}

// ReleaseRun 释放未消耗 hold，成功后把 release_pending 推进到目标终态。
func (s *Results) ReleaseRun(ctx context.Context, runID string) error {
	if s == nil || s.Repo == nil {
		return errors.New("creative service is not configured")
	}
	run, err := s.Repo.GetCreativeRunByRunID(ctx, runID)
	if err != nil {
		return err
	}
	if run == nil || run.Status != CreativeRunStatusReleasePending {
		return nil
	}
	if err := s.Funding.Release(ctx, run); err != nil {
		return err
	}
	if marker, ok := s.Repo.(CreativeRunAllowanceMarker); ok && run.AllowanceReserved {
		if err := marker.SetCreativeRunAllowanceReserved(ctx, runID, false); err != nil {
			return err
		}
	}
	target := run.ReleaseTargetStatus
	if target == "" {
		target = CreativeRunStatusFailed
	}
	if err := s.Repo.TransitionCreativeRunStatus(ctx, runID, target, CreativeRunTransitionOptions{}); err != nil && !errors.Is(err, ErrCreativeInvalidTransition) {
		return err
	}
	s.InvalidateCreativeAuthCache(ctx, run.UserID)
	if s.TransientStore != nil {
		if err := s.TransientStore.DeleteRunTransient(ctx, runID, 0, run.RequestedOutputCount); err != nil {
			return err
		}
	}
	return nil
}

// FailRun 失败路径：先进入 release_pending，再由 ReleaseRun 完成释放和目标终态。
func (s *Results) FailRun(ctx context.Context, runID, errorCode, errorMessage string) error {
	if s == nil || s.Repo == nil {
		return nil
	}
	run, err := s.Repo.GetCreativeRunByRunID(ctx, runID)
	if err != nil {
		return err
	}
	if IsTerminalCreativeRunStatus(run.Status) {
		return nil
	}
	if run.Status == CreativeRunStatusReleasePending {
		return s.ReleaseRun(ctx, runID)
	}
	code := strings.TrimSpace(errorCode)
	if code == "" {
		code = "PROVIDER_FAILED"
	}
	message := SanitizeCreativeMessage(errorMessage)
	if err := s.Repo.TransitionCreativeRunStatus(ctx, runID, CreativeRunStatusReleasePending, CreativeRunTransitionOptions{
		ErrorCode:           &code,
		ErrorMessage:        &message,
		ReleaseTargetStatus: CreativeRunStatusFailed,
	}); err != nil && !errors.Is(err, ErrCreativeInvalidTransition) {
		return err
	}
	if err := s.EnsureCreativeOutbox(ctx, runID, CreativeRunOutboxRelease); err != nil {
		return err
	}
	if err := s.ReleaseRun(ctx, runID); err != nil {
		s.warn("creative.fail_release_failed",
			"run_id", runID,
			"error", err,
		)
		return err
	}
	return nil
}

// CancelRunByWorker 是 worker 侧处理既有 cancelled 状态的入口。
func (s *Results) CancelRunByWorker(ctx context.Context, runID string) error {
	if s == nil || s.Repo == nil {
		return nil
	}
	run, err := s.Repo.GetCreativeRunByRunID(ctx, runID)
	if err != nil {
		return err
	}
	if IsTerminalCreativeRunStatus(run.Status) {
		return nil
	}
	if run.Status == CreativeRunStatusReleasePending {
		return s.ReleaseRun(ctx, runID)
	}
	if err := s.Repo.TransitionCreativeRunStatus(ctx, runID, CreativeRunStatusReleasePending, CreativeRunTransitionOptions{
		ReleaseTargetStatus: CreativeRunStatusCancelled,
	}); err != nil && !errors.Is(err, ErrCreativeInvalidTransition) {
		return err
	}
	if err := s.EnsureCreativeOutbox(ctx, runID, CreativeRunOutboxRelease); err != nil {
		return err
	}
	if err := s.ReleaseRun(ctx, runID); err != nil {
		return err
	}
	return nil
}

// MarkResultLost 把任务标记为 result_lost。
// providerSucceeded 为 true（上游已确认成功且已捕获）时保持计费，否则释放预占。
func (s *Results) MarkResultLost(ctx context.Context, runID string, providerSucceeded bool) error {
	if s == nil || s.Repo == nil {
		return nil
	}
	run, err := s.Repo.GetCreativeRunByRunID(ctx, runID)
	if err != nil {
		return err
	}
	if run.Status == CreativeRunStatusResultLost {
		return nil
	}
	if IsTerminalCreativeRunStatus(run.Status) {
		return nil
	}
	if providerSucceeded {
		return nil
	}
	code := "RESULT_LOST"
	message := "transient result expired or worker lost before client acknowledgment"
	if err := s.Repo.TransitionCreativeRunStatus(ctx, runID, CreativeRunStatusReleasePending, CreativeRunTransitionOptions{
		ErrorCode:           &code,
		ErrorMessage:        &message,
		ReleaseTargetStatus: CreativeRunStatusResultLost,
	}); err != nil {
		return err
	}
	if err := s.EnsureCreativeOutbox(ctx, runID, CreativeRunOutboxRelease); err != nil {
		return err
	}
	return s.ReleaseRun(ctx, runID)
}

// RecordCreativeUsageLog 按成功输出数写图片用量日志（request_id = creative_settle:{runID}）。
func (s *Results) RecordCreativeUsageLog(ctx context.Context, run *CreativeRun, actualCost float64, successCount int, billingResult *billing.TaskFundsResult, createdAt time.Time) {
	if s == nil || s.RecordUsage == nil || run == nil || run.AccountID == nil || successCount <= 0 {
		return
	}
	billingMode := "image"
	imageSize := run.ImageSize
	inboundEndpoint := "/v1/creative/runs"
	upstreamEndpoint := "creative:" + run.Operation
	subscriptionAmount := 0.0
	balanceAmount := actualCost
	allocations := []billing.BillingAllocation(nil)
	if billingResult != nil {
		subscriptionAmount = billingResult.SubscriptionAmountUSD
		balanceAmount = billingResult.BalanceAmountUSD
		allocations = cloneBillingAllocations(billingResult.BillingAllocations)
	}
	billingType := usage.BillingTypeBalance
	if subscriptionAmount > 0 {
		billingType = usage.BillingTypeSubscription
	}
	if len(allocations) == 0 && actualCost > 0 {
		allocations = []billing.BillingAllocation{{Type: billing.BillingAllocationTypeBalance, AmountUSD: actualCost}}
	}
	rateMultiplier := run.BalanceRateMultiplier
	if rateMultiplier <= 0 {
		rateMultiplier = 1
	}
	usageLog := &usage.UsageLog{
		UserID:                run.UserID,
		BillingUserID:         run.UserID,
		APIKeyID:              run.APIKeyID,
		AccountID:             *run.AccountID,
		RequestID:             CreativeSettlementRequestID(run.RunID),
		Model:                 run.Model,
		RequestedModel:        run.RequestedModel,
		InboundEndpoint:       &inboundEndpoint,
		UpstreamEndpoint:      &upstreamEndpoint,
		GroupID:               &run.GroupID,
		ImageCount:            successCount,
		ImageOutputCost:       actualCost,
		TotalCost:             actualCost,
		ActualCost:            actualCost,
		SubscriptionAmountUSD: subscriptionAmount,
		BalanceAmountUSD:      balanceAmount,
		BillingAllocations:    allocations,
		SubscriptionID:        firstAllocatedSubscriptionID(allocations),
		RateMultiplier:        rateMultiplier,
		BillingType:           billingType,
		RequestType:           usage.RequestTypeSync,
		BillingMode:           &billingMode,
		ImageSize:             &imageSize,
		CreatedAt:             createdAt,
	}
	s.RecordUsage(ctx, usageLog)
}

const (
	creativeOutboxClaimLimit = 50
	creativeOutboxLease      = 2 * time.Minute
	creativeOutboxPoll       = 5 * time.Second
	creativeOutboxRetry      = 15 * time.Second
)
const (
	creativeTransientCleanupInterval = 5 * time.Minute
	creativeTransientCleanupAge      = 10 * time.Minute
	creativeTransientCleanupLimit    = 100
)

const maxCreativeErrorMessageChars = 500

// now 保持各原取时点，构造时可注入同一时钟来源。
func (s *Results) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
