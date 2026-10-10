package batchimage_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/batchimage"
	batchimageprovider "github.com/TokenFlux/TokenRouter/internal/batchimage/provider"
	billingcore "github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	pricingprovider "github.com/TokenFlux/TokenRouter/internal/billing/provider"
	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/modeltrace"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	testassert "github.com/TokenFlux/TokenRouter/internal/testutil/assertion"
)

// batchCataloguePolicyReads 统计批量模型列表读取策略和计费来源的次数。
type batchCataloguePolicyReads struct {
	*routing.PricingConfigService
	policyReads, pricingReads int
	modelsList                []string
}

// changingMediaPricingGroupRepo 模拟两次读取之间管理员修改分组倍率，两份配置计算出的最终价格相同。
type changingMediaPricingGroupRepo struct {
	calls              int
	oldGroup, newGroup *batchimage.GroupView
}

// publicPricingConfigFixture 提供模型配置和价卡数据，编译和报价由生产模块执行。
type publicPricingConfigFixture struct {
	routing.PricingConfigRepository
	pricingConfig routing.PricingConfig
	platforms     map[int64]string
	policy        routing.GroupRoutingPolicy
}

type fakeBatchImageAuthCacheInvalidator struct {
	keys     []string
	userIDs  []int64
	groupIDs []int64
}

// TestBatchImageCatalogueIncludesGroupMappingTargets 分组映射目标通过图片价格和权限检查后进入目录。
func TestBatchImageCatalogueIncludesGroupMappingTargets(t *testing.T) {
	for _, source := range []string{"draw-alias", "draw-*"} {
		t.Run(source, func(t *testing.T) {
			service, _, _, _, _, _ := newTestBatchImagePublicService(true)
			service.ProviderExists = func(name string) bool { return name == batchimage.BatchImageProviderGeminiAPI }
			owner := testBatchImageOwner()
			value := testBatchImageMappedProvider(303, "apikey", nil)
			service.ProviderRepo = rebindBatchFixtureProviders(service, &publicBatchImageProviderRepo{providers: []providercore.Record{value}})
			policy := makePublicPricingConfigFixture(routing.PricingConfig{ID: 1, Status: "active", GroupIDs: []int64{*owner.GroupID}}, nil, routing.GroupRoutingPolicy{Enabled: true, ModelMapping: map[string]string{source: "custom-image"}})
			service.PricingConfigService = newPublicPricingConfigFixture(policy)
			service.Pricing = &fakeBatchImagePricingResolver{unitPrice: 0.1}
			result, err := service.ListModels(context.Background(), owner)
			require.NoError(t, err)
			var ids []string
			for _, model := range result.Data {
				ids = append(ids, model.ID)
			}
			want := []string{"custom-image"}
			if source == "draw-alias" {
				want = append(want, source)
			}
			require.ElementsMatch(t, want, ids)

			policy.policy.RestrictModels = true
			policy.policy.RestrictionModelSource = routing.BillingModelSourceRequested
			policy.policy.AllowedModels = []string{source}
			result, err = service.ListModels(context.Background(), owner)
			require.NoError(t, err)
			for _, model := range result.Data {
				require.NotEqual(t, "custom-image", model.ID)
			}

			policy.policy.RestrictModels = false
			value.Credentials["model_whitelist"] = []string{"other-model"}
			result, err = service.ListModels(context.Background(), owner)
			require.NoError(t, err)
			for _, model := range result.Data {
				require.NotEqual(t, "custom-image", model.ID)
			}
		})
	}
}

// TestBatchImageUnboundKeyCannotUseGlobalProviders 检查未绑定分组的 Key 提交任务时返回分组禁用错误。
func TestBatchImageUnboundKeyCannotUseGlobalProviders(t *testing.T) {
	svc, repo, _, platform, _, _ := newTestBatchImagePublicService(true)
	owner := testBatchImageOwner()
	owner.GroupID = nil
	_, err := svc.Submit(context.Background(), owner, validBatchImageSubmitRequest(), "")
	require.ErrorIs(t, err, batchimage.ErrBatchImageGroupDisabled)
	require.Empty(t, repo.jobs)
	require.Empty(t, platform.submits)
}

// TestBatchImageCatalogueResolvesBeforeModalities 覆盖通配来源、分组改写和最终输出模态。
func TestBatchImageCatalogueResolvesBeforeModalities(t *testing.T) {
	for _, tc := range []struct {
		name            string
		groupMapping    map[string]string
		providerMapping map[string]any
		whitelist       []string
		final           string
		visible         bool
	}{
		{name: "provider wildcard", providerMapping: map[string]any{"gpt-*": "catalog-image"}, whitelist: []string{"catalog-image"}, final: "catalog-image", visible: true},
		{name: "group wildcard", groupMapping: map[string]string{"gpt-*": "catalog-image"}, whitelist: []string{"catalog-image"}, final: "catalog-image", visible: true},
		{name: "two mapping stages", groupMapping: map[string]string{"gpt-*": "route-model"}, providerMapping: map[string]any{"route-*": "catalog-image"}, whitelist: []string{"catalog-image"}, final: "catalog-image", visible: true},
		{name: "unknown configured final", providerMapping: map[string]any{"gpt-*": "nano-banana-pro"}, whitelist: []string{"nano-banana-pro"}, final: "nano-banana-pro", visible: true},
		{name: "mapped text final", providerMapping: map[string]any{"gpt-*": "catalog-text"}, whitelist: []string{"catalog-text"}, final: "catalog-text"},
		{name: "provider restriction", providerMapping: map[string]any{"gpt-*": "catalog-image"}, whitelist: []string{"catalog-text"}, final: "catalog-image"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, _, _, _, _ := newTestBatchImagePublicService(true)
			owner := testBatchImageOwner()
			group := *owner.GroupID
			value := testBatchImageMappedProvider(303, "apikey", tc.providerMapping)
			value.Credentials["model_whitelist"] = tc.whitelist
			svc.ProviderRepo = rebindBatchFixtureProviders(svc, &publicBatchImageProviderRepo{providers: []providercore.Record{value}})
			image, text := []string{"image"}, []string{"text"}
			svc.ModelOutputModalities = func(id string) *[]string {
				switch id {
				case "catalog-image":
					return &image
				case "gpt-5.4", "catalog-text":
					return &text
				default:
					return nil
				}
			}
			policy := newPublicPricingConfigFixture(makePublicPricingConfigFixture(routing.PricingConfig{ID: 1, Status: "active", GroupIDs: []int64{group}, BillingModelSource: routing.BillingModelSourceUpstream}, nil, routing.GroupRoutingPolicy{Enabled: true, AllowedModels: []string{"gpt-5.4"}, ModelMapping: tc.groupMapping}))
			svc.PricingConfigService = policy
			price := &fakeBatchImagePricingResolver{unitPrice: 0.1}
			svc.Pricing = price
			result, err := svc.ListModels(context.Background(), owner)
			require.NoError(t, err)
			ids := []string{}
			for _, model := range result.Data {
				ids = append(ids, model.ID)
			}
			if tc.visible {
				require.Contains(t, ids, "gpt-5.4")
				require.Contains(t, price.models, tc.final)
			} else {
				require.NotContains(t, ids, "gpt-5.4")
			}
			require.NotContains(t, ids, "unconfigured-unknown")
			require.NotContains(t, ids, "catalog-text")
		})
	}
}

// TestBatchImageCatalogueUsesOnePolicySnapshot 不同白名单阶段均复用一次策略和计费来源读取。
func TestBatchImageCatalogueUsesOnePolicySnapshot(t *testing.T) {
	for _, stage := range []string{routing.BillingModelSourceRequested, routing.BillingModelSourceGroupMapped, routing.BillingModelSourceUpstream} {
		t.Run(stage, func(t *testing.T) {
			svc, _, _, _, _, _ := newTestBatchImagePublicService(true)
			owner := testBatchImageOwner()
			group := *owner.GroupID
			allowed := map[string]string{routing.BillingModelSourceRequested: "gpt-5.4", routing.BillingModelSourceGroupMapped: "route-model", routing.BillingModelSourceUpstream: "catalog-image"}[stage]
			policies := &batchCataloguePolicyReads{PricingConfigService: newPublicPricingConfigFixture(makePublicPricingConfigFixture(routing.PricingConfig{ID: 1, Status: "active", GroupIDs: []int64{group}, BillingModelSource: routing.BillingModelSourceUpstream}, nil, routing.GroupRoutingPolicy{Enabled: true, ModelMapping: map[string]string{"gpt-*": "route-model", "gpt-5.4": "route-model"}, RestrictModels: true, RestrictionModelSource: stage, AllowedModels: []string{allowed}}))}
			svc.PricingConfigService = policies
			value := testBatchImageMappedProvider(303, "apikey", map[string]any{"route-*": "catalog-image"})
			value.Credentials["model_whitelist"] = []string{"catalog-image"}
			svc.ProviderRepo = rebindBatchFixtureProviders(svc, &publicBatchImageProviderRepo{providers: []providercore.Record{value}})

			image, text := []string{"image"}, []string{"text"}
			svc.ModelOutputModalities = func(id string) *[]string {
				if id == "catalog-image" {
					return &image
				}
				return &text
			}
			svc.Pricing = &fakeBatchImagePricingResolver{unitPrice: 0.1}
			result, err := svc.ListModels(context.Background(), owner)
			require.NoError(t, err)
			ids := []string{}
			for _, model := range result.Data {
				ids = append(ids, model.ID)
			}
			require.Contains(t, ids, "gpt-5.4")
			require.NotContains(t, ids, "text-0")
			require.Equal(t, 1, policies.policyReads)
			require.Equal(t, 1, policies.pricingReads)
		})
	}
}

// TestBatchImageCatalogueRejectsMissingPrice 模态合格的映射仍需有效图片报价。
func TestBatchImageCatalogueRejectsMissingPrice(t *testing.T) {
	svc, _, _, _, _, _ := newTestBatchImagePublicService(true)
	value := testBatchImageMappedProvider(303, "apikey", map[string]any{"gpt-*": "catalog-image"})
	svc.ProviderRepo = rebindBatchFixtureProviders(svc, &publicBatchImageProviderRepo{providers: []providercore.Record{value}})
	image := []string{"image"}
	svc.ModelOutputModalities = func(string) *[]string { return &image }
	svc.Pricing = &fakeBatchImagePricingResolver{err: batchimage.ErrBatchImageSettlementPricingMissing}
	result, err := svc.ListModels(context.Background(), testBatchImageOwner())
	require.NoError(t, err)
	require.Empty(t, result.Data)
}

// TestBatchImageAutomaticCandidates 未配置的目录型号不参与候选，映射到文本的型号被过滤。
func TestBatchImageAutomaticCandidates(t *testing.T) {
	svc, _, _, _, _, _ := newTestBatchImagePublicService(true)
	value := testBatchImageMappedProvider(303, "apikey", map[string]any{"source-image": "final-text"})
	svc.ProviderRepo = rebindBatchFixtureProviders(svc, &publicBatchImageProviderRepo{providers: []providercore.Record{value}})
	image, text := []string{"image"}, []string{"text"}
	svc.ModelOutputModalities = func(id string) *[]string {
		switch id {
		case "catalog-image", "source-image":
			return &image
		case "final-text":
			return &text
		default:
			return nil
		}
	}
	svc.Pricing = &fakeBatchImagePricingResolver{unitPrice: 0.1}
	result, err := svc.ListModels(context.Background(), testBatchImageOwner())
	require.NoError(t, err)
	require.Empty(t, result.Data)
}

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

func TestBatchImagePublicService_Submit(t *testing.T) {
	ctx := context.Background()

	t.Run("rejects when disabled", func(t *testing.T) {
		svc, _, _, _, _, _ := newTestBatchImagePublicService(false)
		_, err := svc.Submit(ctx, testBatchImageOwner(), validBatchImageSubmitRequest(), "")
		require.ErrorIs(t, err, batchimage.ErrBatchImageDisabled)
	})

	t.Run("accepts valid request stores refs and enqueues once", func(t *testing.T) {
		svc, repo, queue, gemini, _, authCache := newTestBatchImagePublicService(true)
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
		require.Equal(t, int64(202), *job.ProviderID)
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
		svc, repo, _, _, _, _ := newTestBatchImagePublicService(true)
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
		svc, repo, _, gemini, _, _ := newTestBatchImagePublicService(true)
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
			source string
			want   string
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
		svc, repo, _, _, _, _ := newTestBatchImagePublicService(true)
		groupID := int64(7)
		providerMultiplier := 1.25
		providerRepo := testassert.MustType[*publicBatchImageProviderRepo](testassert.MustType[*batchProviderFixture](svc.ProviderRepo).source)
		providerRepo.providers[1].RateMultiplier = &providerMultiplier
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
		// 配置的 hold(0.6) < discount(0.8) 属于会导致结算死锁的脏数据，
		// 快照时被钳制为 discount，保证 holdAmount >= 实际成本上限。
		require.InDelta(t, 0.8, job.HoldMultiplier, 1e-12)
		require.InDelta(t, 0.125, job.BillableUnitPrice, 1e-12)
		require.InDelta(t, 0.125, job.HoldUnitPrice, 1e-12)
		require.InDelta(t, 0.25, *job.HoldAmount, 1e-12)
	})

	t.Run("uses subscription plan group rate before user balance rate", func(t *testing.T) {
		svc, repo, _, _, _, _ := newTestBatchImagePublicService(true)
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
		svc, repo, _, _, _, _ := newTestBatchImagePublicService(true)
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
		svc, repo, queue, gemini, _, _ := newTestBatchImagePublicService(true)
		svc.Pricing = &fakeBatchImagePricingResolver{err: batchimage.ErrBatchImageSettlementPricingMissing}

		_, err := svc.Submit(ctx, testBatchImageOwner(), validBatchImageSubmitRequest(), "")
		require.ErrorIs(t, err, batchimage.ErrBatchImageSettlementPricingMissing)
		require.Empty(t, repo.jobs)
		require.Empty(t, queue.enqueued)
		require.Empty(t, gemini.submits)
	})

	t.Run("group batch image disabled rejects before platform submit", func(t *testing.T) {
		svc, repo, queue, gemini, _, _ := newTestBatchImagePublicService(true)
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
		svc, repo, queue, gemini, _, _ := newTestBatchImagePublicService(true)
		groupID := int64(404)

		_, err := svc.Submit(ctx, batchimage.BatchImageOwner{UserID: 11, APIKeyID: 22, GroupID: &groupID}, validBatchImageSubmitRequest(), "")
		require.ErrorIs(t, err, batchimage.ErrBatchImageSettlementPricingMissing)
		require.Empty(t, repo.jobs)
		require.Empty(t, queue.enqueued)
		require.Empty(t, gemini.submits)
	})

	t.Run("generates custom ids deterministically", func(t *testing.T) {
		svc, _, _, gemini, _, _ := newTestBatchImagePublicService(true)
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
		svc, repo, _, gemini, _, _ := newTestBatchImagePublicService(true)
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
			name   string
			mutate func(*batchimage.BatchImageSubmitRequest)
			want   error
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
				svc, _, _, _, _, _ := newTestBatchImagePublicService(true)
				req := validBatchImageSubmitRequest()
				tt.mutate(&req)

				_, err := svc.Submit(ctx, testBatchImageOwner(), req, "")
				require.ErrorIs(t, err, tt.want)
			})
		}
	})

	t.Run("rejects too many items", func(t *testing.T) {
		svc, _, _, _, _, _ := newTestBatchImagePublicService(true)
		req := validBatchImageSubmitRequest()
		req.Items = append(req.Items, batchimage.BatchImageSubmitItem{CustomID: "too_many", Prompt: "x"})

		_, err := svc.Submit(ctx, testBatchImageOwner(), req, "")
		require.ErrorIs(t, err, batchimage.ErrBatchImageInvalidItems)
	})

	t.Run("rejects too many output images", func(t *testing.T) {
		svc, _, _, _, _, _ := newTestBatchImagePublicService(true)
		svc.Options.MaxOutputImagesPerJob = 3
		req := validBatchImageSubmitRequest()
		req.Items[0].OutputCount = 2
		req.Items[1].OutputCount = 2

		_, err := svc.Submit(ctx, testBatchImageOwner(), req, "")
		require.ErrorIs(t, err, batchimage.ErrBatchImageTooManyOutputImages)
	})

	t.Run("rejects too many reference images across request", func(t *testing.T) {
		svc, _, _, _, _, _ := newTestBatchImagePublicService(true)
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
		svc, _, _, _, _, _ := newTestBatchImagePublicService(true)
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
		svc, _, _, gemini, vertex, _ := newTestBatchImagePublicService(true)
		req := validBatchImageSubmitRequest()
		req.Platform = batchimage.BatchImageProviderVertex

		got, err := svc.Submit(ctx, testBatchImageOwner(), req, "")
		require.NoError(t, err)
		require.Equal(t, batchimage.BatchImageProviderVertex, got.Platform)
		require.Empty(t, gemini.submits)
		require.Len(t, vertex.submits, 1)
	})

	t.Run("insufficient balance rejects before platform submit", func(t *testing.T) {
		svc, repo, queue, gemini, _, _ := newTestBatchImagePublicService(true)
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
			name     string
			err      error
			wantCode string
		}{
			{name: "unavailable", err: apikey.ErrPreferredSubscriptionInvalid, wantCode: "PREFERRED_SUBSCRIPTION_INVALID"},
			{name: "group not allowed", err: apikey.ErrPreferredSubscriptionGroup, wantCode: "PREFERRED_SUBSCRIPTION_GROUP_NOT_ALLOWED"},
			{name: "quota exhausted", err: apikey.ErrPreferredSubscriptionInsufficient, wantCode: "PREFERRED_SUBSCRIPTION_EXHAUSTED"},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				svc, repo, queue, gemini, _, _ := newTestBatchImagePublicService(true)
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
		svc, repo, queue, gemini, _, _ := newTestBatchImagePublicService(true)
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
		svc, repo, queue, gemini, _, _ := newTestBatchImagePublicService(true)
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
		svc, repo, queue, _, _, _ := newTestBatchImagePublicService(true)
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
		svc, repo, queue, gemini, _, _ := newTestBatchImagePublicService(true)
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
		svc, _, _, _, _, _ := newTestBatchImagePublicService(true)
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
		svc, _, _, _, _, _ := newTestBatchImagePublicService(true)
		got, err := svc.Submit(ctx, testBatchImageOwner(), validBatchImageSubmitRequest(), "")
		require.NoError(t, err)

		body, err := json.Marshal(got)
		require.NoError(t, err)
		requireBatchImagePublicJSONHasNoInternals(t, string(body))
	})
}

func TestBatchImagePublicService_List(t *testing.T) {
	ctx := context.Background()
	svc, repo, _, _, _, _ := newTestBatchImagePublicService(true)
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

func TestBatchImagePublicService_ListModels(t *testing.T) {
	ctx := context.Background()

	t.Run("requires explicit provider model mapping", func(t *testing.T) {
		svc, _, _, _, _, _ := newTestBatchImagePublicService(true)

		got, err := svc.ListModels(ctx, testBatchImageOwner())
		require.NoError(t, err)
		require.Equal(t, "list", got.Object)
		require.Empty(t, got.Data)
	})

	t.Run("returns priced models from selected provider group", func(t *testing.T) {
		svc, _, _, _, _, _ := newTestBatchImagePublicService(true)
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
		providerRepo := testassert.MustType[*publicBatchImageProviderRepo](testassert.MustType[*batchProviderFixture](svc.ProviderRepo).source)
		providerRepo.providers = []providercore.Record{testBatchImageMappedProvider(303, capability.ProviderTypeAPIKey, map[string]any{
			"gemini-2.5-flash-image": "gemini-2.5-flash-image",
		})}

		got, err := svc.ListModels(ctx, batchimage.BatchImageOwner{UserID: 11, APIKeyID: 22, GroupID: &groupID})
		require.NoError(t, err)
		require.Equal(t, []batchimage.BatchImagePublicModel{{
			ID:       "gemini-2.5-flash-image",
			Object:   "image.batch.model",
			Platform: batchimage.BatchImageProviderGeminiAPI,
		}, {
			ID:       "gemini-2.5-flash-image",
			Object:   "image.batch.model",
			Platform: batchimage.BatchImageProviderVertex,
		}}, got.Data)
	})

	t.Run("wildcard mappings enumerate configured targets", func(t *testing.T) {
		svc, _, _, _, _, _ := newTestBatchImagePublicService(true)
		providerRepo := testassert.MustType[*publicBatchImageProviderRepo](testassert.MustType[*batchProviderFixture](svc.ProviderRepo).source)
		providerRepo.providers = []providercore.Record{testBatchImageMappedProvider(303, capability.ProviderTypeAPIKey, map[string]any{
			"gemini-3.1-*": "gemini-3.1-flash-lite-image",
		})}

		got, err := svc.ListModels(ctx, testBatchImageOwner())
		require.NoError(t, err)
		require.NotEmpty(t, got.Data)
		ids := make([]string, 0, len(got.Data))
		for _, model := range got.Data {
			ids = append(ids, model.ID)
		}
		require.NotContains(t, ids, "gemini-3.1-flash-image")
		require.Contains(t, ids, "gemini-3.1-flash-lite-image")
		require.NotContains(t, ids, "gemini-2.5-flash-image")
	})

	t.Run("filters models without batch image pricing", func(t *testing.T) {
		svc, _, _, _, _, _ := newTestBatchImagePublicService(true)
		svc.Pricing = &fakeBatchImagePricingResolver{
			unitPrice:     0.25,
			missingModels: map[string]bool{"gemini-3.1-flash-lite-image": true},
		}
		providerRepo := testassert.MustType[*publicBatchImageProviderRepo](testassert.MustType[*batchProviderFixture](svc.ProviderRepo).source)
		providerRepo.providers = []providercore.Record{testBatchImageMappedProvider(303, capability.ProviderTypeAPIKey, map[string]any{
			"gemini-2.5-flash-image":      "gemini-2.5-flash-image",
			"gemini-3.1-flash-lite-image": "gemini-3.1-flash-lite-image",
		})}

		got, err := svc.ListModels(ctx, testBatchImageOwner())
		require.NoError(t, err)
		ids := make([]string, 0, len(got.Data))
		for _, model := range got.Data {
			ids = append(ids, model.ID)
		}
		require.Contains(t, ids, "gemini-2.5-flash-image")
		require.NotContains(t, ids, "gemini-3.1-flash-lite-image")
	})

	t.Run("rejects when group disables batch image", func(t *testing.T) {
		svc, _, _, _, _, _ := newTestBatchImagePublicService(true)
		groupID := int64(7)
		svc.GroupRepo = batchGroupReader{&publicBatchImageGroupRepo{groups: map[int64]*batchimage.GroupView{
			groupID: {ID: groupID, AllowBatchImageGeneration: false},
		}}}

		_, err := svc.ListModels(ctx, batchimage.BatchImageOwner{UserID: 11, APIKeyID: 22, GroupID: &groupID})
		require.ErrorIs(t, err, batchimage.ErrBatchImageGroupDisabled)
	})
}

func TestBatchImagePublicService_StatusItemsAndCancel(t *testing.T) {
	ctx := context.Background()

	t.Run("status is owner scoped and maps public status", func(t *testing.T) {
		svc, repo, _, _, _, _ := newTestBatchImagePublicService(true)
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
		svc, repo, _, _, _, _ := newTestBatchImagePublicService(true)
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
		svc, repo, queue, gemini, _, _ := newTestBatchImagePublicService(true)
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
		svc, repo, _, gemini, _, _ := newTestBatchImagePublicService(true)
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
		svc, repo, _, gemini, _, _ := newTestBatchImagePublicService(true)
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

// TestBatchImageServiceAccountErrorCode 验证 Google Service Account 是第三方凭据类型，公开错误码保持原协议名称。
func TestBatchImageServiceAccountErrorCode(t *testing.T) {
	err := batchimage.BatchImageProviderSubmitPublicError(batchimage.ErrBatchImageProviderMissingServiceAccount)
	require.Equal(t, "BATCH_IMAGE_PROVIDER_MISSING_SERVICE_ACCOUNT", apperror.Reason(err))
}

// TestBatchImageConfiguredAlias 保留有价格的自定义图片型号，并检查提供商白名单。
func TestBatchImageConfiguredAlias(t *testing.T) {
	for _, allowed := range []bool{true, false} {
		t.Run(fmt.Sprintf("allowed=%t", allowed), func(t *testing.T) {
			svc, _, _, _, _, _ := newTestBatchImagePublicService(true)
			repo := testassert.MustType[*publicBatchImageProviderRepo](testassert.MustType[*batchProviderFixture](svc.ProviderRepo).source)
			value := testBatchImageMappedProvider(303, capability.ProviderTypeAPIKey, map[string]any{"public-image": "nano-banana-pro"})
			if !allowed {
				value.Credentials["model_whitelist"] = []string{"other-model"}
			}
			repo.providers = []providercore.Record{value}
			svc.Pricing = &fakeBatchImagePricingResolver{unitPrice: 0.1}
			result, err := svc.ListModels(context.Background(), testBatchImageOwner())
			require.NoError(t, err)
			ids := []string{}
			for _, model := range result.Data {
				ids = append(ids, model.ID)
			}
			if allowed {
				require.Contains(t, ids, "public-image")
				require.Contains(t, ids, "nano-banana-pro")
			} else {
				require.NotContains(t, ids, "public-image")
				require.NotContains(t, ids, "nano-banana-pro")
			}
		})
	}
}

// GetGroupPolicy 统计策略读取次数。
func (p *batchCataloguePolicyReads) GetGroupPolicy(ctx context.Context, id int64) (*routing.GroupPolicyView, error) {
	p.policyReads++
	policy, err := p.PricingConfigService.GetGroupPolicy(ctx, id)
	if policy != nil {
		policy.ModelsList = p.modelsList
	}
	return policy, err
}

// GetPricingConfigForGroup 统计计费来源读取次数。
func (p *batchCataloguePolicyReads) GetPricingConfigForGroup(ctx context.Context, id int64) (*routing.PricingConfig, error) {
	p.pricingReads++
	return p.PricingConfigService.GetPricingConfigForGroup(ctx, id)
}

func (r *changingMediaPricingGroupRepo) GetByIDLite(context.Context, int64) (*batchimage.GroupView, error) {
	r.calls++
	if r.calls == 1 {
		return r.oldGroup, nil
	}
	return r.newGroup, nil
}

func testPtrFloat64(value float64) *float64 { return &value }

func rebindBatchFixtureProviders(core *batchimage.Public, source batchProvidersFixtureSource) batchimage.ProviderReader {
	return &batchProviderFixture{source: source, registry: testassert.MustType[*batchProviderFixture](core.ProviderRepo).registry}
}

func (r *publicPricingConfigFixture) ListAll(context.Context) ([]routing.PricingConfig, error) {
	return []routing.PricingConfig{r.pricingConfig}, nil
}

func (r *publicPricingConfigFixture) GetGroupPlatforms(context.Context, []int64) (map[int64]string, error) {
	return r.platforms, nil
}

func makePublicPricingConfigFixture(pricingConfig routing.PricingConfig, platforms map[int64]string, policies ...routing.GroupRoutingPolicy) *publicPricingConfigFixture {
	policy := routing.GroupRoutingPolicy{}
	if len(policies) > 0 {
		policy = policies[0]
	}
	return &publicPricingConfigFixture{pricingConfig: pricingConfig, platforms: platforms, policy: policy}
}

func newPublicPricingConfigFixture(repo *publicPricingConfigFixture) *routing.PricingConfigService {
	return routing.NewPricingConfigService(repo, nil, routing.PricingConfigOptions{Warn: slog.Warn, Now: time.Now, LoadLocation: pricingprovider.LoadPricingLocation, ReadGroup: func(_ context.Context, id int64) (*routing.Group, error) {
		return &routing.Group{ID: id, RoutingPolicy: repo.policy.Clone()}, nil
	}})
}

func testImageModelPricing(prices map[string]*float64) []routing.ModelPricingEntry {
	card := routing.ModelPricingEntry{Models: []string{"*"}, BillingMode: routing.BillingModeImage}
	for tier, price := range prices {
		card.Intervals = append(card.Intervals, routing.PricingInterval{TierLabel: tier, PerRequestPrice: price})
	}
	return []routing.ModelPricingEntry{card}
}

func newTestBatchImagePublicService(enabled bool) (*batchimage.Public, *fakeBatchImageRepository, *publicBatchImageQueue, *publicBatchImageProvider, *publicBatchImageProvider, *fakeBatchImageAuthCacheInvalidator) {
	repo := newFakeBatchImageRepository()
	queue := &publicBatchImageQueue{}
	gemini := &publicBatchImageProvider{name: batchimage.BatchImageProviderGeminiAPI}
	vertex := &publicBatchImageProvider{name: batchimage.BatchImageProviderVertex}
	authCache := &fakeBatchImageAuthCacheInvalidator{}
	svc := newBatchPublicFixture(repo,
		&publicBatchImageProviderRepo{providers: []providercore.Record{testBatchImageProvider(101, capability.ProviderTypeAPIKey), testBatchImageProvider(202, capability.ProviderTypeServiceAccount)}}, nil, nil, nil, queue,
		batchimage.NewRegistry[batchimageprovider.BatchImageProvider](
			gemini,
			vertex,
		),
		&fakeBatchImagePricingResolver{unitPrice: 0.25},
		&fakeBatchImageBillingRepo{},
		authCache,
		&config.Config{BatchImage: config.BatchImageConfig{
			Enabled:                 enabled,
			MaxItemsPerJobDefault:   2,
			MaxPromptCharsPerItem:   8,
			DefaultResponseMimeType: "image/png",
			DefaultImageSize:        "1K",
		}})

	return svc, repo, queue, gemini, vertex, authCache
}

func (f *fakeBatchImageAuthCacheInvalidator) InvalidateAuthCacheByKey(_ context.Context, key string) {
	f.keys = append(f.keys, key)
}

func (f *fakeBatchImageAuthCacheInvalidator) InvalidateAuthCacheByUserID(_ context.Context, userID int64) {
	f.userIDs = append(f.userIDs, userID)
}

func (f *fakeBatchImageAuthCacheInvalidator) InvalidateAuthCacheByGroupID(_ context.Context, groupID int64) {
	f.groupIDs = append(f.groupIDs, groupID)
}

func testBatchImageMappedProvider(id int64, providerType string, mapping map[string]any) providercore.Record {
	provider := testBatchImageProvider(id, providerType)
	provider.Credentials["model_mapping"] = mapping
	return provider
}

// BenchmarkBatchImageConfiguredCatalogue 测量 65 个提供商的有限目录，包含规则准备及模态检查。
func BenchmarkBatchImageConfiguredCatalogue(b *testing.B) {
	service, _, _, _, _, _ := newTestBatchImagePublicService(true)
	rows := &publicBatchImageProviderRepo{}
	whitelist := make([]any, 186)
	mapping := map[string]any{}
	for i := range whitelist {
		model := fmt.Sprintf("probe-model-%03d", i)
		if i == 0 {
			model = "gemini-2.5-flash-image"
		}
		whitelist[i] = model
		mapping[model] = model
	}
	for i := range 65 {
		value := testBatchImageMappedProvider(int64(i+1), "apikey", mapping)
		value.Credentials["model_whitelist"] = whitelist
		rows.providers = append(rows.providers, value)
	}
	service.ProviderRepo = rebindBatchFixtureProviders(service, rows)
	service.ProviderExists = func(name string) bool { return name == batchimage.BatchImageProviderGeminiAPI }
	image, text := []string{"image"}, []string{"text"}
	service.ModelOutputModalities = func(model string) *[]string {
		if model == "gemini-2.5-flash-image" {
			return &image
		}
		return &text
	}
	service.Pricing = &fakeBatchImagePricingResolver{unitPrice: 0.1}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		result, err := service.ListModels(context.Background(), testBatchImageOwner())
		if err != nil {
			b.Fatal(err)
		}
		if len(result.Data) != 1 {
			b.Fatalf("models=%d", len(result.Data))
		}
	}
}

// TestBatchImageCustomListDeclaresUnknownModality 自定义列表中的具体型号可在有价格时作为未知模态候选。
func TestBatchImageCustomListDeclaresUnknownModality(t *testing.T) {
	service, _, _, _, _, _ := newTestBatchImagePublicService(true)
	owner := testBatchImageOwner()
	value := testBatchImageMappedProvider(303, "apikey", nil)
	service.ProviderRepo = rebindBatchFixtureProviders(service, &publicBatchImageProviderRepo{providers: []providercore.Record{value}})
	service.PricingConfigService = &batchCataloguePolicyReads{PricingConfigService: newPublicPricingConfigFixture(makePublicPricingConfigFixture(routing.PricingConfig{ID: 1, Status: "active", GroupIDs: []int64{*owner.GroupID}}, nil, routing.GroupRoutingPolicy{Enabled: true})), modelsList: []string{"custom-image"}}
	service.Pricing = &fakeBatchImagePricingResolver{unitPrice: 0.1}
	result, err := service.ListModels(context.Background(), owner)
	require.NoError(t, err)
	require.NotEmpty(t, result.Data)
	for _, model := range result.Data {
		require.Equal(t, "custom-image", model.ID)
	}
}
