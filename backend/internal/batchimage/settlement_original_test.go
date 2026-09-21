//go:build unit

package batchimage_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/batchimage"
	batchimageprovider "github.com/TokenFlux/TokenRouter/internal/batchimage/provider"
	billingcore "github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/usage"
	"github.com/stretchr/testify/require"
)

func TestBatchImageSettlementService_SettlesAndChargesSuccessfulImagesOnly(t *testing.T) {
	repo := newFakeBatchImageRepository()
	job := testSettlingBatchImageJob("imgbatch_settle")
	job.SuccessCount = 3
	job.FailCount = 2
	job.ItemCount = 5
	job.RequestedModel = "batch-image-alias"
	job.InternalModel = "gemini-internal-image"
	job.SessionID = batchimage.BatchImageStringPtr("batch-settlement-session")
	repo.jobs[job.BatchID] = job
	billing := &fakeBatchImageBillingRepo{}
	usageLogs := &resultUsageRepository{}
	svc := newBatchSettlementFixture(repo, billing,
		usageLogs, &fakeBatchImagePricingResolver{unitPrice: 0.25}, nil, nil)

	result, err := svc.Settle(context.Background(), job.BatchID)
	require.NoError(t, err)
	require.Equal(t, 0.75, result.ActualCost)
	require.Equal(t, batchimage.BatchImageCaptureRequestID(job.BatchID), result.RequestID)
	require.False(t, result.AlreadySettled)
	require.Equal(t, batchimage.BatchImageJobStatusCompleted, repo.jobs[job.BatchID].Status)
	require.NotNil(t, repo.jobs[job.BatchID].ActualCost)
	require.Equal(t, 0.75, *repo.jobs[job.BatchID].ActualCost)
	require.NotEmpty(t, batchimage.BatchImageDerefString(repo.jobs[job.BatchID].ManifestHash))
	require.NotNil(t, repo.jobs[job.BatchID].SettledAt)
	require.Equal(t, "batch-settlement-session", batchimage.BatchImageDerefString(usageLogs.lastLog.SessionID))
	require.Equal(t, "gemini-internal-image", usageLogs.lastLog.Model)
	require.Equal(t, "batch-image-alias", usageLogs.lastLog.RequestedModel)
	require.NotNil(t, usageLogs.lastLog.UpstreamModel)
	require.Equal(t, "gemini-image", *usageLogs.lastLog.UpstreamModel)
	require.NotNil(t, usageLogs.lastLog.ModelMappingChain)
	require.Equal(t, "batch-image-alias→gemini-internal-image→gemini-image", *usageLogs.lastLog.ModelMappingChain)
	require.Len(t, billing.captures, 1)
	require.Equal(t, int64(321), billing.captures[0].APIKeyID)
	require.Equal(t, job.UserID, billing.captures[0].UserID)
	require.Equal(t, job.BatchID, billing.captures[0].Task.ID)
	require.Equal(t, 0.75, billing.captures[0].ActualAmount)
	require.Equal(t, 1.25, billing.captures[0].HoldAmount)
	require.NotContains(t, fmt.Sprintf("%+v", billing.captures[0]), batchImageTestData)
	require.NotContains(t, fmt.Sprintf("%+v", billing.captures[0]), "gs://")
	require.NotContains(t, fmt.Sprintf("%+v", billing.captures[0]), "prompt")
}

func TestBatchImageSettlementService_RecordsSubscriptionFirstBilling(t *testing.T) {
	repo := newFakeBatchImageRepository()
	job := testSettlingBatchImageJob("imgbatch_subscription_settle")
	job.SuccessCount = 3
	job.FailCount = 2
	job.ItemCount = 5
	subscriptionID := int64(77)
	planID := int64(88)
	job.SubscriptionHoldAllocations = []billingcore.BillingAllocation{
		{
			Type:           billingcore.BillingAllocationTypeSubscription,
			AmountUSD:      0.6,
			SubscriptionID: &subscriptionID,
			PlanID:         &planID,
		},
	}
	job.BalanceHoldAmount = 0.65
	repo.jobs[job.BatchID] = job
	usageLogs := &resultUsageRepository{}
	svc := newBatchSettlementFixture(repo, &fakeBatchImageBillingRepo{},
		usageLogs, &fakeBatchImagePricingResolver{unitPrice: 0.25}, nil, nil)

	result, err := svc.Settle(context.Background(), job.BatchID)

	require.NoError(t, err)
	require.InDelta(t, 0.6, result.SubscriptionAmountUSD, 0.000001)
	require.InDelta(t, 0.15, result.BalanceAmountUSD, 0.000001)
	require.NotNil(t, usageLogs.lastLog)
	require.Equal(t, usage.BillingTypeSubscription, usageLogs.lastLog.BillingType)
	require.NotNil(t, usageLogs.lastLog.SubscriptionID)
	require.Equal(t, subscriptionID, *usageLogs.lastLog.SubscriptionID)
	require.InDelta(t, 0.6, usageLogs.lastLog.SubscriptionAmountUSD, 0.000001)
	require.InDelta(t, 0.15, usageLogs.lastLog.BalanceAmountUSD, 0.000001)
	require.Len(t, usageLogs.lastLog.BillingAllocations, 2)
}

func TestBuildBatchImageHoldCommandKeepsTeamBillingSnapshot(t *testing.T) {
	job := testSettlingBatchImageJob("imgbatch_team_snapshot")
	teamID := int64(77)
	job.UserID = 23
	job.BillingUserID = 11
	job.TeamID = &teamID

	cmd, err := batchimage.BuildHoldCommand(job, batchimage.BatchImageCaptureRequestID(job.BatchID), 0.5, "payload-hash")
	require.NoError(t, err)
	require.Equal(t, int64(11), cmd.UserID)
	require.Equal(t, int64(23), cmd.ActorUserID)
	require.NotNil(t, cmd.TeamID)
	require.Equal(t, teamID, *cmd.TeamID)
}

func TestBatchImageBalanceHoldKeepsAPIKeyBillingSnapshot(t *testing.T) {
	job := testSettlingBatchImageJob("imgbatch_api_key_billing_snapshot")
	preferredSubscriptionID := int64(89)
	job.BillingMode = apikey.APIKeyBillingModeSubscription
	job.PreferredSubscriptionID = &preferredSubscriptionID
	job.PricingSnapshotVersion = 3
	billing := &fakeBatchImageBillingRepo{}

	require.NoError(t, (batchimage.Funding{Store: billing}).Reserve(context.Background(), job, nil, "reserve-hash"))
	_, err := (batchimage.Funding{Store: billing}).Capture(context.Background(), job, 0.5, "capture-hash")
	require.NoError(t, err)
	require.NoError(t, (batchimage.Funding{Store: billing}).Release(context.Background(), job, "release-hash"))

	commands := []*billingcore.TaskFundsCommand{
		billing.reserves[0],
		billing.captures[0],
		billing.releases[0],
	}
	for _, command := range commands {
		require.Equal(t, apikey.APIKeyBillingModeSubscription, command.APIKeyBillingMode)
		require.NotNil(t, command.PreferredSubscriptionID)
		require.Equal(t, preferredSubscriptionID, *command.PreferredSubscriptionID)
	}
}

func TestBatchImageSettlementService_ZeroSuccessCanComplete(t *testing.T) {
	repo := newFakeBatchImageRepository()
	job := testSettlingBatchImageJob("imgbatch_zero")
	job.SuccessCount = 0
	job.FailCount = 4
	job.ItemCount = 4
	repo.jobs[job.BatchID] = job
	billing := &fakeBatchImageBillingRepo{}
	svc := newBatchSettlementFixture(repo, billing, nil, &fakeBatchImagePricingResolver{unitPrice: 0.25}, nil, nil)

	result, err := svc.Settle(context.Background(), job.BatchID)
	require.NoError(t, err)
	require.Equal(t, 0.0, result.ActualCost)
	require.Equal(t, batchimage.BatchImageJobStatusCompleted, repo.jobs[job.BatchID].Status)
	require.Len(t, billing.captures, 1)
	require.Equal(t, 0.0, billing.captures[0].ActualAmount)
}

func TestBatchImageSettlementService_CompletedJobReturnsAlreadySettledWithoutBilling(t *testing.T) {
	repo := newFakeBatchImageRepository()
	job := testSettlingBatchImageJob("imgbatch_done")
	job.Status = batchimage.BatchImageJobStatusCompleted
	cost := 0.5
	job.ActualCost = &cost
	repo.jobs[job.BatchID] = job
	billing := &fakeBatchImageBillingRepo{}
	svc := newBatchSettlementFixture(repo, billing, nil, &fakeBatchImagePricingResolver{unitPrice: 0.25}, nil, nil)

	result, err := svc.Settle(context.Background(), job.BatchID)
	require.NoError(t, err)
	require.True(t, result.AlreadySettled)
	require.Equal(t, 0.5, result.ActualCost)
	require.Empty(t, billing.captures)
}

func TestBatchImageSettlementService_IdempotentAfterBillingCrash(t *testing.T) {
	repo := newFakeBatchImageRepository()
	job := testSettlingBatchImageJob("imgbatch_crash")
	repo.jobs[job.BatchID] = job
	billing := &fakeBatchImageBillingRepo{alreadyApplied: map[string]bool{batchimage.BatchImageCaptureRequestID(job.BatchID): true}}
	svc := newBatchSettlementFixture(repo, billing, nil, &fakeBatchImagePricingResolver{unitPrice: 0.25}, nil, nil)

	result, err := svc.Settle(context.Background(), job.BatchID)
	require.NoError(t, err)
	require.Equal(t, 0.5, result.ActualCost)
	require.Equal(t, batchimage.BatchImageJobStatusCompleted, repo.jobs[job.BatchID].Status)
	require.Len(t, billing.captures, 1)
}

func TestBatchImageSettlementService_ValidationErrors(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*batchimage.BatchImageJob)
		pricing batchimage.ImagePricer
		want    error
	}{
		{name: "invalid_status", mutate: func(j *batchimage.BatchImageJob) { j.Status = batchimage.BatchImageJobStatusRunning }, want: batchimage.ErrBatchImageSettlementInvalidStatus},
		{name: "negative_success_count", mutate: func(j *batchimage.BatchImageJob) { j.SuccessCount = -1 }, want: batchimage.ErrBatchImageSettlementInvalidCounts},
		{name: "negative_fail_count", mutate: func(j *batchimage.BatchImageJob) { j.FailCount = -1 }, want: batchimage.ErrBatchImageSettlementInvalidCounts},
		{name: "counts_exceed_item_count", mutate: func(j *batchimage.BatchImageJob) { j.SuccessCount = 2; j.FailCount = 2; j.ItemCount = 3 }, want: batchimage.ErrBatchImageSettlementInvalidCounts},
		{name: "missing_api_key", mutate: func(j *batchimage.BatchImageJob) { j.APIKeyID = nil }, want: batchimage.ErrBatchImageSettlementMissingAPIKeyID},
		{name: "missing_account", mutate: func(j *batchimage.BatchImageJob) { j.AccountID = nil }, want: batchimage.ErrBatchImageSettlementMissingAccountID},
		{name: "pricing_missing", pricing: &fakeBatchImagePricingResolver{err: batchimage.ErrBatchImageSettlementPricingMissing}, want: batchimage.ErrBatchImageSettlementPricingMissing},
		{name: "manifest_conflict", mutate: func(j *batchimage.BatchImageJob) { v := "different"; j.ManifestHash = &v }, want: batchimage.ErrBatchImageSettlementManifestConflict},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newFakeBatchImageRepository()
			job := testSettlingBatchImageJob("imgbatch_" + tt.name)
			if tt.mutate != nil {
				tt.mutate(job)
			}
			repo.jobs[job.BatchID] = job
			pricing := tt.pricing
			if pricing == nil {
				pricing = &fakeBatchImagePricingResolver{unitPrice: 0.25}
			}
			billing := &fakeBatchImageBillingRepo{}
			svc := newBatchSettlementFixture(repo, billing, nil, pricing, nil, nil)

			_, err := svc.Settle(context.Background(), job.BatchID)
			require.ErrorIs(t, err, tt.want)
			require.Empty(t, billing.captures)
			require.NotEqual(t, batchimage.BatchImageJobStatusCompleted, repo.jobs[job.BatchID].Status)
		})
	}
}

func TestBatchImageSettlementService_CostExceedingHoldDoesNotCharge(t *testing.T) {
	repo := newFakeBatchImageRepository()
	job := testSettlingBatchImageJob("imgbatch_cost_over_hold")
	job.SuccessCount = 2
	job.FailCount = 0
	job.ItemCount = 2
	holdAmount := 0.5
	job.HoldAmount = &holdAmount
	job.EstimatedCost = holdAmount
	repo.jobs[job.BatchID] = job
	billing := &fakeBatchImageBillingRepo{}
	svc := newBatchSettlementFixture(repo, billing, nil, &fakeBatchImagePricingResolver{unitPrice: 0.50}, nil, nil)

	_, err := svc.Settle(context.Background(), job.BatchID)
	require.ErrorIs(t, err, batchimage.ErrBatchImageSettlementCostExceedsHold)
	require.Empty(t, billing.captures)
	require.Equal(t, batchimage.BatchImageJobStatusSettling, repo.jobs[job.BatchID].Status)
	require.Equal(t, "SETTLEMENT_COST_EXCEEDS_HOLD", batchimage.BatchImageDerefString(repo.jobs[job.BatchID].LastErrorCode))
}

func TestBatchImageSettlementService_UsesSubmittedPricingSnapshot(t *testing.T) {
	repo := newFakeBatchImageRepository()
	job := testSettlingBatchImageJob("imgbatch_snapshot")
	job.SuccessCount = 2
	job.FailCount = 0
	job.ItemCount = 2
	job.PricingSnapshotVersion = 1
	job.BaseUnitPrice = 0.25
	job.GroupRateMultiplier = 1
	job.AccountRateMultiplier = 1
	job.BatchDiscountMultiplier = 1
	job.HoldMultiplier = 1.1
	job.BillableUnitPrice = 0.25
	job.HoldUnitPrice = 0.275
	holdAmount := 0.55
	job.HoldAmount = &holdAmount
	job.EstimatedCost = 0.5
	repo.jobs[job.BatchID] = job
	billing := &fakeBatchImageBillingRepo{}
	svc := newBatchSettlementFixture(repo, billing, nil, &fakeBatchImagePricingResolver{unitPrice: 0.50}, nil, nil)

	result, err := svc.Settle(context.Background(), job.BatchID)
	require.NoError(t, err)
	require.InDelta(t, 0.5, result.ActualCost, 1e-12)
	require.Len(t, billing.captures, 1)
	require.InDelta(t, 0.5, billing.captures[0].ActualAmount, 1e-12)
	require.InDelta(t, 0.55, billing.captures[0].HoldAmount, 1e-12)
}

func TestBatchImageSettlementService_BillingFailureLeavesSettlingAndRecordsError(t *testing.T) {
	repo := newFakeBatchImageRepository()
	job := testSettlingBatchImageJob("imgbatch_billing_fail")
	repo.jobs[job.BatchID] = job
	billing := &fakeBatchImageBillingRepo{err: errors.New("temporary billing timeout with gs://hidden-output")}
	svc := newBatchSettlementFixture(repo, billing, nil, &fakeBatchImagePricingResolver{unitPrice: 0.25}, nil, nil)

	_, err := svc.Settle(context.Background(), job.BatchID)
	require.ErrorIs(t, err, batchimage.ErrBatchImageSettlementBillingFailed)
	require.Equal(t, batchimage.BatchImageJobStatusSettling, repo.jobs[job.BatchID].Status)
	require.Equal(t, "SETTLEMENT_BILLING_FAILED", batchimage.BatchImageDerefString(repo.jobs[job.BatchID].LastErrorCode))
	require.Contains(t, batchimage.BatchImageDerefString(repo.jobs[job.BatchID].LastErrorMessage), "temporary billing timeout")
	require.NotNil(t, billing.captures[0])
}

func TestBatchImagePipelineProcessor_SettlesQueuedSettlingJob(t *testing.T) {
	repo := newFakeBatchImageRepository()
	job := testSettlingBatchImageJob("imgbatch_pipeline")
	repo.jobs[job.BatchID] = job
	billing := &fakeBatchImageBillingRepo{}
	settlement := newBatchSettlementFixture(repo, billing, nil, &fakeBatchImagePricingResolver{unitPrice: 0.25}, nil, nil)
	processor := &batchimage.PipelineProcessor{
		ProviderProcessor: newBatchProcessorFixture(repo, batchimage.NewRegistry[batchimageprovider.BatchImageProvider](&fakeProcessorProvider{}), &fakeBatchImageAccountResolver{account: &accountcore.Record{}}, nil, nil, nil, 0),
		SettlementService: settlement,
	}

	result, err := processor.Process(context.Background(), job.BatchID)
	require.NoError(t, err)
	require.True(t, result.Terminal)
	require.Equal(t, batchimage.BatchImageJobStatusCompleted, repo.jobs[job.BatchID].Status)
	require.Len(t, billing.captures, 1)
}

func TestBatchImagePipelineProcessor_RequeuesTransientSettlementFailure(t *testing.T) {
	repo := newFakeBatchImageRepository()
	job := testSettlingBatchImageJob("imgbatch_pipeline_retry")
	repo.jobs[job.BatchID] = job
	settlement := newBatchSettlementFixture(repo, &fakeBatchImageBillingRepo{err: errors.New("temporary")}, nil, &fakeBatchImagePricingResolver{unitPrice: 0.25}, nil, nil)
	processor := &batchimage.PipelineProcessor{
		ProviderProcessor: newBatchProcessorFixture(repo, batchimage.NewRegistry[batchimageprovider.BatchImageProvider](&fakeProcessorProvider{}), &fakeBatchImageAccountResolver{account: &accountcore.Record{}}, nil, nil, nil, 0),
		SettlementService: settlement,
	}

	result, err := processor.Process(context.Background(), job.BatchID)
	require.NoError(t, err)
	require.False(t, result.Terminal)
	require.Equal(t, batchimage.BatchImageSettlementRetryDelay, result.RequeueAfter)
	require.Equal(t, batchimage.BatchImageJobStatusSettling, repo.jobs[job.BatchID].Status)
}

func TestBatchImagePipelineProcessor_FailsAndReleasesAfterSettlementRetryLimit(t *testing.T) {
	repo := newFakeBatchImageRepository()
	job := testSettlingBatchImageJob("imgbatch_pipeline_retry_exhausted")
	job.RetryCount = batchimage.BatchImageSettlementMaxRetries - 1
	repo.jobs[job.BatchID] = job
	billing := &fakeBatchImageBillingRepo{captureErr: errors.New("temporary billing timeout")}
	settlement := newBatchSettlementFixture(repo, billing, nil, &fakeBatchImagePricingResolver{unitPrice: 0.25}, nil, nil)
	processor := &batchimage.PipelineProcessor{
		ProviderProcessor: newBatchProcessorFixture(repo, batchimage.NewRegistry[batchimageprovider.BatchImageProvider](&fakeProcessorProvider{}), &fakeBatchImageAccountResolver{account: &accountcore.Record{}}, nil, nil, nil, 0),
		SettlementService: settlement,
	}

	result, err := processor.Process(context.Background(), job.BatchID)
	require.NoError(t, err)
	require.True(t, result.Terminal)
	require.Equal(t, batchimage.BatchImageJobStatusFailed, repo.jobs[job.BatchID].Status)
	require.Equal(t, "SETTLEMENT_BILLING_RETRY_EXHAUSTED", batchimage.BatchImageDerefString(repo.jobs[job.BatchID].LastErrorCode))
	require.Len(t, billing.captures, 1)
	require.Len(t, billing.releases, 1)
	require.Equal(t, batchimage.BatchImageReleaseRequestID(job.BatchID), billing.releases[0].RequestID)
}

func TestBatchImageSettlementRetryExhaustedReleaseIsIdempotentAfterTransitionFailure(t *testing.T) {
	repo := newFakeBatchImageRepository()
	job := testSettlingBatchImageJob("imgbatch_retry_exhausted_transition_fail")
	job.RetryCount = batchimage.BatchImageSettlementMaxRetries
	job.LastErrorCode = batchimage.BatchImageStringPtr("SETTLEMENT_BILLING_FAILED")
	repo.jobs[job.BatchID] = job
	repo.transitionErr = errors.New("temporary transition failure")
	billing := &fakeBatchImageBillingRepo{}
	svc := newBatchSettlementFixture(repo, billing, nil, &fakeBatchImagePricingResolver{unitPrice: 0.25}, nil, nil)

	_, err := svc.Settle(context.Background(), job.BatchID)
	require.ErrorContains(t, err, "temporary transition failure")
	require.Equal(t, batchimage.BatchImageJobStatusSettling, repo.jobs[job.BatchID].Status)
	require.Len(t, billing.releases, 1)
	require.Len(t, billing.seen, 1)

	repo.transitionErr = nil
	_, err = svc.Settle(context.Background(), job.BatchID)
	require.ErrorIs(t, err, batchimage.ErrBatchImageSettlementBillingFailed)
	require.Equal(t, batchimage.BatchImageJobStatusFailed, repo.jobs[job.BatchID].Status)
	require.Len(t, billing.releases, 2)
	require.Equal(t, billing.releases[0].RequestID, billing.releases[1].RequestID)
	require.Len(t, billing.seen, 1)
}

func TestBatchImageSettlementService_CostExceedsHoldExhaustsAndReleases(t *testing.T) {
	repo := newFakeBatchImageRepository()
	job := testSettlingBatchImageJob("imgbatch_over_hold_exhausted")
	job.SuccessCount = 2
	job.FailCount = 0
	job.ItemCount = 2
	holdAmount := 0.5
	job.HoldAmount = &holdAmount
	job.EstimatedCost = holdAmount
	requestHash := "request-hash-over-hold"
	job.RequestHash = &requestHash
	repo.jobs[job.BatchID] = job
	billing := &fakeBatchImageBillingRepo{}
	svc := newBatchSettlementFixture(repo, billing, nil, &fakeBatchImagePricingResolver{unitPrice: 0.50}, nil,

		// 前 N-1 次：记录失败并返回错误（等待 worker 重试）。
		nil)

	for i := 0; i < batchimage.BatchImageSettlementMaxRetries-1; i++ {
		_, err := svc.Settle(context.Background(), job.BatchID)
		require.ErrorIs(t, err, batchimage.ErrBatchImageSettlementCostExceedsHold)
		require.Equal(t, batchimage.BatchImageJobStatusSettling, repo.jobs[job.BatchID].Status)
	}
	// 达到上限：必须走耗尽出口释放冻结并转 failed，而不是无限 requeue。
	_, err := svc.Settle(context.Background(), job.BatchID)
	require.ErrorIs(t, err, batchimage.ErrBatchImageSettlementBillingFailed)
	require.Equal(t, batchimage.BatchImageJobStatusFailed, repo.jobs[job.BatchID].Status)
	require.Empty(t, billing.captures)
	require.Len(t, billing.releases, 1)
	require.Equal(t, batchimage.BatchImageReleaseRequestID(job.BatchID), billing.releases[0].RequestID)
	// 释放指纹必须与 processor/Cancel/recovery 一致地使用 RequestHash，
	// 否则共享同一 request id 的后续释放会命中指纹冲突（毒消息）。
	require.Equal(t, requestHash, billing.releases[0].RequestPayloadHash)
}

func TestBatchImageSettlementService_InvalidCountsExhaustsAndReleases(t *testing.T) {
	repo := newFakeBatchImageRepository()
	job := testSettlingBatchImageJob("imgbatch_bad_counts_exhausted")
	job.SuccessCount = 2
	job.FailCount = 2
	job.ItemCount = 3
	requestHash := "request-hash-bad-counts"
	job.RequestHash = &requestHash
	repo.jobs[job.BatchID] = job
	billing := &fakeBatchImageBillingRepo{}
	svc := newBatchSettlementFixture(repo, billing, nil, &fakeBatchImagePricingResolver{unitPrice: 0.25}, nil, nil)

	for i := 0; i < batchimage.BatchImageSettlementMaxRetries-1; i++ {
		_, err := svc.Settle(context.Background(), job.BatchID)
		require.ErrorIs(t, err, batchimage.ErrBatchImageSettlementInvalidCounts)
	}
	_, err := svc.Settle(context.Background(), job.BatchID)
	require.ErrorIs(t, err, batchimage.ErrBatchImageSettlementBillingFailed)
	require.Equal(t, batchimage.BatchImageJobStatusFailed, repo.jobs[job.BatchID].Status)
	require.Empty(t, billing.captures)
	require.Len(t, billing.releases, 1)
	require.Equal(t, requestHash, billing.releases[0].RequestPayloadHash)
}

func TestReleaseBatchImageBalanceHold_TreatsFingerprintConflictAsReleased(t *testing.T) {
	job := testSettlingBatchImageJob("imgbatch_release_conflict")
	// 历史版本用 manifestHash 释放过一次：同一 request id 再以 RequestHash
	// 释放会命中指纹冲突。资金已归还，必须视为幂等成功而非毒消息。
	billing := &fakeBatchImageBillingRepo{releaseErr: billingcore.ErrUsageBillingRequestConflict}
	err := (batchimage.Funding{Store: billing}).Release(context.Background(), job, "request-hash")
	require.NoError(t, err)
	require.Len(t, billing.releases, 1)
}

func TestBatchImageSettlementManifestHash(t *testing.T) {
	job := testSettlingBatchImageJob("imgbatch_hash")
	first := batchimage.BuildBatchImageSettlementManifestHash(job)
	job.CreatedAt = job.CreatedAt.AddDate(0, 0, 1)
	job.UpdatedAt = job.UpdatedAt.AddDate(0, 0, 1)
	require.Equal(t, first, batchimage.BuildBatchImageSettlementManifestHash(job))

	job.SuccessCount++
	require.NotEqual(t, first, batchimage.BuildBatchImageSettlementManifestHash(job))

	job.SuccessCount--
	promptOrBase64 := first + " prompt " + batchImageTestData
	require.NotContains(t, batchimage.BuildBatchImageSettlementManifestHash(job), promptOrBase64)
}

func TestBatchImageSettlementBillingRequestIDs(t *testing.T) {
	repo := newFakeBatchImageRepository()
	first := testSettlingBatchImageJob("imgbatch_unique_1")
	second := testSettlingBatchImageJob("imgbatch_unique_2")
	repo.jobs[first.BatchID] = first
	repo.jobs[second.BatchID] = second
	billing := &fakeBatchImageBillingRepo{}
	svc := newBatchSettlementFixture(repo, billing, nil, &fakeBatchImagePricingResolver{unitPrice: 0.25}, nil, nil)

	_, err := svc.Settle(context.Background(), first.BatchID)
	require.NoError(t, err)
	_, err = svc.Settle(context.Background(), first.BatchID)
	require.NoError(t, err)
	_, err = svc.Settle(context.Background(), second.BatchID)
	require.NoError(t, err)

	require.Len(t, billing.captures, 2)
	require.Equal(t, batchimage.BatchImageCaptureRequestID(first.BatchID), billing.captures[0].RequestID)
	require.Equal(t, batchimage.BatchImageCaptureRequestID(second.BatchID), billing.captures[1].RequestID)
	require.NotEqual(t, billing.captures[0].RequestID, billing.captures[1].RequestID)
	require.Len(t, billing.seen, 2)
}

func testSettlingBatchImageJob(batchID string) *batchimage.BatchImageJob {
	apiKeyID := int64(321)
	accountID := int64(654)
	providerJobName := "providers/job"
	outputRef := "files/output"
	holdAmount := 1.25
	holdID := batchimage.BatchImageHoldRequestID(batchID)
	return &batchimage.BatchImageJob{
		BatchID:           batchID,
		UserID:            123,
		APIKeyID:          &apiKeyID,
		AccountID:         &accountID,
		Provider:          batchimage.BatchImageProviderGeminiAPI,
		Model:             "gemini-image",
		Status:            batchimage.BatchImageJobStatusSettling,
		ProviderJobName:   &providerJobName,
		ProviderOutputRef: &outputRef,
		ItemCount:         3,
		SuccessCount:      2,
		FailCount:         1,
		EstimatedCost:     holdAmount,
		HoldAmount:        &holdAmount,
		HoldID:            &holdID,
	}
}

type fakeBatchImagePricingResolver struct {
	unitPrice     float64
	missingModels map[string]bool
	err           error
	models        []string
}

func (r *fakeBatchImagePricingResolver) BatchImageUnitPrice(_ context.Context, input batchimage.BatchImagePriceInput) (float64, error) {
	if input.Model != "" {
		r.models = append(r.models, input.Model)
	}
	if r.err != nil {
		return 0, r.err
	}
	if input.Model != "" && r.missingModels[input.Model] {
		return 0, batchimage.ErrBatchImageSettlementPricingMissing
	}
	return r.unitPrice, nil
}

type fakeBatchImageBillingRepo struct {
	usableSubscription *billingcore.UserSubscription
	subscriptionErr    error
	reserves           []*billingcore.TaskFundsCommand
	captures           []*billingcore.TaskFundsCommand
	releases           []*billingcore.TaskFundsCommand
	seen               map[string]struct{}
	alreadyApplied     map[string]bool

	err        error
	reserveErr error
	captureErr error
	releaseErr error
}

func (r *fakeBatchImageBillingRepo) Reserve(_ context.Context, cmd *billingcore.TaskFundsCommand) (*billingcore.TaskFundsResult, error) {
	if r.reserveErr != nil {
		r.reserves = append(r.reserves, cmd)
		return nil, r.reserveErr
	}
	result, err := r.applyHold(cmd, &r.reserves)
	if err == nil && result != nil {
		result.BalanceAmountUSD = cmd.HoldAmount
		result.HoldAmountUSD = cmd.HoldAmount
		result.EstimatedAmountUSD = cmd.HoldAmount
		if cmd.PricingSnapshotVersion >= 2 {
			result.EstimatedAmountUSD = cmd.HoldAmount * cmd.SettlementRateScale
		}
		if cmd.HoldAmount > 0 {
			result.BillingAllocations = []billingcore.BillingAllocation{{Type: billingcore.BillingAllocationTypeBalance, AmountUSD: cmd.HoldAmount}}
		}
	}
	return result, err
}

func (r *fakeBatchImageBillingRepo) Capture(_ context.Context, cmd *billingcore.TaskFundsCommand) (*billingcore.TaskFundsResult, error) {
	if r.captureErr != nil {
		r.captures = append(r.captures, cmd)
		return nil, r.captureErr
	}
	result, err := r.applyHold(cmd, &r.captures)
	if err != nil || result == nil {
		return result, err
	}
	plan, err := billingcore.PlanTaskCapture(cmd)
	if err != nil {
		return nil, err
	}
	cmd.ActualAmount = plan.ActualAmountUSD
	result.SubscriptionAmountUSD = plan.SubscriptionAmountUSD
	result.BalanceAmountUSD = plan.BalanceAmountUSD
	result.ActualAmountUSD = plan.ActualAmountUSD
	result.BillingAllocations = plan.BillingAllocations
	return result, nil
}

func (r *fakeBatchImageBillingRepo) Release(_ context.Context, cmd *billingcore.TaskFundsCommand) (*billingcore.TaskFundsResult, error) {
	if r.releaseErr != nil {
		r.releases = append(r.releases, cmd)
		return nil, r.releaseErr
	}
	return r.applyHold(cmd, &r.releases)
}

func (r *fakeBatchImageBillingRepo) applyHold(cmd *billingcore.TaskFundsCommand, calls *[]*billingcore.TaskFundsCommand) (*billingcore.TaskFundsResult, error) {
	if r.seen == nil {
		r.seen = make(map[string]struct{})
	}
	if r.err != nil {
		*calls = append(*calls, cmd)
		return nil, r.err
	}
	if cmd != nil {
		cmd.Normalize()
		if _, ok := r.seen[cmd.RequestID]; ok || r.alreadyApplied[cmd.RequestID] {
			*calls = append(*calls, cmd)
			return &billingcore.TaskFundsResult{Applied: false}, nil
		}
		r.seen[cmd.RequestID] = struct{}{}
	}
	*calls = append(*calls, cmd)
	return &billingcore.TaskFundsResult{Applied: true}, nil
}

var _ batchimage.FundingStore = (*fakeBatchImageBillingRepo)(nil)
var _ batchimage.ImagePricer = (*fakeBatchImagePricingResolver)(nil)

func (r *fakeBatchImageBillingRepo) ResolveUsableSubscriptionForGroup(context.Context, int64, int64) (*billingcore.UserSubscription, error) {
	return r.usableSubscription, r.subscriptionErr
}
