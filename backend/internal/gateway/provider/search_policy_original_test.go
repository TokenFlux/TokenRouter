//go:build unit

package provider_test

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/routing/testkit"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/searchtools"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/search"
	searchprovider "github.com/TokenFlux/TokenRouter/internal/search/provider"
	"github.com/stretchr/testify/require"
)

// --- isOnlyWebSearchToolInBody ---

// --- extractSearchQueryFromBody ---

// --- buildSearchResultBlocks ---

// --- buildTextSummary ---

// --- shouldEmulateWebSearch ---

// webSearchToolBody is a valid request body with exactly one web_search tool.
var webSearchToolBody = []byte(`{"tools":[{"type":"web_search"}],"messages":[{"role":"user","content":"test"}]}`)

// nonWebSearchToolBody is a request body without web_search tool.
var nonWebSearchToolBody = []byte(`{"tools":[{"type":"text_editor"}],"messages":[{"role":"user","content":"test"}]}`)

// newSearchAccountPolicy creates a test Account with the given web search emulation mode.
func newSearchAccountPolicy(mode string) *searchtools.AccountPolicy {
	return &searchtools.AccountPolicy{
		ID:       1,
		Platform: capability.PlatformAnthropic,
		Type:     capability.AccountTypeAPIKey,
		Extra:    map[string]any{searchtools.FeatureKey: mode},
	}
}

func TestShouldEmulateWebSearch_NilManager(t *testing.T) {
	registry := search.NewRegistry()
	registry.Set(nil)
	defer registry.Set(nil)

	settingSvc := newSearchSettingsFixture(true, registry)

	svc := gatewayprovider.NewSearchTools(settingSvc, nil)
	account := newSearchAccountPolicy(searchtools.ModeEnabled)
	require.False(t, svc.ShouldEmulate(context.Background(), searchtools.PolicyInput{Body: webSearchToolBody, Mode: gatewayprovider.SearchAccountMode(account), Platform: account.Platform, GroupID: nil}))
}

func TestShouldEmulateWebSearch_NotOnlyWebSearchTool(t *testing.T) {
	mgr := search.NewManager([]search.ProviderConfig{{Type: "brave", APIKey: "k"}}, nil, searchprovider.NewExecutor(), nil)
	registry := search.NewRegistry()
	registry.Set(mgr)
	defer registry.Set(nil)

	settingSvc := newSearchSettingsFixture(true, registry)

	svc := gatewayprovider.NewSearchTools(settingSvc, nil)
	account := newSearchAccountPolicy(searchtools.ModeEnabled)
	require.False(t, svc.ShouldEmulate(context.Background(), searchtools.PolicyInput{Body: nonWebSearchToolBody, Mode: gatewayprovider.SearchAccountMode(account), Platform: account.Platform, GroupID: nil}))
}

func TestShouldEmulateWebSearch_GlobalDisabled(t *testing.T) {
	mgr := search.NewManager([]search.ProviderConfig{{Type: "brave", APIKey: "k"}}, nil, searchprovider.NewExecutor(), nil)
	registry := search.NewRegistry()
	registry.Set(mgr)
	defer registry.Set(nil)

	// Global config disabled

	settingSvc := newSearchSettingsFixture(false, registry)
	svc := gatewayprovider.NewSearchTools(settingSvc, nil)
	account := newSearchAccountPolicy(searchtools.ModeEnabled)
	require.False(t, svc.ShouldEmulate(context.Background(), searchtools.PolicyInput{Body: webSearchToolBody, Mode: gatewayprovider.SearchAccountMode(account), Platform: account.Platform, GroupID: nil}))
}

func TestShouldEmulateWebSearch_AccountDisabled(t *testing.T) {
	mgr := search.NewManager([]search.ProviderConfig{{Type: "brave", APIKey: "k"}}, nil, searchprovider.NewExecutor(), nil)
	registry := search.NewRegistry()
	registry.Set(mgr)
	defer registry.Set(nil)

	settingSvc := newSearchSettingsFixture(true, registry)
	svc := gatewayprovider.NewSearchTools(settingSvc, nil)
	account := newSearchAccountPolicy(searchtools.ModeDisabled)
	require.False(t, svc.ShouldEmulate(context.Background(), searchtools.PolicyInput{Body: webSearchToolBody, Mode: gatewayprovider.SearchAccountMode(account), Platform: account.Platform, GroupID: nil}))
}

func TestShouldEmulateWebSearch_AccountEnabled(t *testing.T) {
	mgr := search.NewManager([]search.ProviderConfig{{Type: "brave", APIKey: "k"}}, nil, searchprovider.NewExecutor(), nil)
	registry := search.NewRegistry()
	registry.Set(mgr)
	defer registry.Set(nil)

	settingSvc := newSearchSettingsFixture(true, registry)
	svc := gatewayprovider.NewSearchTools(settingSvc, nil)
	account := newSearchAccountPolicy(searchtools.ModeEnabled)
	require.True(t, svc.ShouldEmulate(context.Background(), searchtools.PolicyInput{Body: webSearchToolBody, Mode: gatewayprovider.SearchAccountMode(account), Platform: account.Platform, GroupID: nil}))
}

func TestShouldEmulateWebSearch_DefaultMode_GroupPolicyEnabled(t *testing.T) {
	mgr := search.NewManager([]search.ProviderConfig{{Type: "brave", APIKey: "k"}}, nil, searchprovider.NewExecutor(), nil)
	registry := search.NewRegistry()
	registry.Set(mgr)
	defer registry.Set(nil)

	settingSvc := newSearchSettingsFixture(true, registry)
	ch := &testkit.Configuration{
		ID:     10,
		Status: billing.StatusActive,
		FeaturesConfig: map[string]any{
			searchtools.FeatureKey: map[string]any{capability.PlatformAnthropic: true},
		},
	}
	pricingConfigSvc := newPricingConfigServiceWithCache(42, ch)
	svc := gatewayprovider.NewSearchTools(settingSvc, pricingConfigSvc)

	account := newSearchAccountPolicy(searchtools.ModeDefault)
	groupID := int64(42)
	require.True(t, svc.ShouldEmulate(context.Background(), searchtools.PolicyInput{Body: webSearchToolBody, Mode: gatewayprovider.SearchAccountMode(account), Platform: account.Platform, GroupID: &groupID}))
}

func TestShouldEmulateWebSearch_DefaultMode_GroupPolicyDisabled(t *testing.T) {
	mgr := search.NewManager([]search.ProviderConfig{{Type: "brave", APIKey: "k"}}, nil, searchprovider.NewExecutor(), nil)
	registry := search.NewRegistry()
	registry.Set(mgr)
	defer registry.Set(nil)

	settingSvc := newSearchSettingsFixture(true, registry)
	ch := &testkit.Configuration{
		ID:     10,
		Status: billing.StatusActive,
		FeaturesConfig: map[string]any{
			searchtools.FeatureKey: map[string]any{capability.PlatformAnthropic: false},
		},
	}
	pricingConfigSvc := newPricingConfigServiceWithCache(42, ch)
	svc := gatewayprovider.NewSearchTools(settingSvc, pricingConfigSvc)

	account := newSearchAccountPolicy(searchtools.ModeDefault)
	groupID := int64(42)
	require.False(t, svc.ShouldEmulate(context.Background(), searchtools.PolicyInput{Body: webSearchToolBody, Mode: gatewayprovider.SearchAccountMode(account), Platform: account.Platform, GroupID: &groupID}))
}

func TestShouldEmulateWebSearch_DefaultMode_NilGroupID(t *testing.T) {
	mgr := search.NewManager([]search.ProviderConfig{{Type: "brave", APIKey: "k"}}, nil, searchprovider.NewExecutor(), nil)
	registry := search.NewRegistry()
	registry.Set(mgr)
	defer registry.Set(nil)

	settingSvc := newSearchSettingsFixture(true, registry)
	svc := gatewayprovider.NewSearchTools(settingSvc, nil)
	account := newSearchAccountPolicy(searchtools.ModeDefault)
	// nil groupID + default mode → falls through to channel check → returns false
	require.False(t, svc.ShouldEmulate(context.Background(), searchtools.PolicyInput{Body: webSearchToolBody, Mode: gatewayprovider.SearchAccountMode(account), Platform: account.Platform, GroupID: nil}))
}

func TestShouldEmulateWebSearch_DefaultMode_NilPricingConfigService(t *testing.T) {
	mgr := search.NewManager([]search.ProviderConfig{{Type: "brave", APIKey: "k"}}, nil, searchprovider.NewExecutor(), nil)
	registry := search.NewRegistry()
	registry.Set(mgr)
	defer registry.Set(nil)

	settingSvc := newSearchSettingsFixture(true, registry)
	svc := gatewayprovider.NewSearchTools(settingSvc, nil)
	account := newSearchAccountPolicy(searchtools.ModeDefault)
	groupID := int64(42)
	// nil channelService + default mode → returns false
	require.False(t, svc.ShouldEmulate(context.Background(), searchtools.PolicyInput{Body: webSearchToolBody, Mode: gatewayprovider.SearchAccountMode(account), Platform: account.Platform, GroupID: &groupID}))
}
