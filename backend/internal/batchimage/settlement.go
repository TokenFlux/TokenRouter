// 批量结算只恢复资金与分析事实，不重新提交供应商任务。
package batchimage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/routing"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

type Settlement struct {
	Now            func() time.Time
	Repo           BatchImageRepository
	Funding        Funding
	Quote          func(context.Context, string, *int64, string) (float64, error)
	RecordUsage    func(context.Context, *usage.UsageLog)
	InvalidateAuth func(context.Context, int64)
	Retention      time.Duration
	Observe        func(string, ...any)
}

func (s *Settlement) warn(event string, values ...any) {
	if s.Observe != nil {
		s.Observe(event, values...)
	}
}

type BatchImageSettlementResult struct {
	BatchID               string
	SuccessCount          int
	FailCount             int
	ActualCost            float64
	ManifestHash          string
	RequestID             string
	SubscriptionAmountUSD float64
	BalanceAmountUSD      float64
	BillingAllocations    []billing.BillingAllocation
	AlreadySettled        bool
}

func (s *Settlement) Settle(ctx context.Context, batchID string) (*BatchImageSettlementResult, error) {
	if s == nil || s.Repo == nil || s.Funding.Store == nil || s.Quote == nil {
		return nil, ErrBatchImageSettlementBillingFailed.WithCause(errors.New("batch image settlement service is not configured"))
	}
	job, err := s.Repo.GetBatchImageJobByBatchID(ctx, batchID)
	if err != nil {
		return nil, err
	}

	manifestHash := BuildBatchImageSettlementManifestHash(job)
	result := &BatchImageSettlementResult{
		BatchID:      job.BatchID,
		SuccessCount: job.SuccessCount,
		FailCount:    job.FailCount,
		ManifestHash: manifestHash,
		RequestID:    BatchImageCaptureRequestID(job.BatchID),
	}
	if job.ActualCost != nil {
		result.ActualCost = *job.ActualCost
	}
	if job.Status == BatchImageJobStatusCompleted {
		result.AlreadySettled = true
		return result, nil
	}
	if job.Status != BatchImageJobStatusSettling {
		return nil, ErrBatchImageSettlementInvalidStatus
	}
	if job.APIKeyID == nil || *job.APIKeyID <= 0 {
		return nil, ErrBatchImageSettlementMissingAPIKeyID
	}
	if job.AccountID == nil || *job.AccountID <= 0 {
		return nil, ErrBatchImageSettlementMissingAccountID
	}
	// 重试耗尽检查必须先于各类可重复失败的校验（counts/manifest/定价/超冻结），
	// 否则这些错误路径会绕过耗尽出口，settling job 无限 requeue、冻结余额永不释放。
	if IsBatchImageSettlementRetryExhausted(job) {
		return nil, s.FailExhaustedSettlement(ctx, job, "settlement retry limit reached: "+BatchImageDerefString(job.LastErrorCode))
	}
	if job.SuccessCount < 0 || job.FailCount < 0 || job.ItemCount < 0 || job.SuccessCount+job.FailCount > job.ItemCount {
		if failErr := s.RecordSettlementFailure(ctx, job, "SETTLEMENT_INVALID_COUNTS",
			fmt.Sprintf("success=%d fail=%d item_count=%d", job.SuccessCount, job.FailCount, job.ItemCount)); failErr != nil {
			return nil, failErr
		}
		return nil, ErrBatchImageSettlementInvalidCounts
	}
	if strings.TrimSpace(BatchImageDerefString(job.ManifestHash)) != "" && BatchImageDerefString(job.ManifestHash) != manifestHash {
		if failErr := s.RecordSettlementFailure(ctx, job, "SETTLEMENT_MANIFEST_CONFLICT", "manifest hash conflict"); failErr != nil {
			return nil, failErr
		}
		return nil, ErrBatchImageSettlementManifestConflict
	}

	unitPrice, err := s.SettlementUnitPrice(ctx, job)
	if err == nil && unitPrice < 0 {
		err = ErrBatchImageSettlementPricingMissing
	}
	if err != nil {
		if failErr := s.RecordSettlementFailure(ctx, job, "SETTLEMENT_PRICING_MISSING", err.Error()); failErr != nil {
			return nil, failErr
		}
		return nil, err
	}
	actualBillingInput := float64(job.SuccessCount) * unitPrice
	actualCost := actualBillingInput
	if job.PricingSnapshotVersion >= 2 {
		if job.BaseUnitPrice < 0 {
			return nil, ErrBatchImageSettlementPricingMissing
		}
		actualBillingInput = float64(job.SuccessCount) * job.BaseUnitPrice
	}
	holdAmount := job.EstimatedCost
	if job.HoldAmount != nil {
		holdAmount = *job.HoldAmount
	}
	if job.PricingSnapshotVersion < 2 && actualCost-holdAmount > BatchImageCostEpsilon {
		msg := fmt.Sprintf("actual cost %.10f exceeds held amount %.10f", actualCost, holdAmount)
		if failErr := s.RecordSettlementFailure(ctx, job, "SETTLEMENT_COST_EXCEEDS_HOLD", msg); failErr != nil {
			return nil, failErr
		}
		return nil, ErrBatchImageSettlementCostExceedsHold
	}

	billingResult, err := s.Funding.Capture(ctx, job, actualBillingInput, manifestHash)
	if err != nil {
		msg := TruncateBatchImageMessage(err.Error(), BatchImageMaxErrorMessageLength)
		if failErr := s.RecordSettlementFailure(ctx, job, "SETTLEMENT_BILLING_FAILED", msg); failErr != nil {
			return nil, failErr
		}
		return nil, err
	}
	if billingResult != nil {
		if job.PricingSnapshotVersion >= 2 {
			actualCost = billingResult.ActualAmountUSD
		}
		result.SubscriptionAmountUSD = billingResult.SubscriptionAmountUSD
		result.BalanceAmountUSD = billingResult.BalanceAmountUSD
		result.BillingAllocations = cloneBillingAllocations(billingResult.BillingAllocations)
	}
	result.ActualCost = actualCost
	s.InvalidateAuthCache(ctx, BillingUserID(job))

	now := s.now()
	outputExpiresAt := now.Add(s.OutputRetentionAfterTerminal())
	if err := s.Repo.MarkBatchImageJobSettled(ctx, MarkBatchImageJobSettledParams{
		BatchID:         job.BatchID,
		ActualCost:      actualCost,
		ManifestHash:    manifestHash,
		Now:             &now,
		OutputExpiresAt: &outputExpiresAt,
		EventPayload: map[string]any{
			"batch_id":      job.BatchID,
			"request_id":    result.RequestID,
			"success_count": job.SuccessCount,
			"fail_count":    job.FailCount,
			"actual_cost":   actualCost,
			"manifest_hash": manifestHash,
		},
	}); err != nil {
		return nil, err
	}
	s.RecordUsageLog(ctx, job, actualCost, result.RequestID, now, billingResult)

	return result, nil
}

// IsBatchImageSettlementRetryExhausted 判断 settling job 是否已达重试上限。
// 必须覆盖所有 SETTLEMENT_* 失败码（而非仅 SETTLEMENT_BILLING_FAILED），
// 否则 SETTLEMENT_COST_EXCEEDS_HOLD / SETTLEMENT_INVALID_COUNTS 等错误会无限 requeue。
func IsBatchImageSettlementRetryExhausted(job *BatchImageJob) bool {
	return job != nil &&
		job.Status == BatchImageJobStatusSettling &&
		job.RetryCount >= BatchImageSettlementMaxRetries &&
		strings.HasPrefix(BatchImageDerefString(job.LastErrorCode), "SETTLEMENT_")
}

// RecordSettlementFailure 记录一次结算失败并递增 retry_count。
// 重试达到上限时立即走耗尽出口（释放冻结余额并转 failed）；
// 返回非 nil 时调用方应直接返回该错误。
func (s *Settlement) RecordSettlementFailure(ctx context.Context, job *BatchImageJob, code, message string) error {
	retryCount, recordErr := s.Repo.SetBatchImageJobSettlementFailed(ctx, job.BatchID, code, TruncateBatchImageMessage(message, BatchImageMaxErrorMessageLength))
	if recordErr != nil {
		s.warn("batch_image.settlement_failure_record_failed",
			"batch_id", job.BatchID,
			"code", code,
			"error", recordErr,
		)
		return nil
	}
	job.RetryCount = retryCount
	job.LastErrorCode = &code
	if retryCount >= BatchImageSettlementMaxRetries {
		return s.FailExhaustedSettlement(ctx, job, message)
	}
	return nil
}
func (s *Settlement) FailExhaustedSettlement(ctx context.Context, job *BatchImageJob, message string) error {
	if s == nil || s.Repo == nil {
		return ErrBatchImageSettlementBillingFailed
	}
	// 释放指纹必须与其余所有释放点（processor/Cancel/recovery）一致地使用 RequestHash：
	// 它们共享同一 request id，payloadHash 不同会触发 ErrUsageBillingRequestConflict，
	// 导致后续 Cancel/重试永远失败、terminal job 变成毒消息。
	if err := s.Funding.Release(ctx, job, BatchImageDerefString(job.RequestHash)); err != nil {
		msg := TruncateBatchImageMessage(err.Error(), BatchImageMaxErrorMessageLength)
		if _, recordErr := s.Repo.SetBatchImageJobSettlementFailed(ctx, job.BatchID, "SETTLEMENT_RELEASE_FAILED", msg); recordErr != nil {
			s.warn("batch_image.settlement_release_failure_record_failed",
				"batch_id", job.BatchID,
				"error", recordErr,
			)
		}
		return ErrBatchImageSettlementBillingFailed.WithCause(err)
	}
	s.InvalidateAuthCache(ctx, BillingUserID(job))
	msg := strings.TrimSpace(message)
	if msg == "" {
		msg = "settlement billing retry limit reached"
	}
	if err := s.Repo.TransitionBatchImageJobStatus(ctx, job.BatchID, BatchImageJobStatusFailed, BatchImageTransitionOptions{
		ErrorCode:    BatchImageStringPtr("SETTLEMENT_BILLING_RETRY_EXHAUSTED"),
		ErrorMessage: BatchImageStringPtr(msg),
		EventType:    "settlement_retry_exhausted",
		EventPayload: map[string]any{
			"batch_id":    job.BatchID,
			"retry_count": job.RetryCount,
		},
	}); err != nil {
		return err
	}
	return ErrBatchImageSettlementBillingFailed
}
func (s *Settlement) RecordUsageLog(ctx context.Context, job *BatchImageJob, actualCost float64, requestID string, createdAt time.Time, billingResult *billing.TaskFundsResult) {
	if s == nil || s.RecordUsage == nil || job == nil || job.APIKeyID == nil || job.AccountID == nil {
		return
	}
	billingMode := "image"
	accountRateMultiplier := job.AccountRateMultiplier
	inboundEndpoint := "/v1/images/batches"
	upstreamEndpoint := "vertex:batchPredictionJobs"
	imageSize := "1K"
	billingType := usage.BillingTypeBalance
	subscriptionAmount := 0.0
	balanceAmount := actualCost
	allocations := []billing.BillingAllocation(nil)
	if billingResult != nil {
		subscriptionAmount = billingResult.SubscriptionAmountUSD
		balanceAmount = billingResult.BalanceAmountUSD
		allocations = cloneBillingAllocations(billingResult.BillingAllocations)
	}
	if subscriptionAmount > BatchImageCostEpsilon {
		billingType = usage.BillingTypeSubscription
	}
	if len(allocations) == 0 && actualCost > 0 {
		allocations = []billing.BillingAllocation{{Type: billing.BillingAllocationTypeBalance, AmountUSD: actualCost}}
	}
	rateMultiplier := job.GroupRateMultiplier * job.BatchDiscountMultiplier
	if job.PricingSnapshotVersion >= 2 && job.SuccessCount > 0 && job.BaseUnitPrice > 0 && accountRateMultiplier > 0 {
		// 混合结算没有单一来源倍率，日志记录本次实际生效的等效分组倍率。
		rateMultiplier = actualCost / (job.BaseUnitPrice * float64(job.SuccessCount) * accountRateMultiplier)
	}
	requestedModel := BatchImageRequestedModel(job)
	internalModel := BatchImageInternalModel(job)
	usageLog := &usage.UsageLog{
		UserID:                job.UserID,
		BillingUserID:         job.BillingUserID,
		TeamID:                job.TeamID,
		APIKeyID:              *job.APIKeyID,
		AccountID:             *job.AccountID,
		RequestID:             strings.TrimSpace(requestID),
		Model:                 internalModel,
		RequestedModel:        requestedModel,
		UpstreamModel:         optionalTrimmedStringPtr(job.Model),
		ModelMappingChain:     optionalTrimmedStringPtr(routing.BuildModelMappingChain(requestedModel, internalModel, job.Model)),
		InboundEndpoint:       &inboundEndpoint,
		UpstreamEndpoint:      &upstreamEndpoint,
		ImageCount:            job.SuccessCount,
		ImageOutputCost:       actualCost,
		TotalCost:             actualCost,
		ActualCost:            actualCost,
		SubscriptionAmountUSD: subscriptionAmount,
		BalanceAmountUSD:      balanceAmount,
		BillingAllocations:    allocations,
		SubscriptionID:        firstAllocatedSubscriptionID(allocations),
		RateMultiplier:        rateMultiplier,
		AccountRateMultiplier: &accountRateMultiplier,
		BillingType:           billingType,
		RequestType:           usage.RequestTypeSync,
		BillingMode:           &billingMode,
		ImageSize:             &imageSize,
		SessionID:             job.SessionID,
		CreatedAt:             createdAt,
	}
	s.RecordUsage(ctx, usageLog)
}
func (s *Settlement) InvalidateAuthCache(ctx context.Context, userID int64) {
	if s != nil && s.InvalidateAuth != nil && userID > 0 {
		s.InvalidateAuth(ctx, userID)
	}
}
func (s *Settlement) SettlementUnitPrice(ctx context.Context, job *BatchImageJob) (float64, error) {
	if job != nil && job.PricingSnapshotVersion >= 1 {
		if job.BillableUnitPrice < 0 {
			return 0, ErrBatchImageSettlementPricingMissing
		}
		return job.BillableUnitPrice, nil
	}
	unitPrice, err := s.Quote(ctx, job.Model, job.GroupID, "1K")
	if err != nil {
		return 0, err
	}
	return unitPrice, nil
}
func (s *Settlement) OutputRetentionAfterTerminal() time.Duration {
	if s.Retention > 0 {
		return s.Retention
	}
	return 72 * time.Hour
}
func BatchImageSettlementRequestID(batchID string) string {
	return BatchImageSettlementRequestPrefix + strings.TrimSpace(batchID)
}
func BuildBatchImageSettlementManifestHash(job *BatchImageJob) string {
	if job == nil {
		return ""
	}
	parts := []string{
		strings.TrimSpace(job.BatchID),
		strings.TrimSpace(job.Provider),
		strings.TrimSpace(job.Model),
		BatchImageDerefString(job.ProviderJobName),
		BatchImageDerefString(job.ProviderOutputRef),
		strconv.Itoa(job.SuccessCount),
		strconv.Itoa(job.FailCount),
		strconv.Itoa(job.ItemCount),
	}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}
func (r *BatchImageSettlementResult) String() string {
	if r == nil {
		return ""
	}
	return fmt.Sprintf("batch_id=%s success=%d fail=%d actual_cost=%0.10f already_settled=%t",
		r.BatchID, r.SuccessCount, r.FailCount, r.ActualCost, r.AlreadySettled)
}

const (
	BatchImageSettlementRequestPrefix = "batch_image_settlement:"
	BatchImageSettlementRetryDelay    = time.Minute
	BatchImageSettlementMaxRetries    = 5
	BatchImageCostEpsilon             = 0.00000001
)

func optionalTrimmedStringPtr(raw string) *string {
	v := strings.TrimSpace(raw)
	if v == "" {
		return nil
	}
	return &v
}
func firstAllocatedSubscriptionID(values []billing.BillingAllocation) *int64 {
	for _, v := range values {
		if v.Type == billing.BillingAllocationTypeSubscription && v.SubscriptionID != nil {
			id := *v.SubscriptionID
			return &id
		}
	}
	return nil
}

// now 保持各原取时点，构造时可注入同一时钟来源。
func (s *Settlement) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
