package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	pricingprovider "github.com/TokenFlux/TokenRouter/internal/billing/provider"
	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	catalogtest "github.com/TokenFlux/TokenRouter/internal/modelcatalog/testkit"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/routing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/upstream/gemini"
)

// gateway_models_test.go checks configured provider candidates, group policy,
// and Key aliases in gateway model lists. The scenarios exercise
// mixed platforms, custom list filtering/order, composite prefixes and
// subscription access, native Gemini display, and local model routes with
// protocol-specific response fields.
//
// The grouped provider fixture clones records before supplying missing
// type/status defaults and derived whitelist targets. An existing whitelist
// key, including an empty value, remains configured; only absent whitelists are
// derived from nonempty mapping targets. This setup makes mapping-only test
// providers executable candidates while leaving unconfigured directories empty,
// and catalogue fixtures independently supply names and reasoning metadata for
// the displayed candidates.

// modelHTTPProviderRows supplies provider candidate reads for local gateway
// model catalogues.
type modelHTTPProviderRows interface {
	// ListSchedulable reads configured provider candidates when no group scope is selected.
	ListSchedulable(context.Context) ([]provider.Record, error)
	// ListSchedulableByGroupID reads configured provider candidates for the requested group identity.
	ListSchedulableByGroupID(context.Context, int64) ([]provider.Record, error)
}

// gatewayModelsProviderRepoStub supplies normalized provider candidates for a
// requested fixture group.
type gatewayModelsProviderRepoStub struct {
	// modelHTTPProviderRows supplies unused ungrouped behavior. This fixture
	// implements grouped reads; tests provide a group before its configured candidate normalization runs.
	modelHTTPProviderRows

	byGroup map[int64][]provider.Record // grouped candidate records
}

// gatewayModelsPricingConfigRepoStub supplies configuration rows and group
// platforms for model-policy fixtures.
type gatewayModelsPricingConfigRepoStub struct {
	// PricingConfigRepository supplies unused operations; this fixture implements
	// row and platform reads needed by testkit configuration-to-group policy assembly.
	routing.PricingConfigRepository

	modelConfigs   []testkit.Configuration // stored pricing fixtures
	groupPlatforms map[int64]string        // configured group platforms
}

// gatewayModelsResponseForTest decodes the local model-list envelope used by
// these scenarios.
type gatewayModelsResponseForTest struct {
	// protocol object category
	Object string                    `json:"object"`
	Data   []gatewayModelItemForTest `json:"data"` // decoded model list
}

// gatewayModelItemForTest decodes model identity, display, and reasoning
// compatibility fields.
type gatewayModelItemForTest struct {
	// public model identity
	ID string `json:"id"`
	// protocol object category
	Object string `json:"object"`
	// legacy model creation value
	Created int64 `json:"created"`
	// catalog provider identity
	OwnedBy string `json:"owned_by"`
	// model entry category
	Type string `json:"type"`
	// catalog model display name
	DisplayName string `json:"display_name"`
	// catalog creation date text
	CreatedAt string `json:"created_at"`
	// reasoning selection availability flag
	SupportsReasoningEffort bool `json:"supportsReasoningEffort"`
	// default reasoning effort value
	ReasoningEffort string `json:"reasoningEffort"`
	// available reasoning effort options
	ReasoningEfforts []gatewayReasoningEffortOptionForTest `json:"reasoningEfforts"`
}

// gatewayReasoningEffortOptionForTest represents one decoded model reasoning
// choice and default marker.
type gatewayReasoningEffortOptionForTest struct {
	Value   string `json:"value"`   // reasoning option value
	Label   string `json:"label"`   // reasoning display label
	Default bool   `json:"default"` // default effort selection flag
}

// codexModelsRemovalProviderRepo supplies local API-Key model candidates for
// Codex route tests.
type codexModelsRemovalProviderRepo struct {
	// modelHTTPProviderRows supplies unused ungrouped behavior; the route tests
	// use this fixture's grouped read for locally configured API-Key model candidates.
	modelHTTPProviderRows
	providers []provider.Record // configured local providers
}

// TestGeminiV1BetaListUsesMixedGroupCapabilitiesAndAliases lists Gemini
// candidates from a mixed group with custom choices and Key aliases. It checks
// supported models and alias response shapes, then individual lookup accepts
// only the listed available Gemini names.
func TestGeminiV1BetaListUsesMixedGroupCapabilitiesAndAliases(t *testing.T) {
	groupID := int64(42)
	source := &gatewayModelsProviderRepoStub{byGroup: map[int64][]provider.Record{groupID: {
		{ID: 1, Platform: "gemini", Type: "apikey", Credentials: map[string]any{"model_whitelist": []string{"gemini-2.5-pro", "gemini-custom", "gemini-2.5-flash"}}},
		{ID: 2, Platform: "anthropic", Type: "apikey", Credentials: map[string]any{"model_whitelist": []string{"claude-sonnet-4-6"}}},
	}}}
	handler := newGatewayModelsHandlerForTest(source)
	key := &apikey.APIKey{GroupID: &groupID, Group: &routing.Group{ID: groupID, AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolGeminiGenerateContent}, ModelsListConfig: routing.GroupModelsListConfig{Enabled: true, Models: []string{"gemini-2.5-pro", "gemini-custom", "phantom", "claude-sonnet-4-6"}}}, ModelMapping: map[string]string{"my-gemini": "gemini-2.5-pro", "custom-alias": "gemini-custom", "unlisted-alias": "gemini-2.5-flash", "wildcard-*": "gemini-2.5-pro"}}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1beta/models", nil)
	c.Set(string(keyhttp.ContextKeyAPIKey), key)
	handler.GeminiV1BetaListModels(c)
	require.Equal(t, 200, rec.Code)
	var got gemini.ModelsListResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got.Models, 4)
	pro := gemini.Model{Name: "models/gemini-2.5-pro", DisplayName: "gemini-2.5-pro", SupportedGenerationMethods: []string{"generateContent", "streamGenerateContent"}}
	custom := gemini.Model{Name: "models/gemini-custom", DisplayName: "gemini-custom", SupportedGenerationMethods: []string{"generateContent", "streamGenerateContent"}}
	require.Equal(t, []gemini.Model{pro, custom}, got.Models[:2])
	pro.Name, pro.DisplayName = "models/my-gemini", "my-gemini"
	custom.Name, custom.DisplayName = "models/custom-alias", "custom-alias"
	require.ElementsMatch(t, []gemini.Model{pro, custom}, got.Models[2:])
	for _, test := range []struct {
		name   string // requested model identity
		status int    // expected resource status
	}{{"my-gemini", 200}, {"gemini-2.5-pro", 200}, {"phantom", 404}, {"gemini-2.5-flash", 404}, {"claude-sonnet-4-6", 404}} {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, "/v1beta/models/"+test.name, nil)
		c.Params = gin.Params{{Key: "model", Value: "/" + test.name}}
		c.Set(string(keyhttp.ContextKeyAPIKey), key)
		handler.GeminiV1BetaGetModel(c)
		require.Equal(t, test.status, rec.Code, test.name)
	}
}

// TestGeminiV1BetaCustomListCannotInventProviders supplies a custom Gemini
// choice without a catalogue reader. It checks successful HTTP returns an empty
// model list rather than inventing a provider-backed candidate.
func TestGeminiV1BetaCustomListCannotInventProviders(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1beta/models", nil)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{Group: &routing.Group{ID: 42, AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolGeminiGenerateContent}, ModelsListConfig: routing.GroupModelsListConfig{Enabled: true, Models: []string{"gemini-2.5-pro"}}}})
	provideModelsHTTP(nil, nil, nil, nil, nil, nil).GeminiV1BetaListModels(c)
	require.Equal(t, 200, rec.Code)
	require.JSONEq(t, `{"models":[]}`, rec.Body.String())
}

// TestGeminiV1BetaForcedAntigravityKeepsGroupRestrictions forces Antigravity in
// a mixed custom group. It checks only the matching Antigravity model is listed
// and disabling the group protocol changes the response to Forbidden.
func TestGeminiV1BetaForcedAntigravityKeepsGroupRestrictions(t *testing.T) {
	groupID := int64(43)
	source := &gatewayModelsProviderRepoStub{byGroup: map[int64][]provider.Record{groupID: {
		{ID: 1, Platform: "antigravity", Type: "oauth", Credentials: map[string]any{"model_whitelist": []string{"gemini-3-flash"}}},
		{ID: 2, Platform: "gemini", Type: "apikey", Credentials: map[string]any{"model_whitelist": []string{"gemini-2.5-pro"}}},
	}}}
	handler := newGatewayModelsHandlerForTest(source)
	group := &routing.Group{ID: groupID, AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolGeminiGenerateContent}, ModelsListConfig: routing.GroupModelsListConfig{Enabled: true, Models: []string{"gemini-3-flash", "gemini-2.5-pro"}}}
	for _, allowed := range []bool{true, false} {
		if !allowed {
			group.AllowedProtocols = nil
		}
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodGet, "/antigravity/v1beta/models", nil)
		c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{GroupID: &groupID, Group: group})
		c.Set(string(keyhttp.ContextKeyForcePlatform), "antigravity")
		handler.GeminiV1BetaListModels(c)
		if !allowed {
			require.Equal(t, 403, rec.Code)
			continue
		}
		require.Equal(t, 200, rec.Code)
		var got gemini.ModelsListResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
		require.Len(t, got.Models, 1)
		require.Equal(t, "models/gemini-3-flash", got.Models[0].Name)
	}
}

// ListAll returns an outer slice copy of the fixture pricing rows. Nested
// configuration maps remain shared, and empty input yields an empty slice for
// the testkit pricing-policy initialization.
func (s *gatewayModelsPricingConfigRepoStub) ListAll(ctx context.Context) ([]testkit.Configuration, error) {
	modelConfigs := make([]testkit.Configuration, len(s.modelConfigs))
	copy(modelConfigs, s.modelConfigs)
	return modelConfigs, nil
}

// GetGroupPlatforms returns a new map of requested group IDs present in the
// fixture.
func (s *gatewayModelsPricingConfigRepoStub) GetGroupPlatforms(ctx context.Context, groupIDs []int64) (map[int64]string, error) {
	platforms := make(map[int64]string, len(groupIDs))
	for _, groupID := range groupIDs {
		if platform, ok := s.groupPlatforms[groupID]; ok {
			platforms[groupID] = platform
		}
	}
	return platforms, nil
}

// ListSchedulableByGroupID clones fixture rows and returns nil for a missing
// group. Missing provider type becomes API Key except Antigravity OAuth and
// Qoder COSY; missing status becomes active with scheduling enabled, and
// GroupIDs is replaced by the requested group.
//
// Existing status and whitelist keys remain supplied values. When whitelist is
// absent, nonempty string targets from either supported mapping representation
// form a derived whitelist. CloneRecord follows provider copying rules, so the
// fixture's normalization does not rewrite its stored supported-container
// configuration.
func (s *gatewayModelsProviderRepoStub) ListSchedulableByGroupID(ctx context.Context, groupID int64) ([]provider.Record, error) {
	providers, ok := s.byGroup[groupID]
	if !ok {
		return nil, nil
	}
	out := make([]provider.Record, len(providers))
	for i := range providers {
		out[i] = *provider.CloneRecord(&providers[i])
		if out[i].Type == "" {
			out[i].Type = "apikey"
			if out[i].Platform == "antigravity" {
				out[i].Type = "oauth"
			}
			if out[i].Platform == "qoder" {
				out[i].Type = "cosy"
			}
		}
		if out[i].Status == "" {
			out[i].Status = "active"
			out[i].Schedulable = true
		}
		out[i].GroupIDs = []int64{groupID}
		// Mapping-only fixtures need finite supported targets for candidate checks.
		// Derive a whitelist only when its key is absent, retaining explicitly configured empty whitelist behavior.
		if _, configured := out[i].Credentials["model_whitelist"]; !configured {
			var allowed []string
			switch mapping := out[i].Credentials["model_mapping"].(type) {
			case map[string]any:
				for _, value := range mapping {
					if model, ok := value.(string); ok && model != "" {
						allowed = append(allowed, model)
					}
				}
			case map[string]string:
				for _, model := range mapping {
					if model != "" {
						allowed = append(allowed, model)
					}
				}
			}
			if len(allowed) > 0 {
				out[i].Credentials["model_whitelist"] = allowed
			}
		}
	}
	return out, nil
}

// newGatewayModelsHandlerForTest delegates local model HTTP construction
// without supplied pricing rules.
func newGatewayModelsHandlerForTest(repo modelHTTPProviderRows) *gatewayhttp.ModelsHandler {
	return newGatewayModelsHandlerWithPricingConfigForTest(repo, nil)
}

// newGatewayModelsHandlerWithPricingConfigForTest combines candidate reads,
// offline attributes, and display metadata. Nil repo leaves Read unset; absent
// pricing service constructs permissive group protocol reads, while a supplied
// service replaces those model-policy inputs.
func newGatewayModelsHandlerWithPricingConfigForTest(repo modelHTTPProviderRows, modelConfigs *routing.PricingConfigService) *gatewayhttp.ModelsHandler {
	var read func(context.Context, *int64) ([]routing.CatalogueProvider, error)
	if repo != nil {
		read = func(ctx context.Context, id *int64) ([]routing.CatalogueProvider, error) {
			var values []provider.Record
			var err error
			if id != nil {
				values, err = repo.ListSchedulableByGroupID(ctx, *id)
			} else {
				values, err = repo.ListSchedulable(ctx)
			}
			if err != nil {
				return nil, err
			}
			return gatewayprovider.CatalogueProviders(values), nil
		}
	}
	var pricingConfigPort routing.CataloguePolicies = routing.NewPricingConfigService(modelCatalogueEmptyPrices{}, nil, routing.PricingConfigOptions{ReadGroup: func(_ context.Context, id int64) (*routing.Group, error) {
		return &routing.Group{ID: id, AllowedProtocols: capability.SupportedGroupClientProtocols("")}, nil
	}})
	if modelConfigs != nil {
		pricingConfigPort = modelConfigs
	}
	catalogue := &routing.RequestableCatalogue{Read: read, Resolver: routing.RequestableResolver{GroupPolicies: pricingConfigPort, Warn: slog.Warn}, Warn: slog.Warn}
	return modelsHTTP(gatewayModelCatalogFixture(), catalogue, nil, nil, nil, &routing.ModelAttributeService{Catalog: routing.ModelAttributeCatalog{View: catalogtest.OfflineView}})
}

// newGatewayModelsPricingConfigServiceForTest binds one configuration to the
// requested group and platform. It replaces GroupIDs on its outer value and
// passes fixture rows to testkit with production location, time, and warning
// callbacks.
func newGatewayModelsPricingConfigServiceForTest(groupID int64, platform string, pricingConfig testkit.Configuration) *routing.PricingConfigService {
	pricingConfig.GroupIDs = []int64{groupID}
	repo := &gatewayModelsPricingConfigRepoStub{
		modelConfigs:   []testkit.Configuration{pricingConfig},
		groupPlatforms: map[int64]string{groupID: platform},
	}
	return testkit.NewPricingConfigService(repo, nil, routing.PricingConfigOptions{
		Warn: slog.Warn,
		Now:  time.Now, LoadLocation: pricingprovider.
			LoadPricingLocation,
	},
	)
}

// TestGatewayModels_GeminiUnconfiguredDirectoryIsEmpty supplies an unconfigured
// Gemini provider and Key aliases. It checks a successful list response has no
// candidates and adds neither exact aliases, wildcard patterns, nor unrelated
// catalog models.
func TestGatewayModels_GeminiUnconfiguredDirectoryIsEmpty(t *testing.T) {
	groupID := int64(20)
	h := newGatewayModelsHandlerForTest(
		&gatewayModelsProviderRepoStub{
			byGroup: map[int64][]provider.Record{
				groupID: {
					{ID: 1, Platform: capability.PlatformGemini},
				},
			},
		},
	)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		Group:        &routing.Group{ID: groupID},
		ModelMapping: map[string]string{"gemini-review": "gemini-2.5-flash", "wild-*": "gemini-2.5-flash"},
	})

	h.Models(c)

	require.Equal(t, http.StatusOK, rec.Code)

	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, "list", got.Object)
	require.Empty(t, got.Data)
	require.NotContains(t, modelIDsForTest(got.Data), "gemini-review")
	require.NotContains(t, modelIDsForTest(got.Data), "wild-*")
	require.NotContains(t, modelIDsForTest(got.Data), "claude-sonnet-4-6")
}

// TestAntigravityModelsWithoutCatalogueReturnsEmpty supplies exact, wildcard,
// and unavailable Key mappings with no catalogue. It checks successful HTTP
// returns an empty local model list without exposing those names.
func TestAntigravityModelsWithoutCatalogueReturnsEmpty(t *testing.T) {
	targetModel := "claude-fable-5"

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/antigravity/models", nil)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{ModelMapping: map[string]string{
		"antigravity-review": targetModel,
		"wild-*":             targetModel,
		"missing":            "not-requestable",
	}})

	provideModelsHTTP(nil, nil, nil, nil, nil, nil).AntigravityModels(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &got))
	ids := modelIDsForTest(got.Data)
	require.Empty(t, ids)
	require.NotContains(t, ids, "wild-*")
	require.NotContains(t, ids, "missing")
}

// TestAntigravityModelsExcludesAliasWhoseTargetIsUnavailableToBoundGroup
// supplies one configured Antigravity target and two Key aliases. It checks the
// available target and alias appear while the unavailable target and its alias
// are omitted.
func TestAntigravityModelsExcludesAliasWhoseTargetIsUnavailableToBoundGroup(t *testing.T) {
	availableModel := "claude-fable-5"
	unavailableModel := "claude-sonnet-4-6"
	groupID := int64(46)
	h := newGatewayModelsHandlerForTest(&gatewayModelsProviderRepoStub{byGroup: map[int64][]provider.Record{
		groupID: {
			{
				ID:          9,
				Platform:    capability.PlatformAntigravity,
				Status:      billing.StatusActive,
				Schedulable: true,
				Credentials: map[string]any{
					"model_mapping": map[string]any{availableModel: availableModel},
				},
			},
		},
	}})

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/antigravity/models", nil)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		GroupID: &groupID,
		Group:   &routing.Group{ID: groupID},
		ModelMapping: map[string]string{
			"available-alias":   availableModel,
			"unavailable-alias": unavailableModel,
		},
	})

	h.AntigravityModels(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &got))
	ids := modelIDsForTest(got.Data)
	require.Contains(t, ids, availableModel)
	require.Contains(t, ids, "available-alias")
	require.NotContains(t, ids, unavailableModel)
	require.NotContains(t, ids, "unavailable-alias")
}

// TestGatewayModelsCompositeKeyAggregatesMappingsInOrder combines OpenAI and
// Anthropic groups with ordered prefixes. It checks available exact aliases,
// omission of unavailable/wildcard aliases, and all GPT-prefixed models
// preceding Claude-prefixed models in the returned list.
func TestGatewayModelsCompositeKeyAggregatesMappingsInOrder(t *testing.T) {
	openAIGroupID := int64(50)
	anthropicGroupID := int64(51)
	h := newGatewayModelsHandlerForTest(&gatewayModelsProviderRepoStub{byGroup: map[int64][]provider.Record{
		openAIGroupID: {
			{ID: 1, Platform: capability.PlatformOpenAI, Credentials: map[string]any{
				"model_mapping": map[string]any{"gpt-5": "gpt-5"},
			}},
		},
		anthropicGroupID: {
			{ID: 2, Platform: capability.PlatformAnthropic, Credentials: map[string]any{
				"model_mapping": map[string]any{"claude-sonnet-4-6": "claude-sonnet-4-6"},
			}},
		},
	}})

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	context.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		IsComposite: true,
		ModelMapping: map[string]string{
			"review":        "gpt-5",
			"missing-alias": "not-requestable",
			"wild-*":        "gpt-5",
		},
		User: &identity.User{Status: billing.StatusActive, AllowedGroups: []int64{anthropicGroupID}},
		CompositeGroups: []apikey.APIKeyCompositeGroup{
			{GroupID: openAIGroupID, Prefix: "GPT", SortOrder: 0, Group: &routing.Group{ID: openAIGroupID, Status: billing.StatusActive}},
			{GroupID: anthropicGroupID, Prefix: "Claude", SortOrder: 1, Group: &routing.Group{ID: anthropicGroupID, Status: billing.StatusActive}},
		},
	})

	h.Models(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &got))
	ids := modelIDsForTest(got.Data)
	require.NotEmpty(t, ids)
	require.Contains(t, ids, "GPT/review")
	require.NotContains(t, ids, "GPT/missing-alias")
	require.NotContains(t, ids, "GPT/wild-*")
	firstClaude := -1
	for index, id := range ids {
		if len(id) >= len("Claude/") && id[:len("Claude/")] == "Claude/" {
			firstClaude = index
			break
		}
		require.Contains(t, id, "GPT/")
	}
	require.Greater(t, firstClaude, 0)
	for _, id := range ids[firstClaude:] {
		require.Contains(t, id, "Claude/")
	}
	require.Contains(t, ids, "GPT/gpt-5")
	require.Contains(t, ids, "Claude/claude-sonnet-4-6")
}

// TestGatewayModelsCompositeKeyFiltersPreferredSubscriptionMappings supplies a
// preferred subscription covering only one exclusive group. It checks local
// models include that group's prefixed candidate and exclude the other mapped
// group.
func TestGatewayModelsCompositeKeyFiltersPreferredSubscriptionMappings(t *testing.T) {
	allowedGroupID := int64(52)
	blockedGroupID := int64(53)
	h := newGatewayModelsHandlerForTest(&gatewayModelsProviderRepoStub{byGroup: map[int64][]provider.Record{
		allowedGroupID: {{ID: 1, Platform: capability.PlatformOpenAI, Credentials: map[string]any{
			"model_mapping": map[string]any{"allowed-model": "allowed-model"},
		}}},
		blockedGroupID: {{ID: 2, Platform: capability.PlatformOpenAI, Credentials: map[string]any{
			"model_mapping": map[string]any{"blocked-model": "blocked-model"},
		}}},
	}})

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	context.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		IsComposite: true,
		BillingMode: apikey.APIKeyBillingModeSubscription,
		User: &identity.User{
			Status:        billing.StatusActive,
			AllowedGroups: []int64{allowedGroupID, blockedGroupID},
		},
		CompositeGroups: []apikey.APIKeyCompositeGroup{
			{GroupID: allowedGroupID, Prefix: "Allowed", Group: &routing.Group{ID: allowedGroupID, Status: billing.StatusActive, IsExclusive: true}},
			{GroupID: blockedGroupID, Prefix: "Blocked", Group: &routing.Group{ID: blockedGroupID, Status: billing.StatusActive, IsExclusive: true}},
		},
	})
	context.Set(string(gatewayhttp.ContextKeyAPIKeyBilling), &billing.APIKeyBillingContext{
		Mode:      apikey.APIKeyBillingModeSubscription,
		Source:    "subscription",
		Available: true,
		Subscription: &billing.UserSubscription{Plan: &billing.SubscriptionPlan{
			GroupIDs: []int64{allowedGroupID},
		}},
	})

	h.Models(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &got))
	ids := modelIDsForTest(got.Data)
	require.Contains(t, ids, "Allowed/allowed-model")
	require.NotContains(t, ids, "Blocked/blocked-model")
}

// TestGatewayModelsCompositeKeyFiltersRevokedMappings disables a public group
// and removes access to an exclusive group. It checks the successful
// composite-key model response contains no candidate rows from either mapping.
func TestGatewayModelsCompositeKeyFiltersRevokedMappings(t *testing.T) {
	publicGroupID := int64(60)
	exclusiveGroupID := int64(61)
	h := newGatewayModelsHandlerForTest(&gatewayModelsProviderRepoStub{byGroup: map[int64][]provider.Record{
		publicGroupID: {{ID: 1, Platform: capability.PlatformOpenAI, Credentials: map[string]any{
			"model_mapping": map[string]any{"public-model": "public-model"},
		}}},
		exclusiveGroupID: {{ID: 2, Platform: capability.PlatformAnthropic, Credentials: map[string]any{
			"model_mapping": map[string]any{"exclusive-model": "exclusive-model"},
		}}},
	}})

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	context.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		IsComposite: true,
		User: &identity.User{
			Status:               billing.StatusActive,
			DisabledPublicGroups: []int64{publicGroupID},
			AllowedGroups:        nil,
		},
		CompositeGroups: []apikey.APIKeyCompositeGroup{
			{GroupID: publicGroupID, Prefix: "Public", Group: &routing.Group{ID: publicGroupID, Status: billing.StatusActive}},
			{GroupID: exclusiveGroupID, Prefix: "Private", Group: &routing.Group{ID: exclusiveGroupID, Status: billing.StatusActive, IsExclusive: true}},
		},
	})

	h.Models(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &got))
	require.Empty(t, got.Data)
}

// TestGatewayModels_AntigravityConfiguredModelMetadata lists a configured
// Antigravity Fable candidate. It checks the shared display name is applied
// while the absent catalogue creation text stays empty.
func TestGatewayModels_AntigravityConfiguredModelMetadata(t *testing.T) {
	groupID := int64(32)
	h := newGatewayModelsHandlerForTest(
		&gatewayModelsProviderRepoStub{
			byGroup: map[int64][]provider.Record{
				groupID: {
					{
						ID:       1,
						Platform: capability.PlatformAntigravity,
						Credentials: map[string]any{
							"model_whitelist": []string{"claude-fable-5"},
						},
					},
				},
			},
		},
	)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		Group: &routing.Group{ID: groupID},
	})

	h.Models(c)

	require.Equal(t, http.StatusOK, rec.Code)

	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.NotEmpty(t, got.Data)
	for _, model := range got.Data {
		if model.ID == "claude-fable-5" {
			require.Equal(t, "Claude Fable 5", model.DisplayName)
			require.Empty(t, model.CreatedAt)
			return
		}
	}
	t.Fatal("catalog model missing")
}

// TestGatewayModels_QoderUsesExecutionModels lists a default Qoder fixture
// through local model HTTP. It checks the list envelope includes the tested
// DeepSeek and Claude names from Qoder execution model choices.
func TestGatewayModels_QoderUsesExecutionModels(t *testing.T) {
	groupID := int64(28)
	h := newGatewayModelsHandlerForTest(
		&gatewayModelsProviderRepoStub{
			byGroup: map[int64][]provider.Record{
				groupID: {
					{ID: 1, Platform: capability.PlatformQoder},
				},
			},
		},
	)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		Group: &routing.Group{ID: groupID},
	})

	h.Models(c)

	require.Equal(t, http.StatusOK, rec.Code)

	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, "list", got.Object)
	require.Contains(t, modelIDsForTest(got.Data), "deepseek-v4-pro")
	require.Contains(t, modelIDsForTest(got.Data), "claude-opus-4-6")
}

// TestGatewayModels_Grok45AdvertisesReasoningEffortForGrokBuild lists one
// mapped Grok 4.5 candidate. It checks the shared reasoning helper observes
// low/medium/high/xhigh choices with high marked as the default and compatible
// display fields.
func TestGatewayModels_Grok45AdvertisesReasoningEffortForGrokBuild(t *testing.T) {
	assertGrokGatewayReasoningEfforts(t, 4409, "grok-4.5", []gatewayReasoningEffortOptionForTest{
		{Value: "low", Label: "Low"},
		{Value: "medium", Label: "Medium"},
		{Value: "high", Label: "High", Default: true},
		{Value: "xhigh", Label: "xHigh"},
	})
}

// TestGatewayModels_Grok46AdvertisesXHighReasoningEffortForGrokBuild lists the
// mapped Grok 4.6 candidate. It checks low, medium, high, and xhigh options are
// returned while high retains the default marker.
func TestGatewayModels_Grok46AdvertisesXHighReasoningEffortForGrokBuild(t *testing.T) {
	xhighEfforts := []gatewayReasoningEffortOptionForTest{
		{Value: "low", Label: "Low"},
		{Value: "medium", Label: "Medium"},
		{Value: "high", Label: "High", Default: true},
		{Value: "xhigh", Label: "xHigh"},
	}
	tests := []struct {
		groupID int64  // reasoning fixture group
		model   string // mapped Grok model
	}{
		{groupID: 4410, model: "grok-4.6"},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			assertGrokGatewayReasoningEfforts(t, tt.groupID, tt.model, xhighEfforts)
		})
	}
}

// assertGrokGatewayReasoningEfforts serves one mapped Grok model and checks its
// complete decoded display shape. It asserts ID/object/owner/type, nonempty
// name, absent creation text, enabled reasoning support, high default, and the
// supplied option sequence.
func assertGrokGatewayReasoningEfforts(t *testing.T, groupID int64, modelID string, want []gatewayReasoningEffortOptionForTest) {
	t.Helper()

	h := newGatewayModelsHandlerForTest(
		&gatewayModelsProviderRepoStub{
			byGroup: map[int64][]provider.Record{
				groupID: {
					{
						ID:       1,
						Platform: capability.PlatformGrok,
						Credentials: map[string]any{
							"model_mapping": map[string]any{modelID: modelID},
						},
					},
				},
			},
		},
	)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		Group: &routing.Group{ID: groupID},
	})

	h.Models(c)

	require.Equal(t, http.StatusOK, rec.Code)
	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got.Data, 1)
	model := got.Data[0]
	require.Equal(t, modelID, model.ID)
	require.Equal(t, "model", model.Object)
	require.Equal(t, "xai", model.OwnedBy)
	require.Equal(t, "model", model.Type)
	require.NotEmpty(t, model.DisplayName)
	require.Empty(t, model.CreatedAt)
	require.True(t, model.SupportsReasoningEffort)
	require.Equal(t, "high", model.ReasoningEffort)
	require.Equal(t, want, model.ReasoningEfforts)
}

// TestGatewayModels_GrokDefaultsExcludeBuiltinAliases lists an unconfigured
// Grok provider and then an explicitly mapped alias. It checks the first
// response is empty and the second includes the configured grok alias.
func TestGatewayModels_GrokDefaultsExcludeBuiltinAliases(t *testing.T) {
	groupID := int64(4410)
	h := newGatewayModelsHandlerForTest(
		&gatewayModelsProviderRepoStub{
			byGroup: map[int64][]provider.Record{
				groupID: {
					{ID: 1, Platform: capability.PlatformGrok, Credentials: map[string]any{}},
				},
			},
		},
	)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		Group: &routing.Group{ID: groupID},
	})

	h.Models(c)

	require.Equal(t, http.StatusOK, rec.Code)
	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Empty(t, got.Data)
	require.NotContains(t, modelIDsForTest(got.Data), "grok")
	require.NotContains(t, modelIDsForTest(got.Data), "grok-latest")

	mappedGroupID := int64(4411)
	mappedHandler := newGatewayModelsHandlerForTest(
		&gatewayModelsProviderRepoStub{
			byGroup: map[int64][]provider.Record{
				mappedGroupID: {
					{
						ID:       2,
						Platform: capability.PlatformGrok,
						Credentials: map[string]any{
							"model_mapping": map[string]any{"grok": "grok-4.3"},
						},
					},
				},
			},
		},
	)
	mappedRecorder := httptest.NewRecorder()
	mappedContext, _ := gin.CreateTestContext(mappedRecorder)
	mappedContext.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	mappedContext.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		Group: &routing.Group{ID: mappedGroupID},
	})

	mappedHandler.Models(mappedContext)

	require.Equal(t, http.StatusOK, mappedRecorder.Code)
	var mapped gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(mappedRecorder.Body.Bytes(), &mapped))
	require.Contains(t, modelIDsForTest(mapped.Data), "grok")
}

// TestGatewayModels_MixedGroupIncludesMappedModelsFromEveryPlatform supplies
// mapped Anthropic and Gemini candidates in one group. It checks the local
// model response contains exactly both configured names in the expected order.
func TestGatewayModels_MixedGroupIncludesMappedModelsFromEveryPlatform(t *testing.T) {
	groupID := int64(21)
	h := newGatewayModelsHandlerForTest(
		&gatewayModelsProviderRepoStub{
			byGroup: map[int64][]provider.Record{
				groupID: {
					{
						ID:       1,
						Platform: capability.PlatformAnthropic,
						Credentials: map[string]any{
							"model_mapping": map[string]any{
								"claude-sonnet-4-6": "claude-sonnet-4-6",
							},
						},
					},
					{
						ID:       2,
						Platform: capability.PlatformGemini,
						Credentials: map[string]any{
							"model_mapping": map[string]any{
								"gemini-2.5-flash": "gemini-2.5-flash",
							},
						},
					},
				},
			},
		},
	)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		Group: &routing.Group{ID: groupID},
	})

	h.Models(c)

	require.Equal(t, http.StatusOK, rec.Code)

	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, []string{"claude-sonnet-4-6", "gemini-2.5-flash"}, modelIDsForTest(got.Data))
}

// TestGatewayModels_CustomModelsListDisabledKeepsOriginalModels disables a
// narrower custom list over two configured OpenAI models. It checks both
// configured names remain sorted with the expected model owner/object and
// absent creation text.
func TestGatewayModels_CustomModelsListDisabledKeepsOriginalModels(t *testing.T) {
	groupID := int64(22)
	h := newGatewayModelsHandlerForTest(
		&gatewayModelsProviderRepoStub{
			byGroup: map[int64][]provider.Record{
				groupID: {
					{
						ID:       1,
						Platform: capability.PlatformOpenAI,
						Credentials: map[string]any{
							"model_mapping": map[string]any{
								"gpt-5.5": "gpt-5.5",
								"gpt-5.4": "gpt-5.4",
							},
						},
					},
				},
			},
		},
	)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		Group: &routing.Group{
			ID: groupID,
			ModelsListConfig: routing.GroupModelsListConfig{
				Enabled: false,
				Models:  []string{"gpt-5.5"},
			},
		},
	})

	h.Models(c)

	require.Equal(t, http.StatusOK, rec.Code)

	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, []string{"gpt-5.4", "gpt-5.5"}, modelIDsForTest(got.Data))
	require.Equal(t, "model", got.Data[0].Object)
	require.Equal(t, "openai", got.Data[0].OwnedBy)
	require.Empty(t, got.Data[0].CreatedAt)
}

// TestGatewayModels_CustomModelsListFiltersAndOrdersMappedModels supplies
// mapped names and an ordered custom selection including a missing name. It
// checks only available selected names remain in the custom order.
func TestGatewayModels_CustomModelsListFiltersAndOrdersMappedModels(t *testing.T) {
	groupID := int64(23)
	h := newGatewayModelsHandlerForTest(
		&gatewayModelsProviderRepoStub{
			byGroup: map[int64][]provider.Record{
				groupID: {
					{
						ID:       1,
						Platform: capability.PlatformOpenAI,
						Credentials: map[string]any{
							"model_mapping": map[string]any{
								"gpt-5.4":         "gpt-5.4",
								"gpt-5.5":         "gpt-5.5",
								"legacy-gpt-2024": "legacy-gpt-2024",
							},
						},
					},
				},
			},
		},
	)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		Group: &routing.Group{
			ID: groupID,
			ModelsListConfig: routing.GroupModelsListConfig{
				Enabled: true,
				Models:  []string{"gpt-5.5", "missing-model", "gpt-5.4"},
			},
		},
	})

	h.Models(c)

	require.Equal(t, http.StatusOK, rec.Code)

	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, []string{"gpt-5.5", "gpt-5.4"}, modelIDsForTest(got.Data))
}

// TestGatewayModels_CustomModelsListKeepsConcreteModelAllowedByWildcardMapping
// supplies an Anthropic wildcard mapping and a concrete custom choice. It
// checks the resolved concrete model is retained as the sole returned
// candidate.
func TestGatewayModels_CustomModelsListKeepsConcreteModelAllowedByWildcardMapping(t *testing.T) {
	groupID := int64(26)
	h := newGatewayModelsHandlerForTest(
		&gatewayModelsProviderRepoStub{
			byGroup: map[int64][]provider.Record{
				groupID: {
					{
						ID:       1,
						Platform: capability.PlatformAnthropic,
						Credentials: map[string]any{
							"model_mapping": map[string]any{
								"claude-*": "claude-sonnet-4-6",
							},
						},
					},
				},
			},
		},
	)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		Group: &routing.Group{
			ID: groupID,
			ModelsListConfig: routing.GroupModelsListConfig{
				Enabled: true,
				Models:  []string{"claude-sonnet-4-6"},
			},
		},
	})

	h.Models(c)

	require.Equal(t, http.StatusOK, rec.Code)

	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, []string{"claude-sonnet-4-6"}, modelIDsForTest(got.Data))
}

// TestGatewayModels_AnthropicCustomModelsListIncludesOAuthClaudeAndMappedDeepSeek
// combines OAuth Claude whitelist and API-Key DeepSeek mapping. It checks the
// custom selection retains all three configured names in the requested display
// order.
func TestGatewayModels_AnthropicCustomModelsListIncludesOAuthClaudeAndMappedDeepSeek(t *testing.T) {
	groupID := int64(28)
	h := newGatewayModelsHandlerForTest(
		&gatewayModelsProviderRepoStub{
			byGroup: map[int64][]provider.Record{
				groupID: {
					{
						ID:          1,
						Platform:    capability.PlatformAnthropic,
						Type:        capability.ProviderTypeOAuth,
						Credentials: map[string]any{"model_whitelist": []string{"claude-fable-5", "claude-opus-4-8"}},
					},
					{
						ID:       2,
						Platform: capability.PlatformAnthropic,
						Type:     capability.ProviderTypeAPIKey,
						Credentials: map[string]any{
							"model_mapping": map[string]any{
								"deepseek-v4-pro": "deepseek-v4-pro",
							},
						},
					},
				},
			},
		},
	)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		Group: &routing.Group{
			ID: groupID,
			ModelsListConfig: routing.GroupModelsListConfig{
				Enabled: true,
				Models:  []string{"claude-fable-5", "claude-opus-4-8", "deepseek-v4-pro"},
			},
		},
	})

	h.Models(c)

	require.Equal(t, http.StatusOK, rec.Code)

	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, []string{"claude-fable-5", "claude-opus-4-8", "deepseek-v4-pro"}, modelIDsForTest(got.Data))
}

// TestGatewayModels_DisabledCustomListUsesConfiguredModels disables a custom
// list over unconfigured OAuth and mapped API-Key providers. It checks mapped
// DeepSeek remains and an unrelated default Claude model is absent.
func TestGatewayModels_DisabledCustomListUsesConfiguredModels(t *testing.T) {
	groupID := int64(29)
	h := newGatewayModelsHandlerForTest(
		&gatewayModelsProviderRepoStub{
			byGroup: map[int64][]provider.Record{
				groupID: {
					{
						ID:       1,
						Platform: capability.PlatformAnthropic,
						Type:     capability.ProviderTypeOAuth,
					},
					{
						ID:       2,
						Platform: capability.PlatformAnthropic,
						Type:     capability.ProviderTypeAPIKey,
						Credentials: map[string]any{
							"model_mapping": map[string]any{
								"deepseek-v4-pro": "deepseek-v4-pro",
							},
						},
					},
				},
			},
		},
	)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		Group: &routing.Group{
			ID: groupID,
			ModelsListConfig: routing.GroupModelsListConfig{
				Enabled: false,
				Models:  []string{"claude-fable-5", "deepseek-v4-pro"},
			},
		},
	})

	h.Models(c)

	require.Equal(t, http.StatusOK, rec.Code)

	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	modelIDs := modelIDsForTest(got.Data)
	require.Contains(t, modelIDs, "deepseek-v4-pro")
	require.NotContains(t, modelIDs, "claude-opus-4-6")
}

// TestGatewayModels_AnthropicCustomModelsListDoesNotAddModelsOutsideResolvedCandidates
// supplies custom Claude choices with an unconfigured OAuth provider. It checks
// successful HTTP returns no models outside the resolved configured candidate
// set.
func TestGatewayModels_AnthropicCustomModelsListDoesNotAddModelsOutsideResolvedCandidates(t *testing.T) {
	groupID := int64(30)
	h := newGatewayModelsHandlerForTest(
		&gatewayModelsProviderRepoStub{
			byGroup: map[int64][]provider.Record{
				groupID: {
					{
						ID:       1,
						Platform: capability.PlatformAnthropic,
						Type:     capability.ProviderTypeOAuth,
					},
				},
			},
		},
	)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		Group: &routing.Group{
			ID: groupID,
			ModelsListConfig: routing.GroupModelsListConfig{
				Enabled: true,
				Models:  []string{"claude-opus-4-6-thinking", "claude-sonnet-4-5"},
			},
		},
	})

	h.Models(c)

	require.Equal(t, http.StatusOK, rec.Code)

	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Empty(t, modelIDsForTest(got.Data))
}

// TestGatewayModels_CustomModelsListCanReturnEmptyWhenSelectionsUnavailable
// selects a custom name absent from the provider mapping. It checks the local
// list remains successful HTTP and contains no model identities.
func TestGatewayModels_CustomModelsListCanReturnEmptyWhenSelectionsUnavailable(t *testing.T) {
	groupID := int64(24)
	h := newGatewayModelsHandlerForTest(
		&gatewayModelsProviderRepoStub{
			byGroup: map[int64][]provider.Record{
				groupID: {
					{
						ID:       1,
						Platform: capability.PlatformOpenAI,
						Credentials: map[string]any{
							"model_mapping": map[string]any{
								"gpt-5.4": "gpt-5.4",
							},
						},
					},
				},
			},
		},
	)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		Group: &routing.Group{
			ID: groupID,
			ModelsListConfig: routing.GroupModelsListConfig{
				Enabled: true,
				Models:  []string{"gpt-5.5"},
			},
		},
	})

	h.Models(c)

	require.Equal(t, http.StatusOK, rec.Code)

	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Empty(t, modelIDsForTest(got.Data))
}

// TestGatewayModels_CustomModelsListFiltersConfiguredModels selects available
// whitelist names around an unavailable custom name. It checks unavailable
// selection is omitted and the two configured names retain custom order.
func TestGatewayModels_CustomModelsListFiltersConfiguredModels(t *testing.T) {
	groupID := int64(25)
	h := newGatewayModelsHandlerForTest(
		&gatewayModelsProviderRepoStub{
			byGroup: map[int64][]provider.Record{
				groupID: {
					{ID: 1, Platform: capability.PlatformOpenAI, Credentials: map[string]any{"model_whitelist": []string{"gpt-5.5", "gpt-5.4"}}},
				},
			},
		},
	)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		Group: &routing.Group{
			ID: groupID,
			ModelsListConfig: routing.GroupModelsListConfig{
				Enabled: true,
				Models:  []string{"gpt-5.5", "legacy-gpt-2024", "gpt-5.4"},
			},
		},
	})

	h.Models(c)

	require.Equal(t, http.StatusOK, rec.Code)

	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, []string{"gpt-5.5", "gpt-5.4"}, modelIDsForTest(got.Data))
}

// TestGatewayModels_OpenAICustomModelsListKeepsOpenAIResponseShapeForConfiguredModels
// lists two whitelisted custom OpenAI choices. It checks their order, model
// object, zero created value, OpenAI owner, and absent creation text.
func TestGatewayModels_OpenAICustomModelsListKeepsOpenAIResponseShapeForConfiguredModels(t *testing.T) {
	groupID := int64(27)
	h := newGatewayModelsHandlerForTest(
		&gatewayModelsProviderRepoStub{
			byGroup: map[int64][]provider.Record{
				groupID: {
					{ID: 1, Platform: capability.PlatformOpenAI, Credentials: map[string]any{"model_whitelist": []string{"gpt-5.5", "gpt-5.4"}}},
				},
			},
		},
	)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		Group: &routing.Group{
			ID: groupID,
			ModelsListConfig: routing.GroupModelsListConfig{
				Enabled: true,
				Models:  []string{"gpt-5.5", "gpt-5.4"},
			},
		},
	})

	h.Models(c)

	require.Equal(t, http.StatusOK, rec.Code)

	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, []string{"gpt-5.5", "gpt-5.4"}, modelIDsForTest(got.Data))
	require.Equal(t, "model", got.Data[0].Object)
	require.Zero(t, got.Data[0].Created)
	require.Equal(t, "openai", got.Data[0].OwnedBy)
	require.Empty(t, got.Data[0].CreatedAt)
}

// TestGatewayModels_OpenAIEmptyListKeepsOpenAIResponseShape supplies an
// unconfigured OpenAI provider. It checks successful HTTP retains the list
// object with no model rows, preserving the empty response envelope.
func TestGatewayModels_OpenAIEmptyListKeepsOpenAIResponseShape(t *testing.T) {
	groupID := int64(31)
	h := newGatewayModelsHandlerForTest(
		&gatewayModelsProviderRepoStub{
			byGroup: map[int64][]provider.Record{
				groupID: {{ID: 1, Platform: capability.PlatformOpenAI}},
			},
		},
	)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		Group: &routing.Group{ID: groupID},
	})

	h.Models(c)

	require.Equal(t, http.StatusOK, rec.Code)
	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Empty(t, got.Data)
	require.Equal(t, "list", got.Object)
}

// TestGatewayModels_QoderCustomModelsListFiltersConfiguredModels supplies three
// whitelisted Qoder names and the same custom order. It checks the local
// response returns those configured choices in that exact order.
func TestGatewayModels_QoderCustomModelsListFiltersConfiguredModels(t *testing.T) {
	groupID := int64(29)
	h := newGatewayModelsHandlerForTest(
		&gatewayModelsProviderRepoStub{
			byGroup: map[int64][]provider.Record{
				groupID: {
					{ID: 1, Platform: capability.PlatformQoder, Credentials: map[string]any{"model_whitelist": []string{"deepseek-v4-pro", "claude-sonnet-4-6", "lite"}}},
				},
			},
		},
	)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		Group: &routing.Group{
			ID: groupID,
			ModelsListConfig: routing.GroupModelsListConfig{
				Enabled: true,
				Models:  []string{"deepseek-v4-pro", "claude-sonnet-4-6", "lite"},
			},
		},
	})

	h.Models(c)

	require.Equal(t, http.StatusOK, rec.Code)

	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, []string{"deepseek-v4-pro", "claude-sonnet-4-6", "lite"}, modelIDsForTest(got.Data))
}

// TestGatewayModels_GroupRestrictionEmptyDoesNotFallBackToDefaults combines an
// empty enabled price-model restriction with an unconfigured OpenAI provider.
// It checks the decoded response remains empty rather than adding catalogue
// defaults.
func TestGatewayModels_GroupRestrictionEmptyDoesNotFallBackToDefaults(t *testing.T) {
	groupID := int64(31)
	providerRepo := &gatewayModelsProviderRepoStub{byGroup: map[int64][]provider.Record{
		groupID: {{ID: 1, Platform: capability.PlatformOpenAI}},
	}}
	pricingConfigService := newGatewayModelsPricingConfigServiceForTest(groupID, capability.PlatformOpenAI, testkit.Configuration{
		ID:                 81,
		Status:             billing.StatusActive,
		RestrictModels:     true,
		BillingModelSource: routing.BillingModelSourceRequested,
	})
	h := newGatewayModelsHandlerWithPricingConfigForTest(providerRepo, pricingConfigService)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		Group: &routing.Group{ID: groupID},
	})

	h.Models(c)

	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Empty(t, got.Data)
}

// TestGatewayModels_CustomListIntersectsPricingConfigFilteredModels supplies
// mapped allowed/blocked names with pricing restriction for one name. It checks
// an ordered custom list is intersected to the single allowed configured model.
func TestGatewayModels_CustomListIntersectsPricingConfigFilteredModels(t *testing.T) {
	groupID := int64(32)
	price := 0.01
	providerRepo := &gatewayModelsProviderRepoStub{byGroup: map[int64][]provider.Record{
		groupID: {{
			ID:       1,
			Platform: capability.PlatformOpenAI,
			Credentials: map[string]any{
				"model_mapping": map[string]any{
					"allowed-model": "allowed-model",
					"blocked-model": "blocked-model",
				},
			},
		}},
	}}
	pricingConfigService := newGatewayModelsPricingConfigServiceForTest(groupID, capability.PlatformOpenAI, testkit.Configuration{
		ID:                 82,
		Status:             billing.StatusActive,
		RestrictModels:     true,
		BillingModelSource: routing.BillingModelSourceRequested,
		ModelPricing: []routing.ModelPricingEntry{{
			Models:     []string{"allowed-model"},
			InputPrice: &price,
		}},
	})
	h := newGatewayModelsHandlerWithPricingConfigForTest(providerRepo, pricingConfigService)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
		Group: &routing.Group{
			ID: groupID,
			ModelsListConfig: routing.GroupModelsListConfig{
				Enabled: true,
				Models:  []string{"blocked-model", "allowed-model"},
			},
		},
	})

	h.Models(c)

	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, []string{"allowed-model"}, modelIDsForTest(got.Data))
}

// modelIDsForTest returns the decoded model identities in their existing
// response order.
func modelIDsForTest(models []gatewayModelItemForTest) []string {
	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	return ids
}

// gatewayModelCatalogFixture constructs metadata entries for the named model
// scenarios. It assigns catalog provider text by model prefix and replaces the
// Fable entry with its display name, leaving execution availability to
// configured provider candidates.
func gatewayModelCatalogFixture() *catalogtest.Catalog {
	c := catalogtest.New("claude-opus-4-6", "claude-opus-4-8", "gpt-5", "gpt-5.5", "claude-fable-5", "claude-sonnet-4-6", "claude-sonnet-4-5-20250929", "gpt-5.4", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gemini-2.5-flash", "gemini-3.1-flash-image", "grok-4.5", "grok-4.6")
	for id, entry := range c.Entries {
		switch {
		case strings.HasPrefix(id, "gpt-"):
			entry.Provider = "openai"
		case strings.HasPrefix(id, "claude-"):
			entry.Provider = "anthropic"
		case strings.HasPrefix(id, "gemini-"):
			entry.Provider = "google"
		case strings.HasPrefix(id, "grok-"):
			entry.Provider = "xai"
		}
		c.Entries[id] = entry
	}
	name := "Claude Fable 5"
	c.Entries["claude-fable-5"] = modelcatalog.Entry{Model: "claude-fable-5", Provider: "anthropic", Attributes: modelcatalog.Attributes{DisplayName: &name}}
	return c
}

// TestResolveModelsListReadLimit supplies nil, positive, and nonpositive
// configuration limits. It checks default model-response bytes for
// absence/nonpositive input and the configured value when it is positive.
func TestResolveModelsListReadLimit(t *testing.T) {
	t.Run("nil config uses default", func(t *testing.T) {
		require.Equal(t, config.DefaultModelsListReadMaxBytes, resolveModelsListReadLimit(nil))
	})

	t.Run("configured limit wins", func(t *testing.T) {
		cfg := &config.Config{}
		// Sixteen MiB supplies a distinct configured response-read byte limit.
		cfg.Gateway.ModelsListResponseMaxBytes = 16 << 20
		require.Equal(t, int64(16<<20), resolveModelsListReadLimit(cfg))
	})

	t.Run("non-positive limit uses default", func(t *testing.T) {
		cfg := &config.Config{}
		require.Equal(t, config.DefaultModelsListReadMaxBytes, resolveModelsListReadLimit(cfg))
	})
}

// ListSchedulableByGroupID returns an outer slice copy of the configured local
// providers. Group identity is ignored, nested provider references remain
// shared, and nil/empty input follows append-to-nil result behavior.
func (r *codexModelsRemovalProviderRepo) ListSchedulableByGroupID(context.Context, int64) ([]provider.Record, error) {
	return append([]provider.Record(nil), r.providers...), nil
}

// TestGatewayRoutesModelsWithClientVersionUsesLocalList requests versioned and
// bare model routes with client_version for an API-Key fixture. It checks
// successful local list JSON contains the configured model without an
// OAuth-provider availability error.
func TestGatewayRoutesModelsWithClientVersionUsesLocalList(t *testing.T) {
	repo := &codexModelsRemovalProviderRepo{
		providers: []provider.Record{
			{
				ID:       1,
				Platform: capability.PlatformOpenAI,
				Type:     capability.ProviderTypeAPIKey,
				Credentials: map[string]any{
					"api_key":         "sk-test",
					"model_whitelist": []string{"local-api-key-model"},
					"model_mapping": map[string]any{
						"local-api-key-model": "local-api-key-model",
					},
				},
			},
		},
	}
	router := newGatewayRoutesTestRouterWithGroup(&config.Config{}, &routing.Group{
		ID: 1, AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolAnthropicMessages, protocol.ProtocolOpenAIResponses, protocol.ProtocolOpenAIChatCompletions},
	}, newGatewayModelsHandlerForTest(repo))
	paths := []string{
		"/v1/models?client_version=0.144.0",
		"/models?client_version=0.144.0",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			recorder := httptest.NewRecorder()

			router.ServeHTTP(recorder, req)

			require.Equal(t, http.StatusOK, recorder.Code)
			require.NotContains(t, recorder.Body.String(), "No available OpenAI OAuth providers")
			var response struct {
				// protocol object category
				Object string `json:"object"`
				Data   []struct {
					// public model identity
					ID string `json:"id"`
				} `json:"data"` // decoded model entries
			}
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
			require.Equal(t, "list", response.Object)
			require.Len(t, response.Data, 1)
			require.Equal(t, "local-api-key-model", response.Data[0].ID)
		})
	}
}

// TestGatewayRoutesCodexModelsManifestPathIsRemoved requests the retired Codex
// model manifest and a valid sideband call path. It checks manifest NotFound,
// sideband dispatch, retained Responses POST registration, and shared
// model-route handler identities.
func TestGatewayRoutesCodexModelsManifestPathIsRemoved(t *testing.T) {
	router := newGatewayRoutesTestRouter(capability.PlatformOpenAI)

	req := httptest.NewRequest(http.MethodGet, "/backend-api/codex/models?client_version=0.144.0", nil)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	require.Equal(t, http.StatusNotFound, recorder.Code)

	// This valid call-shaped path must avoid the removed-manifest NotFound
	// result, distinguishing sideband dispatch from the retired fixed models endpoint.
	req = httptest.NewRequest(http.MethodGet, "/backend-api/codex/call_test", nil)
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, req)
	require.NotEqual(t, http.StatusNotFound, recorder.Code)

	registered := make(map[string]string)
	for _, route := range router.Routes() {
		registered[route.Method+" "+route.Path] = route.Handler
	}
	require.NotEmpty(t, registered[http.MethodPost+" /backend-api/codex/responses"])
	require.Empty(t, registered[http.MethodGet+" /backend-api/codex/models"])
	require.Equal(t, registered[http.MethodGet+" /v1/models"], registered[http.MethodGet+" /models"])
}
