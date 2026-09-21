package catalogue_test

import (
	"context"
	"testing"

	routingtestkit "github.com/TokenFlux/TokenRouter/internal/routing/testkit"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

func TestModelMarketplaceQoderAccountMappedCustomModelUsesRouteKeyManualPricing(t *testing.T) {
	groupID := int64(903)
	inputPrice := 0.01
	outputPrice := 0.02
	channelService := routingtestkit.Channel(groupID, capability.PlatformQoder, routing.Channel{ID: groupID, Status: billing.StatusActive, BillingModelSource: routing.BillingModelSourceUpstream, ModelPricing: []routing.ChannelModelPricing{{Platform: capability.PlatformQoder, Models: []string{"qmodel"}, BillingMode: routing.BillingModeToken, InputPrice: &inputPrice, OutputPrice: &outputPrice}}})

	billingService := billingtestkit.Calculator(0, nil, nil)
	svc := newCatalogueMarketplace(nil, newCatalogueFixture(&modelsListAccountRepoStub{byGroup: map[int64][]accountcore.Record{
		groupID: {
			{
				ID:       1,
				Platform: capability.PlatformQoder,
				Type:     capability.AccountTypeCosy,
				Credentials: map[string]any{
					"model_mapping": map[string]any{
						"custom-qoder-model": "qmodel",
					},
				},
			},
		},
	}}, channelService, cataloguePriceResolver(channelService, billingService)), billingService)
	group := &routing.Group{ID: groupID, Platform: capability.PlatformQoder, RateMultiplier: 1}

	models := svc.ModelsForGroup(context.Background(), group)

	var customModel *routing.ModelMarketplaceModel
	for i := range models {
		if models[i].ID == "custom-qoder-model" {
			customModel = &models[i]
			break
		}
	}
	if customModel == nil {
		t.Fatalf("Qoder account-mapped marketplace models = %#v, want custom-qoder-model", models)
	}
	pricing := customModel.Pricing
	if pricing.InputPricePerToken != inputPrice || pricing.OutputPricePerToken != outputPrice {
		t.Fatalf("Qoder account-mapped route key display price = (%g, %g), want (%g, %g)", pricing.InputPricePerToken, pricing.OutputPricePerToken, inputPrice, outputPrice)
	}
}

func TestModelMarketplaceListPublicPrefetchesAccountsOnce(t *testing.T) {
	groups := []routing.Group{
		{ID: 4101, Name: "OpenAI A", Platform: capability.PlatformOpenAI, Status: billing.StatusActive, RateMultiplier: 1, ActiveAccountCount: 1},
		{ID: 4102, Name: "OpenAI B", Platform: capability.PlatformOpenAI, Status: billing.StatusActive, RateMultiplier: 1, ActiveAccountCount: 1},
	}
	accounts := []accountcore.Record{
		{
			ID:       5101,
			Platform: capability.PlatformOpenAI,
			GroupIDs: []int64{4101},
			AccountGroups: []accountcore.GroupMembership{{
				AccountID: 5101,
				GroupID:   4101,
			}},
			Credentials: map[string]any{
				"model_mapping":   map[string]any{"client-a": "upstream-a"},
				"model_whitelist": []any{"upstream-a"},
			},
		},
		{
			ID:       5102,
			Platform: capability.PlatformOpenAI,
			GroupIDs: []int64{4102},
			AccountGroups: []accountcore.GroupMembership{{
				AccountID: 5102,
				GroupID:   4102,
			}},
			Credentials: map[string]any{
				"model_mapping":   map[string]any{"client-b": "upstream-b"},
				"model_whitelist": []any{"upstream-b"},
			},
		},
	}
	accountRepo := &modelsListAccountRepoStub{
		all: accounts,
		byGroup: map[int64][]accountcore.Record{
			4101: {accounts[0]},
			4102: {accounts[1]},
		},
	}
	gatewayService := newCatalogueFixture(accountRepo, nil, nil)
	service := newCatalogueMarketplace(&marketplaceGroupRepoStub{groups: groups}, gatewayService, billingtestkit.Calculator(0, nil, nil))

	result, err := service.ListPublic(context.Background())
	if err != nil {
		t.Fatalf("ListPublic returned error: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("ListPublic returned %d groups, want 2", len(result))
	}
	if accountRepo.listAllCalls.Load() != 1 || accountRepo.listByGroupCalls.Load() != 0 {
		t.Fatalf("account queries = all:%d by_group:%d, want all:1 by_group:0", accountRepo.listAllCalls.Load(), accountRepo.listByGroupCalls.Load())
	}
	groupAModels := make(map[string]struct{}, len(result[0].Models))
	for _, model := range result[0].Models {
		groupAModels[model.ID] = struct{}{}
	}
	groupBModels := make(map[string]struct{}, len(result[1].Models))
	for _, model := range result[1].Models {
		groupBModels[model.ID] = struct{}{}
	}
	if _, ok := groupAModels["client-a"]; !ok {
		t.Fatalf("group A models = %#v, want client-a", result[0].Models)
	}
	if _, leaked := groupAModels["client-b"]; leaked {
		t.Fatalf("group A models = %#v, must not contain client-b", result[0].Models)
	}
	if _, ok := groupBModels["client-b"]; !ok {
		t.Fatalf("group B models = %#v, want client-b", result[1].Models)
	}
	if _, leaked := groupBModels["client-a"]; leaked {
		t.Fatalf("group B models = %#v, must not contain client-a", result[1].Models)
	}
}

func TestModelMarketplacePrefetchSortsByGlobalAccountPriority(t *testing.T) {
	accountRepo := &modelsListAccountRepoStub{all: []accountcore.Record{
		{ID: 5103, Priority: 10, GroupIDs: []int64{4101}},
		{ID: 5102, Priority: 5, GroupIDs: []int64{4101}},
		{ID: 5101, Priority: 5, GroupIDs: []int64{4101}},
	}}
	svc := newCatalogueMarketplace(nil, newCatalogueFixture(accountRepo, nil, nil), nil)

	accountsByGroup, ok := svc.PrefetchAccounts(context.Background())

	if !ok {
		t.Fatal("prefetchPublicGroupAccounts should succeed")
	}
	accounts := accountsByGroup[4101]
	if len(accounts) != 3 {
		t.Fatalf("prefetched accounts = %d, want 3", len(accounts))
	}
	if accounts[0].ID != 5101 || accounts[1].ID != 5102 || accounts[2].ID != 5103 {
		t.Fatalf("prefetched account order = [%d %d %d], want [5101 5102 5103]", accounts[0].ID, accounts[1].ID, accounts[2].ID)
	}
}

type marketplaceGroupRepoStub struct {
	routing.GroupRepository

	groups []routing.Group
}

func (s *marketplaceGroupRepoStub) ListActive(context.Context) ([]routing.Group, error) {
	return s.groups, nil
}
