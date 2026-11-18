package provider

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	billingprovider "github.com/TokenFlux/TokenRouter/internal/billing/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"
)

// Catalog fixtures retain full provider identities while tests vary prices,
// attributes, and conditional download results. The helpers construct unloaded
// price services or initialized offline services, and the remote fixture records
// each validator so update tests can inspect their request history.

// mediaAliasFixture and modelsCatalogFixture supply provider-qualified,
// attribute-only, and tiered-price records for supplement and publication tests.
const (
	mediaAliasFixture = `{
	"openai":{"id":"openai","models":{"gpt-image-2":{"id":"gpt-image-2","type":"chat","capabilities":{},"canonical_id":"openai/gpt-image-2","name":"Image","cost":{"text":{"input":{"price":5},"output":{"price":30}}}},"gpt-image-2-snapshot":{"id":"gpt-image-2-snapshot","type":"chat","capabilities":{},"canonical_id":"openai/gpt-image-2","cost":{"text":{"input":{"price":4},"output":{"price":20}}}}}},
	"google-vertex":{"id":"google-vertex","models":{"gpt-image-2":{"id":"gpt-image-2","type":"chat","capabilities":{},"canonical_id":"openai/gpt-image-2","cost":{"text":{"input":{"price":1},"output":{"price":2}}}}}}
}`

	modelsCatalogFixture = `{"anthropic":{"id":"anthropic","models":{"claude-test":{"id":"claude-test","type":"chat","capabilities":{"reasoning":{"supported":true}},"canonical_id":"anthropic/claude-test","name":"Claude","limit":{"output":10},"cost":{"text":{"input":{"price":3,"cache":{"read":0.3,"write":3.75},"overrides":[{"when":{"context":{"gt":100}},"price":6,"cache":{"read":0.6,"write":7.5}},{"when":{"context":{"gt":200}},"price":9,"cache":{"read":0.9,"write":11.25}}]},"output":{"price":15,"overrides":[{"when":{"context":{"gt":100}},"price":30},{"when":{"context":{"gt":200}},"price":45}]}}}},"attributes-only":{"id":"attributes-only","type":"chat","capabilities":{"temperature":false},"name":"No price"}}}}`
)

// modelCatalogFixture supplies options and prices before service initialization.
type modelCatalogFixture struct {
	options Options // startup catalog fixture options
	// pricingData is transferred to NewServiceFromSnapshot by the fixture builder.
	// Tests finish mutations before sharing the constructed service with concurrent queries.
	pricingData map[string]*pricing.CatalogModelPricing
}

// catalogRemoteFixture returns controlled remote results and records conditional validators.
type catalogRemoteFixture struct {
	// mu protects body, err, etag, unchanged, and validators during FetchCatalog.
	// Tests changing those fields while fetches can run acquire the same mutex;
	// returned body bytes remain shared with the fixture.
	mu         sync.Mutex // remote fixture access lock
	body       []byte     // configured remote catalog bytes
	err        error      // configured remote fetch failure
	etag       string     // configured response ETag validator
	unchanged  bool       // configured unchanged body result
	validators []string   // observed request ETag validators
}

// newModelCatalogFixture constructs a query service owning the supplied price
// map and using production identity candidates. It remains uninitialized until
// a test explicitly invokes catalog loading or publication.
func newModelCatalogFixture(fixture modelCatalogFixture) *Service {
	options := fixture.options
	options.ModelLookupCandidates = modelidentity.CandidatesFactory
	return NewServiceFromSnapshot(options, nil, Snapshot{Data: fixture.pricingData})
}

// setPricingFixtureData replaces a fixture's price map before concurrent use.
// The service shares the supplied map and entries, so tests complete their
// changes before starting readers or update work.
func setPricingFixtureData(service *Service, data map[string]*pricing.CatalogModelPricing) {
	service.pricingData = data
}

// newStubCatalogFromJSON constructs an unloaded service with parsed fixture
// prices and production identity candidates. It fails the test on a parser
// error before installing the fixture's retained price data.
func newStubCatalogFromJSON(t *testing.T, body string) *Service {
	t.Helper()
	service := newModelCatalogFixture(modelCatalogFixture{})
	data, err := parsePricingFixture([]byte(body))
	require.NoError(t, err)
	setPricingFixtureData(service, data)
	return service
}

// parsePricingFixture decodes test entries through the production pricing
// parser. Validation diagnostics take precedence over its returned parser
// error, letting tests observe malformed fields before inspecting decoded
// price results.
func parsePricingFixture(body []byte) (map[string]*pricing.CatalogModelPricing, error) {
	raw, err := pricing.DecodeCatalogEntries(body)
	if err != nil {
		return nil, err
	}
	values, diagnostics, err := pricing.ParsePricingEntries(raw)
	if validationErr := diagnostics.ValidationError(); validationErr != nil {
		return nil, validationErr
	}
	return values, err
}

// newOfflinePricingFixture initializes embedded catalog data and supplements
// in a temporary cache directory. It fails the test on initialization error
// and returns the service before any scheduled synchronization starts.
func newOfflinePricingFixture(t *testing.T) *Service {
	t.Helper()
	service := newModelCatalogFixture(modelCatalogFixture{options: Options{DataDir: t.TempDir()}})
	require.NoError(t, service.Initialize())
	return service
}

// catalogPriceForTest resolves a retained catalog entry into billing prices
// for assertions. It reports the calling test's location when resolution
// fails, including requests whose catalog record has no usable token price.
func catalogPriceForTest(t *testing.T, service *Service, model string) *pricing.ModelPricing {
	t.Helper()
	value, err := pricing.ResolveModelPricing(model, service.GetModelPricing(model))
	require.NoError(t, err)
	return value
}

// FetchCatalog records the validator and returns the configured fixture result
// under its mutex. URL and cancellation inputs are ignored, and returned body
// bytes remain shared for read-only test consumption.
func (r *catalogRemoteFixture) FetchCatalog(_ context.Context, _ string, validator string) ([]byte, string, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Append the validator while holding the result lock so conditional-update
	// assertions read one ordered history of the fixture's completed request dispatches.
	r.validators = append(r.validators, validator)
	return r.body, r.etag, r.unchanged, r.err
}

// readCatalogTestFile returns prepared file bytes for publication assertions.
// It fails the calling test on read errors, and callers receive bytes separate
// from subsequent writes to the fixture file.
func readCatalogTestFile(t *testing.T, path string) []byte {
	t.Helper()
	body, err := os.ReadFile(path)
	require.NoError(t, err)
	return body
}

// newBillingFixture constructs a production calculator using the test catalog,
// current clock, and pricing location loader. A nil catalog becomes a nil
// PriceCatalog interface, keeping the absent source distinct from a typed nil.
func newBillingFixture(catalog *Service) *billing.Calculator {
	var source billing.PriceCatalog
	if catalog != nil {
		source = catalog
	}
	return billing.NewCalculator(source, billing.CalculatorOptions{
		Now:          time.Now,
		LoadLocation: billingprovider.LoadPricingLocation,
	})
}
