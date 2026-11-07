package provider

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog/testkit"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// creative_catalog_test.go checks the execution-policy adapter used by
// creative model discovery. Provider fixtures retain model mappings, allowed
// routes, and GIF switches so wrapping a configured provider cannot silently
// remove a capability restriction.

// creativeCatalogRows supplies configured provider adapters to creative model
// catalogue tests.
type creativeCatalogRows struct {
	values []creative.CatalogProvider /* returned catalogue provider rows */
}

// ListSchedulableByGroupIDAndPlatform returns the fixture's configured
// catalogue providers for creative queries. The slice remains shared with
// values, so fixture callers keep its entries stable during catalogue reads.
func (s creativeCatalogRows) ListSchedulableByGroupIDAndPlatform(context.Context, int64, string) ([]creative.CatalogProvider, error) {
	return s.values, nil
}

// TestCreativeCatalogUsesExecutionModelPolicy lists an image alias mapped to a
// whitelisted upstream model with passthrough both disabled and enabled. Both
// catalogue queries retain the alias and report the same mapped image model.
func TestCreativeCatalogUsesExecutionModelPolicy(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		value := &provider.Record{
			Platform: creative.PlatformOpenAI, Type: "apikey", Status: "active", Schedulable: true,
			Credentials: map[string]any{"model_mapping": map[string]any{"gpt-image-1": "gpt-image-2"}},
			Extra:       map[string]any{"openai_passthrough": passthrough},
		}
		public := creative.Public{Attributes: &routing.ModelAttributeService{Catalog: routing.ModelAttributeCatalog{View: testkit.OfflineView}}, ProviderRepo: creativeCatalogRows{[]creative.CatalogProvider{CreativeCatalogProvider(value)}}}
		group := &creative.GroupView{ID: 12, Operations: creative.OperationsForGroup(false, nil), RoutingPolicy: routing.GroupRoutingPolicy{
			Enabled: true, RestrictModels: true, RestrictionModelSource: routing.BillingModelSourceUpstream,
			AllowedModels: []string{"gpt-image-2"},
		}}
		models, err := public.CreativeModelsForGroup(context.Background(), group)
		require.NoError(t, err)
		require.Equal(t, "gpt-image-2", models["gpt-image-1"])
	}
}

// TestCreativeCatalogGIFSwitch keeps provider animation policy through the
// execution-policy wrapper. A disabled API-key endpoint cannot regain GIF support
// merely because catalogue adaptation erased its concrete provider type.
func TestCreativeCatalogGIFSwitch(t *testing.T) {
	value := &provider.Record{Platform: "openai", Type: "apikey", Extra: map[string]any{"images_gif_enabled": false}}
	adapter := CreativeCatalogProvider(value)
	require.False(t, adapter.SupportsImageGIF("openai_images_generations"))
	value.Extra["images_gif_enabled"] = true
	require.True(t, adapter.SupportsImageGIF("openai_images_generations"))
}
