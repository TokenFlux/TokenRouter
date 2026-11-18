package provider

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
)

// catalog_filtering_scenario_test.go exercises catalog.go, remote.go,
// service.go, and catalog_view.go together through HTTP updates and disk reloads.
// Raw responses retain relay data for conditional caching, while published
// attributes and prices retain accepted records and explicitly configured
// supplements. Rejected updates leave both publications and cache bytes intact.

// TestCatalogFilteringAcrossHTTPAndLocalReload serves changed, unchanged, and
// invalid HTTP candidates around a local supplement edit. Filtering must apply
// to every publication, including disk startup, while manual zero prices survive.
func TestCatalogFilteringAcrossHTTPAndLocalReload(t *testing.T) {
	const first = `{
	"openai":{"id":"openai","models":{"original":{"id":"original","type":"chat","capabilities":{},"limit":{"context":128},"cost":{"text":{"input":{"price":1},"output":{"price":2}}}},"public-only":{"id":"public-only","type":"chat","capabilities":{},"canonical_id":"openai/public-only","limit":{"context":64}}}},
	"google-vertex":{"id":"google-vertex","models":{"original":{"id":"original","type":"chat","capabilities":{},"limit":{"context":32},"cost":{"text":{"input":{"price":9},"output":{"price":10}}}}}},
	"openrouter":{"id":"openrouter","models":{"original":{"id":"original","type":"chat","capabilities":{},"canonical_id":"openai/original","limit":{"context":2},"cost":{"text":{"input":{"price":7},"output":{"price":8}}}},"relay-only":{"id":"relay-only","type":"chat","capabilities":{},"cost":{"text":{"input":{"price":7},"output":{"price":8}}}},"manual":{"id":"manual","type":"chat","capabilities":{},"limit":{"context":2},"cost":{"text":{"input":{"price":7},"output":{"price":8}}}}}}
}`
	second := strings.Replace(first, `"input":{"price":1},"output":{"price":2}`, `"input":{"price":3},"output":{"price":4}`, 1)
	var requests atomic.Int32
	server := newLocalTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch requests.Add(1) {
		case 1:
			assertCatalogResponse(t, w, r, "", `"v1"`, first)
		case 2, 3:
			require.Equal(t, `"v1"`, r.Header.Get("If-None-Match"))
			w.WriteHeader(http.StatusNotModified)
		case 4:
			assertCatalogResponse(t, w, r, `"v1"`, `"v2"`, second)
		case 5:
			assertCatalogResponse(t, w, r, `"v2"`, `"invalid"`, `{"openai":{"id":"openai","models":{"bad":{"id":"bad","type":"chat","capabilities":{},"limit":{"context":-1}}}}}`)
		case 6:
			require.Equal(t, `"v2"`, r.Header.Get("If-None-Match"))
			w.WriteHeader(http.StatusNotModified)
		default:
			t.Error("unexpected catalog request")
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(server.Close)
	dir := t.TempDir()
	patch := filepath.Join(dir, "supplement.json")
	require.NoError(t, os.WriteFile(patch, []byte(`{}`), 0o600))
	service := NewService(Options{
		DataDir: dir, RemoteURL: server.URL, AllowInsecureHTTP: true,
		FallbackFile: patch, ModelLookupCandidates: modelidentity.CandidatesFactory,
	}, &remoteClient{httpClient: server.Client()})
	assertFiltered := func(reader *Service) {
		t.Helper()
		require.NotContains(t, reader.ModelIDs(), "relay-only")
		require.NotContains(t, reader.ModelIDs(), "openrouter/relay-only")
		require.NotContains(t, reader.ModelIDs(), "openrouter/original")
		require.Nil(t, reader.ModelAttributes("openrouter/original").Context)
		require.Equal(t, "unpriced", reader.GetModelPricing("openrouter/original").Source)
		require.Equal(t, 64, *reader.ModelAttributes("public-only").Context)
		require.Equal(t, 32, *reader.ModelAttributes("google-vertex/original").Context)
		// Allow binary rounding from million-token price conversion.
		require.InDelta(t, 9e-6, reader.GetModelPricing("google-vertex/original").InputCostPerToken, 1e-12)
		for row := range reader.PricingView().Models() {
			require.NotEqual(t, "openrouter/original", row.Model)
			require.NotEqual(t, "openrouter/relay-only", row.Model)
		}
	}
	require.NoError(t, service.ForceUpdate())
	assertFiltered(service)
	index, before := service.AttributeIndex()
	require.NotContains(t, index.Search(modelcatalog.AttributeQuery{}).Providers, "openrouter")
	require.Equal(t, []byte(first), readCatalogTestFile(t, service.catalogFilePath()))
	require.NoError(t, service.syncWithRemote())
	require.Equal(t, before.Version, service.ModelVersion())
	assertFiltered(service)

	require.NoError(t, os.WriteFile(patch, []byte(`{
		"openrouter/manual":{"provider":"openrouter","input_cost_per_token":0,"output_cost_per_token":0,
			"attributes":{"context":16,"temperature":false}}
	}`), 0o600))
	require.NoError(t, service.syncWithRemote())
	require.NotEqual(t, before.Version, service.ModelVersion())
	manual := service.GetModelPricing("openrouter/manual")
	require.True(t, manual.InputPricePresent)
	require.False(t, manual.TokenPricingAbsent)
	require.Zero(t, manual.InputCostPerToken)
	require.Equal(t, 16, *service.ModelAttributes("openrouter/manual").Context)
	require.False(t, *service.ModelAttributes("openrouter/manual").Temperature)
	assertFiltered(service)
	old := service.PricingView()
	require.NoError(t, service.syncWithRemote())
	// Allow binary rounding when comparing converted token prices.
	require.InDelta(t, 1e-6, old.GetModelPricing("original").InputCostPerToken, 1e-12)
	require.InDelta(t, 3e-6, service.GetModelPricing("original").InputCostPerToken, 1e-12)
	assertFiltered(service)
	before = service.AttributesSnapshot()
	require.ErrorContains(t, service.syncWithRemote(), "negative model limit")
	require.Equal(t, before.Version, service.ModelVersion())
	require.Equal(t, before.Items, service.AttributesSnapshot().Items)
	require.Equal(t, []byte(second), readCatalogTestFile(t, service.catalogFilePath()))
	require.NoError(t, service.syncWithRemote())
	require.Empty(t, service.AttributesSnapshot().LastError)
	require.EqualValues(t, 6, requests.Load())

	local := NewService(Options{DataDir: dir, FallbackFile: patch}, nil)
	require.NoError(t, local.Initialize())
	t.Cleanup(local.Stop)
	assertFiltered(local)
	require.Equal(t, 16, *local.ModelAttributes("openrouter/manual").Context)
	require.NoError(t, os.Remove(patch))
	require.NoError(t, local.ForceUpdate())
	assertFiltered(local)
	require.NotContains(t, local.ModelIDs(), "openrouter/manual")
	require.Equal(t, "unpriced", local.GetModelPricing("openrouter/manual").Source)
}

// assertCatalogResponse checks the incoming conditional validator and writes
// one catalog response with its replacement ETag. Callers provide valid header
// values and invoke it synchronously inside the fixture's HTTP handler.
func assertCatalogResponse(t *testing.T, w http.ResponseWriter, r *http.Request, validator, etag, body string) {
	t.Helper()
	require.Equal(t, validator, r.Header.Get("If-None-Match"))
	w.Header().Set("ETag", etag)
	_, err := w.Write([]byte(body))
	require.NoError(t, err)
}

// TestOfflineCatalogPrefersOrdinaryAPIPrice loads GLM's ordinary and subscription
// offerings from the embedded catalog. Bare pricing must select the ordinary
// API, while the qualified subscription identity retains its explicit zero rate.
func TestOfflineCatalogPrefersOrdinaryAPIPrice(t *testing.T) {
	service := newOfflinePricingFixture(t)
	t.Cleanup(service.Stop)
	for _, reader := range []*Service{service, service.ReadOnlySnapshot()} {
		price := reader.GetModelPricing("glm-5-turbo")
		require.NotNil(t, price)
		_, err := pricing.ResolveModelPricing("glm-5-turbo", price)
		require.NoError(t, err)
		require.Equal(t, "zhipu", price.Provider)
		// Allow binary rounding from million-token price conversion.
		require.InDelta(t, 1.2e-6, price.InputCostPerToken, 1e-12)
		require.InDelta(t, 4e-6, price.OutputCostPerToken, 1e-12)
		plan := reader.GetModelPricing("zai-coding-plan/glm-5-turbo")
		require.NotNil(t, plan)
		require.True(t, plan.InputPricePresent)
		require.False(t, plan.TokenPricingAbsent)
		require.Zero(t, plan.InputCostPerToken)
		require.Zero(t, plan.OutputCostPerToken)
	}
}

// TestOfflineCatalogKeepsQualifiedPublicIdentity resolves a public xAI model
// and Vertex's offering of that model. Public attributes and absent pricing
// must remain separate from Vertex's smaller context and service-specific quote.
func TestOfflineCatalogKeepsQualifiedPublicIdentity(t *testing.T) {
	service := newOfflinePricingFixture(t)
	t.Cleanup(service.Stop)
	for _, reader := range []*Service{service, service.ReadOnlySnapshot()} {
		model := "xai/grok-4.1-fast-reasoning"
		require.Equal(t, "xai", reader.ModelEntry(model).Provider)
		require.Equal(t, 2000000, *reader.ModelAttributes(model).Context)
		_, err := pricing.ResolveModelPricing(model, reader.GetModelPricing(model))
		require.ErrorIs(t, err, pricing.ErrModelPricingUnavailable)
		hosted := "google-vertex/" + model
		require.Equal(t, "google-vertex", reader.ModelEntry(hosted).Provider)
		require.Equal(t, 128000, *reader.ModelAttributes(hosted).Context)
		price := reader.GetModelPricing(hosted)
		_, err = pricing.ResolveModelPricing(hosted, price)
		require.NoError(t, err)
		require.Equal(t, "google-vertex", price.Provider)
		// Allow binary rounding from million-token price conversion.
		require.InDelta(t, 0.2e-6, price.InputCostPerToken, 1e-12)
		require.InDelta(t, 0.5e-6, price.OutputCostPerToken, 1e-12)
	}
}
