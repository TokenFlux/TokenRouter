package batchimage_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/batchimage"
	batchimageprovider "github.com/TokenFlux/TokenRouter/internal/batchimage/provider"
	billingcore "github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/gateway/modeltrace"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	testassert "github.com/TokenFlux/TokenRouter/internal/testutil/assertion"
)

// TestBatchImageSubmissionRanksBothBackends supplies competing Gemini and Vertex
// records. New jobs follow ascending priority and ID across authentication types,
// while an explicit platform restricts selection to that backend.
func TestBatchImageSubmissionRanksBothBackends(t *testing.T) {
	for _, tc := range []struct {
		name           string // selection scenario name
		geminiPriority int    // API key numeric priority
		vertexPriority int    // service account numeric priority
		geminiID       int64  // API key provider identity
		vertexID       int64  // service account provider identity
		platform       string // requested backend restriction
		wantID         int64  // expected bound provider identity
		wantPlatform   string // expected bound backend identity
	}{
		{"Vertex priority", 20, 10, 101, 202, "", 202, batchimage.BatchImageProviderVertex},
		{"Gemini priority", 10, 20, 101, 202, "", 101, batchimage.BatchImageProviderGeminiAPI},
		{"ID tie", 10, 10, 202, 101, "", 101, batchimage.BatchImageProviderVertex},
		{"explicit Gemini", 20, 10, 101, 202, batchimage.BatchImageProviderGeminiAPI, 101, batchimage.BatchImageProviderGeminiAPI},
		{"explicit Vertex", 10, 20, 101, 202, batchimage.BatchImageProviderVertex, 202, batchimage.BatchImageProviderVertex},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo, _, _, _, _ := newTestBatchImagePublicService()
			source := testassert.MustType[*publicBatchImageProviderRepo](testassert.MustType[*batchProviderFixture](svc.ProviderRepo).source)
			apiKey := testBatchImageProvider(tc.geminiID, capability.ProviderTypeAPIKey)
			apiKey.Priority = tc.geminiPriority
			serviceAccount := testBatchImageProvider(tc.vertexID, capability.ProviderTypeServiceAccount)
			serviceAccount.Priority = tc.vertexPriority
			source.providers = []providercore.Record{serviceAccount, apiKey}
			req := validBatchImageSubmitRequest()
			req.Platform = tc.platform
			job, err := svc.Submit(context.Background(), testBatchImageOwner(), req, "")
			require.NoError(t, err)
			require.Equal(t, tc.wantPlatform, job.Platform)
			require.Equal(t, tc.wantID, *repo.jobs[job.ID].ProviderID)
		})
	}
}

// TestBatchImagePublicService_SubmitCompensation exercises item persistence errors,
// upstream errors and unusable upstream names after funds have been reserved.
// It checks compensation order, cancelled context reuse, error precedence and
// the visibility of records retained for billing retries.
func TestBatchImagePublicService_SubmitCompensation(t *testing.T) {
	stages := []struct {
		name        string                       // scenario display name
		itemErr     error                        // injected item persistence failure
		providerErr error                        // injected upstream submission error
		providerJob *batchimage.BatchProviderJob // returned unusable upstream identity
		wantErr     error                        // expected public error category
		wantCode    string                       // expected persisted diagnostic code
		wantMessage string                       // expected sanitized diagnostic text
	}{
		{
			name:        "item creation",
			itemErr:     errors.New("projects/private/item persistence failed"),
			wantErr:     batchimage.ErrBatchImageQueueFailed,
			wantCode:    "ITEM_CREATE_FAILED",
			wantMessage: "upstream platform operation failed",
		},
		{
			name:        "upstream error",
			providerErr: errors.New("projects/private/upstream failed"),
			wantErr:     batchimage.ErrBatchImageProviderSubmitFailed,
			wantCode:    "PROVIDER_SUBMIT_FAILED",
			wantMessage: "upstream platform operation failed",
		},
		{
			name:        "upstream credential error",
			providerErr: batchimage.ErrBatchImageProviderMissingServiceAccount,
			wantErr:     batchimage.ErrBatchImageProviderMissingServiceAccount,
			wantCode:    "BATCH_IMAGE_PROVIDER_MISSING_SERVICE_ACCOUNT",
			wantMessage: batchimage.ErrBatchImageProviderMissingServiceAccount.Error(),
		},
		{
			name:        "blank upstream name",
			providerJob: &batchimage.BatchProviderJob{ProviderJobName: " \t "},
			wantErr:     batchimage.ErrBatchImageProviderSubmitFailed,
			wantCode:    "PROVIDER_SUBMIT_FAILED",
			wantMessage: "platform job name missing",
		},
		{
			name:        "nil upstream job",
			wantErr:     batchimage.ErrBatchImageProviderSubmitFailed,
			wantCode:    "PROVIDER_SUBMIT_FAILED",
			wantMessage: "platform job name missing",
		},
	}
	for _, stage := range stages {
		t.Run(stage.name, func(t *testing.T) {
			for _, failure := range []struct {
				name       string // compensation failure scenario name
				releaseErr error  // injected funding release failure
				recordErr  error  // injected diagnostic persistence failure
				hideErr    error  // injected record hiding failure
				queueErr   error  // injected retry publication failure
			}{
				{name: "success"},
				{name: "record failure", recordErr: errors.New("record unavailable")},
				{name: "hide failure", hideErr: errors.New("hide unavailable")},
				{name: "release failure", releaseErr: errors.New("funding unavailable")},
				{name: "release and record failure", releaseErr: errors.New("funding unavailable"), recordErr: errors.New("record unavailable")},
				{name: "release and queue failure", releaseErr: errors.New("funding unavailable"), queueErr: errors.New("queue unavailable")},
			} {
				t.Run(failure.name, func(t *testing.T) {
					svc, repo, queue, gemini, _, authCache := newTestBatchImagePublicService()
					ctx, cancel := context.WithCancel(routing.WithModelAttributes(context.Background(), svc.Attributes))
					defer cancel()
					var calls []string
					onCall := func(gotCtx context.Context, operation string) {
						require.Same(t, ctx, gotCtx)
						if operation != "items" && operation != "submit" {
							require.ErrorIs(t, gotCtx.Err(), context.Canceled)
						}
						calls = append(calls, operation)
					}
					observeError := func(err error) error {
						if err == nil {
							return nil
						}
						return submitCompensationError{err: err, onError: func() { onCall(ctx, "describe") }}
					}
					writes := &submitCompensationRepository{
						fakeBatchImageRepository: repo,
						onCall:                   onCall,
						cancel:                   cancel,
						itemErr:                  observeError(stage.itemErr),
						recordErr:                failure.recordErr,
						hideErr:                  failure.hideErr,
					}
					svc.Repo = writes
					funds := testassert.MustType[*fakeBatchImageBillingRepo](taskFixtureBilling(svc))
					funds.releaseErr = failure.releaseErr
					svc.Funding.Store = &submitCompensationFunding{fakeBatchImageBillingRepo: funds, onCall: onCall}
					queue.err = failure.queueErr
					svc.Queue = &submitCompensationQueue{publicBatchImageQueue: queue, onCall: onCall}
					gemini.submitErr = observeError(stage.providerErr)
					provider := &submitCompensationProvider{
						publicBatchImageProvider: gemini,
						onCall:                   onCall,
						cancel:                   cancel,
						result:                   stage.providerJob,
					}
					providers := testassert.MustType[*batchProviderFixture](svc.ProviderRepo)
					providers.registry = batchimage.NewRegistry[batchimageprovider.BatchImageProvider](provider)
					req := validBatchImageSubmitRequest()
					req.TaskName = "compensation"

					got, err := svc.Submit(ctx, testBatchImageOwner(), req, "")

					require.Nil(t, got)
					wantErr := stage.wantErr
					wantCode := stage.wantCode
					wantCalls := []string{"items"}
					if stage.itemErr == nil {
						wantCalls = append(wantCalls, "submit")
					}
					wantCalls = append(wantCalls, "release")
					if failure.releaseErr != nil {
						wantErr = batchimage.ErrBatchImageBillingHoldFailed
						wantCode = "BILLING_RELEASE_FAILED"
						wantCalls = append(wantCalls, "record:"+wantCode, "enqueue")
						require.Len(t, authCache.userIDs, 1)
					} else {
						if stage.itemErr != nil || stage.providerErr != nil {
							wantCalls = append(wantCalls, "describe")
						}
						wantCalls = append(wantCalls, "record:"+wantCode, "hide")
						require.Equal(t, stage.wantMessage, writes.message)
						require.Len(t, authCache.userIDs, 2)
					}
					require.ErrorIs(t, err, wantErr)
					require.Equal(t, wantCalls, calls)
					require.Len(t, funds.reserves, 1)
					require.Len(t, funds.releases, 1)
					require.Equal(t, funds.reserves[0].RequestPayloadHash, funds.releases[0].RequestPayloadHash)
					require.Len(t, repo.jobs, 1)
					for _, job := range repo.jobs {
						require.Nil(t, job.ProviderJobName)
						require.Equal(t, failure.releaseErr != nil, job.AllowanceReserved)
						require.Equal(t, batchimage.BatchImageReleaseRequestID(job.BatchID), funds.releases[0].RequestID)
						if failure.recordErr == nil {
							require.Equal(t, batchimage.BatchImageJobStatusFailed, job.Status)
							require.Equal(t, wantCode, batchimage.BatchImageDerefString(job.LastErrorCode))
						} else {
							wantStatus := batchimage.BatchImageJobStatusUploading
							if stage.itemErr != nil {
								wantStatus = batchimage.BatchImageJobStatusCreated
							}
							require.Equal(t, wantStatus, job.Status)
							require.Nil(t, job.LastErrorCode)
						}
						if failure.releaseErr == nil && failure.recordErr == nil && failure.hideErr == nil {
							require.NotNil(t, job.UserDeletedAt)
						} else {
							require.Nil(t, job.UserDeletedAt)
						}
						if failure.releaseErr != nil && failure.queueErr == nil {
							require.Equal(t, []string{job.BatchID}, queue.enqueued)
						} else {
							require.Empty(t, queue.enqueued)
						}
					}
				})
			}
		})
	}
}

// TestBatchImagePricingSnapshotUsesOneGroupVersion changes group rates
// between successive reads while resolving an image snapshot. It checks one
// GroupView read and billable and hold prices derived from that first
// version.
func TestBatchImagePricingSnapshotUsesOneGroupVersion(t *testing.T) {
	oldGroup := &batchimage.GroupView{
		ID: 7, AllowBatchImageGeneration: true, RateMultiplier: 10, BatchImageDiscountMultiplier: 0.5, BatchImageHoldMultiplier: 0.6,
	}
	newGroup := *oldGroup
	newGroup.RateMultiplier = 1
	groups := &changingMediaPricingGroupRepo{oldGroup: oldGroup, newGroup: &newGroup}
	svc := newBatchPublicFixture(nil, nil, nil, groups, nil, nil, nil, &batchimage.Pricing{Resolver: billingtestkit.SharedPriceResolver(billingtestkit.Calculator(nil, nil), 7, pricing.DefaultBillingSettings(), []routing.ModelPricingEntry{{Models: []string{"gemini-3.1-flash-image"}, BillingMode: routing.BillingModeImage, PerRequestPrice: testPtrFloat64(1)}}), GroupRepo: batchGroupReader{groups}}, nil, nil, nil)
	snapshot, err := svc.ResolvePricingSnapshot(context.Background(), batchimage.BatchImageOwner{UserID: 11, APIKeyID: 22, GroupID: &oldGroup.ID, BillingMode: apikey.APIKeyBillingModeBalance}, batchimage.BatchImageSubmitRequest{Model: "gemini-3.1-flash-image", ImageSize: "1K"}, "gemini_api", nil)
	require.NoError(t, err)
	require.Equal(t, 1, groups.calls)
	require.Equal(t, 1.0, snapshot.BaseUnitPrice)
	require.Equal(t, 10.0, snapshot.GroupRateMultiplier)
	require.InDelta(t, 5.0, snapshot.BillableUnitPrice, 1e-12)
	require.InDelta(t, 6.0, snapshot.HoldUnitPrice, 1e-12)
}

// TestBatchImagePublicService_Submit covers normalization, selected
// providers, model identities, pricing and funding snapshots, plus failures
// across submission phases. It checks recorded upstream and queue calls,
// compensation, idempotent replay and public response filtering.
func TestBatchImagePublicService_Submit(t *testing.T) {
	ctx := context.Background()

	t.Run("rejects when storage is unavailable", func(t *testing.T) {
		svc, _, _, _, _, _ := newTestBatchImagePublicService()
		svc.Repo = nil
		_, err := svc.Submit(ctx, testBatchImageOwner(), validBatchImageSubmitRequest(), "")
		require.ErrorIs(t, err, batchimage.ErrBatchImageDisabled)
	})

	t.Run("accepts valid request stores refs and enqueues once", func(t *testing.T) {
		svc, repo, queue, gemini, _, authCache := newTestBatchImagePublicService()
		req := validBatchImageSubmitRequest()
		req.SessionID = batchimage.BatchImageStringPtr("batch-session-123")

		got, err := svc.Submit(ctx, testBatchImageOwner(), req, "")
		require.NoError(t, err)
		require.Equal(t, "image.batch", got.Object)
		require.Equal(t, "queued", got.Status)
		require.Equal(t, batchimage.BatchImageProviderGeminiAPI, got.Platform)
		require.Equal(t, 2, got.ItemCount)
		require.Equal(t, 0.25, got.EstimatedCost)
		require.Len(t, repo.jobs, 1)
		require.Len(t, gemini.submits, 1)
		require.Equal(t, []string{got.ID}, queue.enqueued)
		billing := testassert.MustType[*fakeBatchImageBillingRepo](taskFixtureBilling(svc))
		require.Len(t, billing.reserves, 1)
		require.Equal(t, batchimage.BatchImageHoldRequestID(got.ID), billing.reserves[0].RequestID)
		require.InDelta(t, 0.3, billing.reserves[0].HoldAmount, 1e-12)
		require.Empty(t, billing.releases)
		require.Equal(t, []int64{11}, authCache.userIDs)

		job := repo.jobs[got.ID]
		require.Equal(t, batchimage.BatchImageJobStatusSubmitted, job.Status)
		require.Equal(t, "providers/gemini_api/job", batchimage.BatchImageDerefString(job.ProviderJobName))
		require.Equal(t, "files/gemini_api/input", batchimage.BatchImageDerefString(job.ProviderInputRef))
		require.Equal(t, "files/gemini_api/output", batchimage.BatchImageDerefString(job.ProviderOutputRef))
		require.NotNil(t, job.ProviderID)
		require.Equal(t, int64(101), *job.ProviderID)
		require.Equal(t, 3, job.PricingSnapshotVersion)
		require.InDelta(t, 0.25, job.BaseUnitPrice, 1e-12)
		require.InDelta(t, 1.0, job.GroupRateMultiplier, 1e-12)
		require.InDelta(t, 1.0, job.ProviderRateMultiplier, 1e-12)
		require.InDelta(t, 0.5, job.BatchDiscountMultiplier, 1e-12)
		require.InDelta(t, 0.6, job.HoldMultiplier, 1e-12)
		require.InDelta(t, 0.125, job.BillableUnitPrice, 1e-12)
		require.InDelta(t, 0.15, job.HoldUnitPrice, 1e-12)
		require.Equal(t, "batch-session-123", batchimage.BatchImageDerefString(job.SessionID))
	})

	t.Run("persists composite route identity and separates idempotency", func(t *testing.T) {
		svc, repo, _, _, _, _ := newTestBatchImagePublicService()
		groupID := int64(7)
		otherGroupID := int64(8)
		svc.GroupRepo = batchGroupReader{&publicBatchImageGroupRepo{groups: map[int64]*batchimage.GroupView{
			groupID: {
				ID:                        groupID,
				RateMultiplier:            1,
				AllowBatchImageGeneration: true,
			},
			otherGroupID: {
				ID:                        otherGroupID,
				RateMultiplier:            1,
				AllowBatchImageGeneration: true,
			},
		}}}
		owner := batchimage.BatchImageOwner{UserID: 11, APIKeyID: 22, GroupID: &groupID}
		compositeCtx := context.WithValue(ctx, telemetry.ClientModel, "Gemini/gemini-2.5-flash-image")

		got, err := svc.Submit(compositeCtx, owner, validBatchImageSubmitRequest(), "composite-key")
		require.NoError(t, err)
		require.Equal(t, "Gemini/gemini-2.5-flash-image", got.Model)
		job := repo.jobs[got.ID]
		require.Equal(t, "gemini-2.5-flash-image", job.Model)
		require.Equal(t, "Gemini/gemini-2.5-flash-image", job.RequestedModel)
		require.Equal(t, "gemini-2.5-flash-image", job.InternalModel)
		require.NotNil(t, job.GroupID)
		require.Equal(t, groupID, *job.GroupID)

		caseInsensitiveCtx := context.WithValue(ctx, telemetry.ClientModel, "gEmInI/gemini-2.5-flash-image")
		retried, err := svc.Submit(caseInsensitiveCtx, owner, validBatchImageSubmitRequest(), "composite-key")
		require.NoError(t, err)
		require.Equal(t, got.ID, retried.ID)

		otherOwner := owner
		otherOwner.GroupID = &otherGroupID
		otherGroupCtx := context.WithValue(ctx, telemetry.ClientModel, "Backup/gemini-2.5-flash-image")
		second, err := svc.Submit(otherGroupCtx, otherOwner, validBatchImageSubmitRequest(), "composite-key")
		require.Nil(t, second)
		require.ErrorIs(t, err, batchimage.ErrBatchImageIdempotencyConflict)
	})

	t.Run("applies channel and provider model mappings before platform submit", func(t *testing.T) {
		svc, repo, _, gemini, _, _ := newTestBatchImagePublicService()
		groupID := int64(7)
		svc.GroupRepo = batchGroupReader{&publicBatchImageGroupRepo{groups: map[int64]*batchimage.GroupView{
			groupID: {
				ID:                           groupID,
				RateMultiplier:               1,
				AllowBatchImageGeneration:    true,
				BatchImageDiscountMultiplier: 0.5,
				BatchImageHoldMultiplier:     0.6,
			},
		}}}
		svc.PricingConfigService = newPublicPricingConfigFixture(makePublicPricingConfigFixture(routing.PricingConfig{
			ID:       31,
			Status:   billingcore.StatusActive,
			GroupIDs: []int64{groupID},
		}, map[int64]string{groupID: capability.PlatformGemini}, routing.GroupRoutingPolicy{
			Enabled:      true,
			ModelMapping: map[string]string{"gemini-2.5-flash-image": "group-image-model"},
		}))
		svc.ProviderRepo = rebindBatchFixtureProviders(svc, &publicBatchImageProviderRepo{providers: []providercore.Record{
			testBatchImageMappedProvider(301, capability.ProviderTypeAPIKey, map[string]any{
				"group-image-model": "upstream-image-model",
			}),
		}})
		svc.Attributes = &routing.ModelAttributeService{Catalog: routing.ModelAttributeCatalog{Lookup: func(model string) modelcatalog.Attributes {
			image := []string{"image"}
			if model == "upstream-image-model" {
				return modelcatalog.Attributes{OutputModalities: &image}
			}
			return modelcatalog.Attributes{}
		}}}
		trace := modeltrace.NewAPIKeyModelRedirectTrace("image-alias", "image-alias", "gemini-2.5-flash-image")
		requestCtx := modeltrace.WithContext(ctx, trace)
		requestCtx = context.WithValue(requestCtx, telemetry.ClientModel, "image-alias")

		got, err := svc.Submit(requestCtx, batchimage.BatchImageOwner{UserID: 11, APIKeyID: 22, GroupID: &groupID}, validBatchImageSubmitRequest(), "")

		require.NoError(t, err)
		require.Equal(t, "image-alias", got.Model)
		require.Len(t, gemini.submits, 1)
		require.Equal(t, "upstream-image-model", gemini.submits[0].Model)
		job := repo.jobs[got.ID]
		require.Equal(t, "upstream-image-model", job.Model)
		require.Equal(t, "image-alias", job.RequestedModel)
		require.Equal(t, "gemini-2.5-flash-image", job.InternalModel)
		require.ElementsMatch(t, []string{"gemini-2.5-flash-image", "group-image-model", "upstream-image-model"}, trace.ResponseModels())
		pricing := testassert.MustType[*fakeBatchImagePricingResolver](svc.Pricing)
		require.Equal(t, []string{"group-image-model"}, pricing.models)
	})

	t.Run("selects batch pricing model from channel billing source", func(t *testing.T) {
		tests := []struct {
			source string // selected billing stage
			want   string // expected observable result
		}{
			{source: routing.BillingModelSourceRequested, want: "key-target"},
			{source: routing.BillingModelSourceGroupMapped, want: "channel-target"},
			{source: routing.BillingModelSourceUpstream, want: "upstream-target"},
			{source: "", want: "channel-target"},
		}
		for _, test := range tests {
			require.Equal(t, test.want, batchimage.BatchImagePricingModel(
				routing.GroupMappingResult{BillingModelSource: test.source},
				"key-target",
				"channel-target",
				"upstream-target",
			))
		}
	})

	t.Run("combines user group image rate provider rate discount and hold margin", func(t *testing.T) {
		svc, repo, _, _, _, _ := newTestBatchImagePublicService()
		groupID := int64(7)
		providerMultiplier := 1.25
		providerRepo := testassert.MustType[*publicBatchImageProviderRepo](testassert.MustType[*batchProviderFixture](svc.ProviderRepo).source)
		providerRepo.providers[0].RateMultiplier = &providerMultiplier
		svc.GroupRepo = batchGroupReader{&publicBatchImageGroupRepo{groups: map[int64]*batchimage.GroupView{
			groupID: {
				ID:                           groupID,
				RateMultiplier:               2.0,
				AllowBatchImageGeneration:    true,
				BatchImageDiscountMultiplier: 0.8,
				BatchImageHoldMultiplier:     0.6,
			},
		}}}
		userRate := 0.5
		svc.UserGroupRateRepo = &publicBatchImageUserGroupRateRepo{rates: map[int64]*float64{groupID: &userRate}}

		got, err := svc.Submit(ctx, batchimage.BatchImageOwner{UserID: 11, APIKeyID: 22, GroupID: &groupID}, validBatchImageSubmitRequest(), "")
		require.NoError(t, err)
		require.InDelta(t, 0.25, got.EstimatedCost, 1e-12)
		billing := testassert.MustType[*fakeBatchImageBillingRepo](taskFixtureBilling(svc))
		require.Len(t, billing.reserves, 1)
		require.NotNil(t, billing.reserves[0].GroupID)
		require.Equal(t, groupID, *billing.reserves[0].GroupID)

		job := repo.jobs[got.ID]
		require.InDelta(t, 0.25, job.BaseUnitPrice, 1e-12)
		require.InDelta(t, 0.5, job.GroupRateMultiplier, 1e-12)
		require.InDelta(t, 1.25, job.ProviderRateMultiplier, 1e-12)
		require.InDelta(t, 0.8, job.BatchDiscountMultiplier, 1e-12)
		// A hold multiplier of 0.6 below the 0.8 discount cannot cover full success.
		// Snapshot creation clamps it to the discount so the hold covers the maximum cost.
		require.InDelta(t, 0.8, job.HoldMultiplier, 1e-12)
		require.InDelta(t, 0.125, job.BillableUnitPrice, 1e-12)
		require.InDelta(t, 0.125, job.HoldUnitPrice, 1e-12)
		require.InDelta(t, 0.25, *job.HoldAmount, 1e-12)
	})

	t.Run("uses subscription plan group rate before user balance rate", func(t *testing.T) {
		svc, repo, _, _, _, _ := newTestBatchImagePublicService()
		groupID := int64(7)
		svc.GroupRepo = batchGroupReader{&publicBatchImageGroupRepo{groups: map[int64]*batchimage.GroupView{
			groupID: {
				ID:                           groupID,
				RateMultiplier:               2,
				AllowBatchImageGeneration:    true,
				BatchImageDiscountMultiplier: 0.5,
				BatchImageHoldMultiplier:     0.6,
			},
		}}}
		userRate := 0.25
		svc.UserGroupRateRepo = &publicBatchImageUserGroupRateRepo{rates: map[int64]*float64{groupID: &userRate}}
		billing := testassert.MustType[*fakeBatchImageBillingRepo](taskFixtureBilling(svc))
		billing.usableSubscription = &billingcore.UserSubscription{
			ID:     101,
			UserID: 11,
			Plan: &billingcore.SubscriptionPlan{
				ID:                   202,
				GroupIDs:             []int64{groupID},
				GroupRateMultipliers: map[int64]float64{groupID: 0.8},
			},
		}

		got, err := svc.Submit(ctx, batchimage.BatchImageOwner{UserID: 11, APIKeyID: 22, GroupID: &groupID}, validBatchImageSubmitRequest(), "")

		require.NoError(t, err)
		require.InDelta(t, 0.2, got.EstimatedCost, 1e-12)
		job := repo.jobs[got.ID]
		require.InDelta(t, 0.8, job.GroupRateMultiplier, 1e-12)
		require.InDelta(t, 0.1, job.BillableUnitPrice, 1e-12)
		require.InDelta(t, 0.12, job.HoldUnitPrice, 1e-12)
		require.InDelta(t, 0.24, *job.HoldAmount, 1e-12)
		require.Len(t, billing.reserves, 1)
		require.InDelta(t, 0.5, billing.reserves[0].BaseAmountUSD, 1e-12)
		require.InDelta(t, 1.2, billing.reserves[0].SubscriptionRateMultiplier, 1e-12)
		require.InDelta(t, 0.6, billing.reserves[0].SubscriptionRateMultiplierScale, 1e-12)
		require.InDelta(t, 0.15, billing.reserves[0].BalanceRateMultiplier, 1e-12)
		require.False(t, billing.reserves[0].DisablePlanGroupRateMultiplier)
	})

	t.Run("uses configured group 1k image price for batch image base price", func(t *testing.T) {
		svc, repo, _, _, _, _ := newTestBatchImagePublicService()
		groupID := int64(7)
		imagePrice := 0.134
		svc.GroupRepo = batchGroupReader{&publicBatchImageGroupRepo{groups: map[int64]*batchimage.GroupView{
			groupID: {
				ID:                           groupID,
				RateMultiplier:               1.0,
				AllowBatchImageGeneration:    true,
				BatchImageDiscountMultiplier: 0.5,
				BatchImageHoldMultiplier:     0.6,
			},
		}}}

		svc.Pricing = &batchimage.Pricing{Resolver: billingtestkit.SharedPriceResolver(billingtestkit.Calculator(nil, nil), groupID, pricing.DefaultBillingSettings(), testImageModelPricing(map[string]*float64{"1K": &imagePrice})), GroupRepo: svc.GroupRepo}

		got, err := svc.Submit(ctx, batchimage.BatchImageOwner{UserID: 11, APIKeyID: 22, GroupID: &groupID}, validBatchImageSubmitRequest(), "")
		require.NoError(t, err)
		require.InDelta(t, 0.134, got.EstimatedCost, 1e-12)

		job := repo.jobs[got.ID]
		require.InDelta(t, 0.134, job.BaseUnitPrice, 1e-12)
		require.InDelta(t, 0.067, job.BillableUnitPrice, 1e-12)
		require.InDelta(t, 0.0804, job.HoldUnitPrice, 1e-12)
		require.InDelta(t, 0.1608, *job.HoldAmount, 1e-12)
	})

	t.Run("pricing missing rejects before platform submit", func(t *testing.T) {
		svc, repo, queue, gemini, _, _ := newTestBatchImagePublicService()
		svc.Pricing = &fakeBatchImagePricingResolver{err: batchimage.ErrBatchImageSettlementPricingMissing}

		_, err := svc.Submit(ctx, testBatchImageOwner(), validBatchImageSubmitRequest(), "")
		require.ErrorIs(t, err, batchimage.ErrBatchImageSettlementPricingMissing)
		require.Empty(t, repo.jobs)
		require.Empty(t, queue.enqueued)
		require.Empty(t, gemini.submits)
	})

	t.Run("group batch image disabled rejects before platform submit", func(t *testing.T) {
		svc, repo, queue, gemini, _, _ := newTestBatchImagePublicService()
		groupID := int64(7)
		svc.GroupRepo = batchGroupReader{&publicBatchImageGroupRepo{groups: map[int64]*batchimage.GroupView{
			groupID: {
				ID:                           groupID,
				RateMultiplier:               1,
				AllowBatchImageGeneration:    false,
				BatchImageDiscountMultiplier: 0.5,
				BatchImageHoldMultiplier:     0.6,
			},
		}}}

		_, err := svc.Submit(ctx, batchimage.BatchImageOwner{UserID: 11, APIKeyID: 22, GroupID: &groupID}, validBatchImageSubmitRequest(), "")
		require.ErrorIs(t, err, batchimage.ErrBatchImageGroupDisabled)
		require.Empty(t, repo.jobs)
		require.Empty(t, queue.enqueued)
		require.Empty(t, gemini.submits)
	})

	t.Run("group pricing load failure rejects before platform submit", func(t *testing.T) {
		svc, repo, queue, gemini, _, _ := newTestBatchImagePublicService()
		groupID := int64(404)

		_, err := svc.Submit(ctx, batchimage.BatchImageOwner{UserID: 11, APIKeyID: 22, GroupID: &groupID}, validBatchImageSubmitRequest(), "")
		require.ErrorIs(t, err, batchimage.ErrBatchImageSettlementPricingMissing)
		require.Empty(t, repo.jobs)
		require.Empty(t, queue.enqueued)
		require.Empty(t, gemini.submits)
	})

	t.Run("generates custom ids deterministically", func(t *testing.T) {
		svc, _, _, gemini, _, _ := newTestBatchImagePublicService()
		req := validBatchImageSubmitRequest()
		req.Items[0].CustomID = ""
		req.Items[1].CustomID = ""

		_, err := svc.Submit(ctx, testBatchImageOwner(), req, "")
		require.NoError(t, err)
		require.Len(t, gemini.submits, 1)
		require.Equal(t, "item_000001", gemini.submits[0].Items[0].CustomID)
		require.Equal(t, "item_000002", gemini.submits[0].Items[1].CustomID)
	})

	t.Run("expands output count into separate billable items", func(t *testing.T) {
		svc, repo, _, gemini, _, _ := newTestBatchImagePublicService()
		req := validBatchImageSubmitRequest()
		req.Items = []batchimage.BatchImageSubmitItem{
			{CustomID: "cover", Prompt: "hero", OutputCount: 3, ReferenceImages: []batchimage.BatchImageReferenceInput{{MimeType: "image/png", Data: []byte("ref")}}},
		}

		got, err := svc.Submit(ctx, testBatchImageOwner(), req, "")
		require.NoError(t, err)
		require.Equal(t, 3, got.ItemCount)
		require.InDelta(t, 0.375, got.EstimatedCost, 1e-12)
		require.Len(t, gemini.submits, 1)
		require.Len(t, gemini.submits[0].Items, 3)
		require.Equal(t, []string{"cover_01", "cover_02", "cover_03"}, []string{
			gemini.submits[0].Items[0].CustomID,
			gemini.submits[0].Items[1].CustomID,
			gemini.submits[0].Items[2].CustomID,
		})
		require.Len(t, gemini.submits[0].Items[0].ReferenceImages, 1)
		require.Len(t, repo.items[got.ID], 3)
	})

	t.Run("validates request fields", func(t *testing.T) {
		tests := []struct {
			name   string                                    // scenario display name
			mutate func(*batchimage.BatchImageSubmitRequest) // scenario setup mutation
			want   error                                     // expected observable result
		}{
			{name: "missing_model", mutate: func(r *batchimage.BatchImageSubmitRequest) { r.Model = "" }, want: batchimage.ErrBatchImageInvalidModel},
			{name: "empty_items", mutate: func(r *batchimage.BatchImageSubmitRequest) { r.Items = nil }, want: batchimage.ErrBatchImageInvalidItems},
			{name: "duplicate_custom_ids", mutate: func(r *batchimage.BatchImageSubmitRequest) { r.Items[1].CustomID = r.Items[0].CustomID }, want: batchimage.ErrBatchImageDuplicateCustomIDInRequest},
			{name: "empty_prompt", mutate: func(r *batchimage.BatchImageSubmitRequest) { r.Items[0].Prompt = " " }, want: batchimage.ErrBatchImageInvalidItems},
			{name: "prompt_too_long", mutate: func(r *batchimage.BatchImageSubmitRequest) { r.Items[0].Prompt = strings.Repeat("x", 9) }, want: batchimage.ErrBatchImagePromptTooLong},
			{name: "unsupported_provider", mutate: func(r *batchimage.BatchImageSubmitRequest) { r.Platform = "other" }, want: batchimage.ErrBatchImageUnsupportedProvider},
			{name: "vertex_rejects_2k", mutate: func(r *batchimage.BatchImageSubmitRequest) {
				r.Platform = batchimage.BatchImageProviderVertex
				r.ImageSize = "2K"
			}, want: batchimage.ErrBatchImageInvalidItems},
			{name: "too_many_outputs_per_item", mutate: func(r *batchimage.BatchImageSubmitRequest) {
				r.Items[0].OutputCount = 5
			}, want: batchimage.ErrBatchImageInvalidItems},
			{name: "too_many_reference_images_for_flash", mutate: func(r *batchimage.BatchImageSubmitRequest) {
				r.Model = "gemini-2.5-flash-image"
				r.Items[0].ReferenceImages = []batchimage.BatchImageReferenceInput{
					{MimeType: "image/png", Data: []byte("1")},
					{MimeType: "image/png", Data: []byte("2")},
					{MimeType: "image/png", Data: []byte("3")},
					{MimeType: "image/png", Data: []byte("4")},
				}
			}, want: batchimage.ErrBatchImageTooManyReferenceImages},
			{name: "bad_reference_mime", mutate: func(r *batchimage.BatchImageSubmitRequest) {
				r.Items[0].ReferenceImages = []batchimage.BatchImageReferenceInput{{MimeType: "application/octet-stream", Data: []byte("x")}}
			}, want: batchimage.ErrBatchImageInvalidReferenceImage},
			{name: "reference_requires_data_or_file_uri", mutate: func(r *batchimage.BatchImageSubmitRequest) {
				r.Items[0].ReferenceImages = []batchimage.BatchImageReferenceInput{{MimeType: "image/png"}}
			}, want: batchimage.ErrBatchImageInvalidReferenceImage},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				svc, _, _, _, _, _ := newTestBatchImagePublicService()
				req := validBatchImageSubmitRequest()
				tt.mutate(&req)

				_, err := svc.Submit(ctx, testBatchImageOwner(), req, "")
				require.ErrorIs(t, err, tt.want)
			})
		}
	})

	t.Run("rejects too many items", func(t *testing.T) {
		svc, _, _, _, _, _ := newTestBatchImagePublicService()
		req := validBatchImageSubmitRequest()
		req.Items = append(req.Items, batchimage.BatchImageSubmitItem{CustomID: "too_many", Prompt: "x"})

		_, err := svc.Submit(ctx, testBatchImageOwner(), req, "")
		require.ErrorIs(t, err, batchimage.ErrBatchImageInvalidItems)
	})

	t.Run("rejects too many output images", func(t *testing.T) {
		svc, _, _, _, _, _ := newTestBatchImagePublicService()
		svc.Options.MaxOutputImagesPerJob = 3
		req := validBatchImageSubmitRequest()
		req.Items[0].OutputCount = 2
		req.Items[1].OutputCount = 2

		_, err := svc.Submit(ctx, testBatchImageOwner(), req, "")
		require.ErrorIs(t, err, batchimage.ErrBatchImageTooManyOutputImages)
	})

	t.Run("rejects too many reference images across request", func(t *testing.T) {
		svc, _, _, _, _, _ := newTestBatchImagePublicService()
		svc.Options.MaxReferenceImagesPerJob = 3
		req := validBatchImageSubmitRequest()
		req.Model = "gemini-2.5-flash-image"
		req.Items[0].ReferenceImages = []batchimage.BatchImageReferenceInput{
			{MimeType: "image/png", Data: []byte("1")},
			{MimeType: "image/png", Data: []byte("2")},
		}
		req.Items[1].ReferenceImages = []batchimage.BatchImageReferenceInput{
			{MimeType: "image/png", Data: []byte("3")},
			{MimeType: "image/png", Data: []byte("4")},
		}

		_, err := svc.Submit(ctx, testBatchImageOwner(), req, "")
		require.ErrorIs(t, err, batchimage.ErrBatchImageTooManyReferenceImages)
	})

	t.Run("rejects too much inline reference image data across request", func(t *testing.T) {
		svc, _, _, _, _, _ := newTestBatchImagePublicService()
		svc.Options.MaxReferenceImagesPerJob = 10
		svc.Options.MaxReferenceInlineBytesPerJob = 4
		req := validBatchImageSubmitRequest()
		req.Model = "gemini-2.5-flash-image"
		req.Items[0].ReferenceImages = []batchimage.BatchImageReferenceInput{{MimeType: "image/png", Data: []byte("123")}}
		req.Items[1].ReferenceImages = []batchimage.BatchImageReferenceInput{{MimeType: "image/png", Data: []byte("456")}}

		_, err := svc.Submit(ctx, testBatchImageOwner(), req, "")
		require.ErrorIs(t, err, batchimage.ErrBatchImageReferenceImagesTooLarge)
	})

	t.Run("selects requested platform", func(t *testing.T) {
		svc, _, _, gemini, vertex, _ := newTestBatchImagePublicService()
		req := validBatchImageSubmitRequest()
		req.Platform = batchimage.BatchImageProviderVertex

		got, err := svc.Submit(ctx, testBatchImageOwner(), req, "")
		require.NoError(t, err)
		require.Equal(t, batchimage.BatchImageProviderVertex, got.Platform)
		require.Empty(t, gemini.submits)
		require.Len(t, vertex.submits, 1)
	})

	t.Run("insufficient balance rejects before platform submit", func(t *testing.T) {
		svc, repo, queue, gemini, _, _ := newTestBatchImagePublicService()
		billing := &fakeBatchImageBillingRepo{err: batchimage.ErrBatchImageInsufficientBalance}
		svc.Funding = nativeTaskFundingFixture(billing)

		_, err := svc.Submit(ctx, testBatchImageOwner(), validBatchImageSubmitRequest(), "")
		require.ErrorIs(t, err, batchimage.ErrBatchImageInsufficientBalance)
		require.Empty(t, queue.enqueued)
		require.Empty(t, gemini.submits)
		require.Len(t, billing.reserves, 1)
		require.Empty(t, billing.releases)
		require.Len(t, repo.jobs, 1)
		for _, job := range repo.jobs {
			require.Equal(t, batchimage.BatchImageJobStatusFailed, job.Status)
			require.Equal(t, "INSUFFICIENT_BALANCE", batchimage.BatchImageDerefString(job.LastErrorCode))
			require.NotNil(t, job.UserDeletedAt)
		}
	})

	t.Run("preferred subscription failure rejects before platform submit", func(t *testing.T) {
		tests := []struct {
			name     string // scenario display name
			err      error  // injected operation error
			wantCode string // expected failure category
		}{
			{name: "unavailable", err: apikey.ErrPreferredSubscriptionInvalid, wantCode: "PREFERRED_SUBSCRIPTION_INVALID"},
			{name: "group not allowed", err: apikey.ErrPreferredSubscriptionGroup, wantCode: "PREFERRED_SUBSCRIPTION_GROUP_NOT_ALLOWED"},
			{name: "quota exhausted", err: apikey.ErrPreferredSubscriptionInsufficient, wantCode: "PREFERRED_SUBSCRIPTION_EXHAUSTED"},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				svc, repo, queue, gemini, _, _ := newTestBatchImagePublicService()
				billing := &fakeBatchImageBillingRepo{err: test.err}
				svc.Funding = nativeTaskFundingFixture(billing)
				preferredSubscriptionID := int64(301)
				svc.PreferredSubscription = func(context.Context, int64, int64, *int64) *billingcore.UserSubscription {
					return &billingcore.UserSubscription{ID: preferredSubscriptionID}
				}
				owner := testBatchImageOwner()
				owner.BillingMode = apikey.APIKeyBillingModeSubscription
				owner.PreferredSubscriptionID = &preferredSubscriptionID

				_, err := svc.Submit(ctx, owner, validBatchImageSubmitRequest(), "")

				require.ErrorIs(t, err, test.err)
				require.Empty(t, queue.enqueued)
				require.Empty(t, gemini.submits)
				require.Len(t, billing.reserves, 1)
				require.Len(t, repo.jobs, 1)
				for _, job := range repo.jobs {
					require.Equal(t, batchimage.BatchImageJobStatusFailed, job.Status)
					require.Equal(t, test.wantCode, batchimage.BatchImageDerefString(job.LastErrorCode))
					require.NotNil(t, job.UserDeletedAt)
				}
			})
		}
	})

	t.Run("platform failure marks failed and does not enqueue", func(t *testing.T) {
		svc, repo, queue, gemini, _, _ := newTestBatchImagePublicService()
		gemini.submitErr = errors.New("projects/secret-platform-job failed")
		billing := testassert.MustType[*fakeBatchImageBillingRepo](taskFixtureBilling(svc))

		_, err := svc.Submit(ctx, testBatchImageOwner(), validBatchImageSubmitRequest(), "")
		require.ErrorIs(t, err, batchimage.ErrBatchImageProviderSubmitFailed)
		require.Empty(t, queue.enqueued)
		require.Len(t, billing.reserves, 1)
		require.Len(t, billing.releases, 1)
		require.Equal(t, batchimage.BatchImageReleaseRequestID(billing.reserves[0].Task.ID), billing.releases[0].RequestID)
		require.Len(t, repo.jobs, 1)
		for _, job := range repo.jobs {
			require.Equal(t, batchimage.BatchImageJobStatusFailed, job.Status)
			require.Equal(t, "PROVIDER_SUBMIT_FAILED", batchimage.BatchImageDerefString(job.LastErrorCode))
			require.Equal(t, "upstream platform operation failed", batchimage.BatchImageDerefString(job.LastErrorMessage))
			require.NotNil(t, job.UserDeletedAt)
		}
	})

	t.Run("platform failure with release failure enqueues billing retry", func(t *testing.T) {
		svc, repo, queue, gemini, _, _ := newTestBatchImagePublicService()
		gemini.submitErr = errors.New("projects/secret-platform-job failed")
		billing := testassert.MustType[*fakeBatchImageBillingRepo](taskFixtureBilling(svc))
		billing.releaseErr = errors.New("billing database timeout")

		_, err := svc.Submit(ctx, testBatchImageOwner(), validBatchImageSubmitRequest(), "")
		require.ErrorIs(t, err, batchimage.ErrBatchImageBillingHoldFailed)
		require.Len(t, billing.reserves, 1)
		require.Len(t, billing.releases, 1)
		require.Len(t, repo.jobs, 1)
		for _, job := range repo.jobs {
			require.Equal(t, batchimage.BatchImageJobStatusFailed, job.Status)
			require.Equal(t, "BILLING_RELEASE_FAILED", batchimage.BatchImageDerefString(job.LastErrorCode))
			require.Equal(t, []string{job.BatchID}, queue.enqueued)
		}
	})

	t.Run("queue failure is recorded after platform submit", func(t *testing.T) {
		svc, repo, queue, _, _, _ := newTestBatchImagePublicService()
		queue.err = errors.New("redis unavailable")
		billing := testassert.MustType[*fakeBatchImageBillingRepo](taskFixtureBilling(svc))

		_, err := svc.Submit(ctx, testBatchImageOwner(), validBatchImageSubmitRequest(), "")
		require.ErrorIs(t, err, batchimage.ErrBatchImageQueueFailed)
		require.Len(t, billing.reserves, 1)
		require.Empty(t, billing.releases)
		require.Len(t, repo.jobs, 1)
		for _, job := range repo.jobs {
			require.Equal(t, batchimage.BatchImageJobStatusSubmitted, job.Status)
			require.Equal(t, "QUEUE_FAILED", batchimage.BatchImageDerefString(job.LastErrorCode))
			require.Contains(t, repo.events[job.BatchID], "queue_failed")
		}
	})

	t.Run("idempotency returns same batch without platform resubmit", func(t *testing.T) {
		svc, repo, queue, gemini, _, _ := newTestBatchImagePublicService()
		req := validBatchImageSubmitRequest()
		req.SessionID = batchimage.BatchImageStringPtr("original-session")

		first, err := svc.Submit(ctx, testBatchImageOwner(), req, "client-key")
		require.NoError(t, err)
		req.SessionID = batchimage.BatchImageStringPtr("retry-session")
		second, err := svc.Submit(ctx, testBatchImageOwner(), req, "client-key")
		require.NoError(t, err)

		require.Equal(t, first.ID, second.ID)
		require.Equal(t, "original-session", batchimage.BatchImageDerefString(repo.jobs[first.ID].SessionID))
		require.Len(t, gemini.submits, 1)
		require.Equal(t, []string{first.ID}, queue.enqueued)
	})

	t.Run("idempotency conflict rejects changed request", func(t *testing.T) {
		svc, _, _, _, _, _ := newTestBatchImagePublicService()
		req := validBatchImageSubmitRequest()
		first, err := svc.Submit(ctx, testBatchImageOwner(), req, "client-key")
		require.NoError(t, err)

		req.Items[0].Prompt = "diff"
		second, err := svc.Submit(ctx, testBatchImageOwner(), req, "client-key")
		require.Nil(t, second)
		require.ErrorIs(t, err, batchimage.ErrBatchImageIdempotencyConflict)
		require.NotEmpty(t, first.ID)
	})

	t.Run("public response does not expose internals", func(t *testing.T) {
		svc, _, _, _, _, _ := newTestBatchImagePublicService()
		got, err := svc.Submit(ctx, testBatchImageOwner(), validBatchImageSubmitRequest(), "")
		require.NoError(t, err)

		body, err := json.Marshal(got)
		require.NoError(t, err)
		requireBatchImagePublicJSONHasNoInternals(t, string(body))
	})
}

// TestBatchImagePublicService_List stores jobs owned by different API keys
// under the same user. The listing exposes only the requested key's batch and
// reports a short page with no more-data indicator.
func TestBatchImagePublicService_List(t *testing.T) {
	ctx := context.Background()
	svc, repo, _, _, _, _ := newTestBatchImagePublicService()
	visibleKeyID := int64(22)
	otherKeyID := int64(23)

	repo.jobs["visible-1"] = &batchimage.BatchImageJob{
		BatchID:   "visible-1",
		UserID:    11,
		APIKeyID:  &visibleKeyID,
		Status:    batchimage.BatchImageJobStatusCompleted,
		Platform:  batchimage.BatchImageProviderVertex,
		Model:     "gemini-3.1-flash-lite-image",
		ItemCount: 1,
		CreatedAt: time.Now(),
	}
	repo.jobs["hidden-other-key"] = &batchimage.BatchImageJob{
		BatchID:   "hidden-other-key",
		UserID:    11,
		APIKeyID:  &otherKeyID,
		Status:    batchimage.BatchImageJobStatusCompleted,
		Platform:  batchimage.BatchImageProviderVertex,
		Model:     "gemini-3.1-flash-lite-image",
		ItemCount: 1,
		CreatedAt: time.Now(),
	}

	got, err := svc.List(ctx, batchimage.BatchImageOwner{UserID: 11, APIKeyID: visibleKeyID}, batchimage.BatchImageJobsQuery{Limit: 20})
	require.NoError(t, err)
	require.Equal(t, "list", got.Object)
	require.Len(t, got.Data, 1)
	require.Equal(t, "visible-1", got.Data[0].ID)
	require.False(t, got.HasMore)
}

// TestBatchImagePublicService_StatusItemsAndCancel queries owner-scoped
// status and filtered result pages before exercising cancellation. It checks
// scrubbed public diagnostics, pending upstream cancellation with held funds,
// terminal replay and public cancellation errors.
func TestBatchImagePublicService_StatusItemsAndCancel(t *testing.T) {
	ctx := context.Background()

	t.Run("status is owner scoped and maps public status", func(t *testing.T) {
		svc, repo, _, _, _, _ := newTestBatchImagePublicService()
		apiKeyID := int64(22)
		providerID := int64(101)
		repo.jobs["imgbatch_status"] = &batchimage.BatchImageJob{
			BatchID:         "imgbatch_status",
			UserID:          11,
			APIKeyID:        &apiKeyID,
			ProviderID:      &providerID,
			Platform:        batchimage.BatchImageProviderGeminiAPI,
			Model:           "gemini-2.5-flash-image",
			Status:          batchimage.BatchImageJobStatusIndexing,
			ProviderJobName: batchimage.BatchImageStringPtr("providers/internal/job"),
			CreatedAt:       time.Now(),
		}

		got, err := svc.Get(ctx, testBatchImageOwner(), "imgbatch_status")
		require.NoError(t, err)
		require.Equal(t, "processing_results", got.Status)
		body, err := json.Marshal(got)
		require.NoError(t, err)
		requireBatchImagePublicJSONHasNoInternals(t, string(body))

		_, err = svc.Get(ctx, batchimage.BatchImageOwner{UserID: 11, APIKeyID: 999}, "imgbatch_status")
		require.ErrorIs(t, err, batchimage.ErrBatchImageJobNotFound)
	})

	t.Run("items are filtered paginated and sanitized", func(t *testing.T) {
		svc, repo, _, _, _, _ := newTestBatchImagePublicService()
		apiKeyID := int64(22)
		repo.jobs["imgbatch_items"] = &batchimage.BatchImageJob{
			BatchID:   "imgbatch_items",
			UserID:    11,
			APIKeyID:  &apiKeyID,
			Platform:  batchimage.BatchImageProviderGeminiAPI,
			Model:     "gemini-2.5-flash-image",
			Status:    batchimage.BatchImageJobStatusCompleted,
			CreatedAt: time.Now(),
		}
		sourceObject := "gs://bucket/internal/output.jsonl"
		mime := "image/png"
		ext := "png"
		code := "SAFETY_BLOCKED"
		msg := "blocked in gs://bucket/internal/output.jsonl"
		repo.items["imgbatch_items"] = []batchimage.CreateBatchImageItemParams{
			{JobID: "imgbatch_items", CustomID: "ok_1", Status: batchimage.BatchImageItemStatusSuccess, ProviderSourceObject: &sourceObject, MimeType: &mime, FileExtension: &ext, ImageCount: 1},
			{JobID: "imgbatch_items", CustomID: "bad_1", Status: batchimage.BatchImageItemStatusFailed, ProviderSourceObject: &sourceObject, ErrorCode: &code, ErrorMessage: &msg},
			{JobID: "imgbatch_items", CustomID: "ok_2", Status: batchimage.BatchImageItemStatusSuccess, MimeType: &mime, FileExtension: &ext, ImageCount: 1},
		}

		page, err := svc.ListItems(ctx, testBatchImageOwner(), "imgbatch_items", batchimage.BatchImageItemsQuery{Limit: 1})
		require.NoError(t, err)
		require.True(t, page.HasMore)
		require.Len(t, page.Data, 1)
		require.Equal(t, "ok_1", page.Data[0].CustomID)

		filtered, err := svc.ListItems(ctx, testBatchImageOwner(), "imgbatch_items", batchimage.BatchImageItemsQuery{Status: "failed", Limit: 100})
		require.NoError(t, err)
		require.False(t, filtered.HasMore)
		require.Len(t, filtered.Data, 1)
		require.Equal(t, "failed", filtered.Data[0].Status)
		require.NotNil(t, filtered.Data[0].Error)
		require.Equal(t, "upstream platform operation failed", filtered.Data[0].Error.Message)

		body, err := json.Marshal(filtered)
		require.NoError(t, err)
		requireBatchImagePublicJSONHasNoInternals(t, string(body))
		require.NotContains(t, string(body), "download_url")

		_, err = svc.ListItems(ctx, batchimage.BatchImageOwner{UserID: 12, APIKeyID: 22}, "imgbatch_items", batchimage.BatchImageItemsQuery{})
		require.ErrorIs(t, err, batchimage.ErrBatchImageJobNotFound)
	})

	t.Run("cancel active job calls platform and waits for confirmed terminal state", func(t *testing.T) {
		svc, repo, queue, gemini, _, _ := newTestBatchImagePublicService()
		apiKeyID := int64(22)
		providerID := int64(101)
		holdAmount := 0.5
		holdID := batchimage.BatchImageHoldRequestID("imgbatch_cancel")
		repo.jobs["imgbatch_cancel"] = &batchimage.BatchImageJob{
			BatchID:         "imgbatch_cancel",
			UserID:          11,
			APIKeyID:        &apiKeyID,
			ProviderID:      &providerID,
			Platform:        batchimage.BatchImageProviderGeminiAPI,
			Model:           "gemini-2.5-flash-image",
			Status:          batchimage.BatchImageJobStatusSubmitted,
			ProviderJobName: batchimage.BatchImageStringPtr("providers/internal/job"),
			EstimatedCost:   holdAmount,
			HoldAmount:      &holdAmount,
			HoldID:          &holdID,
			CreatedAt:       time.Now(),
		}

		got, err := svc.Cancel(ctx, testBatchImageOwner(), "imgbatch_cancel")
		require.NoError(t, err)
		require.Equal(t, "queued", got.Status)
		require.Equal(t, 1, gemini.cancelCount)
		billing := testassert.MustType[*fakeBatchImageBillingRepo](taskFixtureBilling(svc))
		require.Empty(t, billing.releases)
		require.Equal(t, []string{"imgbatch_cancel"}, queue.enqueued)
		require.Equal(t, batchimage.BatchImageJobStatusSubmitted, repo.jobs["imgbatch_cancel"].Status)
		require.Contains(t, repo.events["imgbatch_cancel"], "job_cancel_requested")
	})

	t.Run("cancel terminal job is idempotent", func(t *testing.T) {
		svc, repo, _, gemini, _, _ := newTestBatchImagePublicService()
		apiKeyID := int64(22)
		repo.jobs["imgbatch_done"] = &batchimage.BatchImageJob{
			BatchID:   "imgbatch_done",
			UserID:    11,
			APIKeyID:  &apiKeyID,
			Platform:  batchimage.BatchImageProviderGeminiAPI,
			Model:     "gemini-2.5-flash-image",
			Status:    batchimage.BatchImageJobStatusCompleted,
			CreatedAt: time.Now(),
		}

		got, err := svc.Cancel(ctx, testBatchImageOwner(), "imgbatch_done")
		require.NoError(t, err)
		require.Equal(t, "completed", got.Status)
		require.Zero(t, gemini.cancelCount)
	})

	t.Run("cancel hides platform raw errors behind public error", func(t *testing.T) {
		svc, repo, _, gemini, _, _ := newTestBatchImagePublicService()
		gemini.cancelErr = errors.New("projects/secret-platform-job not found")
		apiKeyID := int64(22)
		providerID := int64(101)
		repo.jobs["imgbatch_cancel_error"] = &batchimage.BatchImageJob{
			BatchID:         "imgbatch_cancel_error",
			UserID:          11,
			APIKeyID:        &apiKeyID,
			ProviderID:      &providerID,
			Platform:        batchimage.BatchImageProviderGeminiAPI,
			Model:           "gemini-2.5-flash-image",
			Status:          batchimage.BatchImageJobStatusSubmitted,
			ProviderJobName: batchimage.BatchImageStringPtr("providers/internal/job"),
			CreatedAt:       time.Now(),
		}

		_, err := svc.Cancel(ctx, testBatchImageOwner(), "imgbatch_cancel_error")
		require.ErrorIs(t, err, batchimage.ErrBatchImageCancelFailed)
		require.Equal(t, "BATCH_IMAGE_CANCEL_FAILED", apperror.Reason(err))
		require.NotContains(t, apperror.Message(err), "projects/")
	})
}

// TestBatchImageServiceAccountErrorCode maps the missing Google
// service-account credential error through public submission translation. It
// checks the exact protocol reason retained for clients needing that
// credential configuration.
func TestBatchImageServiceAccountErrorCode(t *testing.T) {
	err := batchimage.BatchImageProviderSubmitPublicError(batchimage.ErrBatchImageProviderMissingServiceAccount)
	require.Equal(t, "BATCH_IMAGE_PROVIDER_MISSING_SERVICE_ACCOUNT", apperror.Reason(err))
}
