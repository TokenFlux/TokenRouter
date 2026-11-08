package creative

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/modelcatalog/testkit"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// helpers_test.go supplies target-query fixtures that record creative group and provider reads.
//
// Target-query fixtures count group/provider reads and candidate-inventory
// access separately. Provider support checks retain their requested names,
// allowing direct validation and saved-setting discovery tests to distinguish
// chosen-model resolution from a complete candidate enumeration.

// targetQueryRunRepository satisfies feature-gate storage presence for
// query-only creative fixtures.
type targetQueryRunRepository struct {
	// CreativeRunRepository is left nil because these fixtures perform discovery
	// and validation only. Promoted persistence methods require another
	// implementation.
	CreativeRunRepository // unused task persistence methods
}

// targetQueryFixture records rule and repository reads for single-model
// validation and settings saves.
type targetQueryFixture struct {
	// groupReads counts group reads; providerReads counts platform reads;
	// inventoryReads
	// counts mapping or candidate enumeration. checked separately retains support
	// lookup names so tests can distinguish one selected-model check from
	// inventory work.
	groupReads, providerReads, inventoryReads int
	checked                                   []string // provider support lookup model names
}

// CanBindGroup allows every supplied group in the target-query fixture
// regardless of exclusivity.
func (f *targetQueryFixture) CanBindGroup(int64, bool) bool { return true }

// GetByID returns this shared fixture as the user access value for any
// identity. Tests inspect the same counters after query calls, so this helper
// does not allocate a user copy.
func (f *targetQueryFixture) GetByID(context.Context, int64) (UserAccess, error) { return f, nil }

// GetByIDLite counts one group read and returns a fresh active group-one view
// permitting OpenAI generation. The supplied identifier is ignored, isolating
// model-query behavior from group storage admission.
func (f *targetQueryFixture) GetByIDLite(context.Context, int64) (*GroupView, error) {
	f.groupReads++
	return &GroupView{ID: 1, Active: true, AllowImageGeneration: true, Operations: map[string][]string{PlatformOpenAI: {CreativeOperationGenerate}}}, nil
}

// ListActive obtains the fixture group through GetByIDLite and returns its
// value in a one-entry slice. The call therefore shares the same group-read
// counter used by direct submission validation.
func (f *targetQueryFixture) ListActive(ctx context.Context) ([]GroupView, error) {
	group, err := f.GetByIDLite(ctx, 1)
	return []GroupView{*group}, err
}

// ListSchedulableByGroupIDAndPlatform counts one provider read and returns
// this fixture as the sole provider. Group and platform arguments do not
// filter it; provider PlatformID supplies its OpenAI identity.
func (f *targetQueryFixture) ListSchedulableByGroupIDAndPlatform(context.Context, int64, string) ([]CatalogProvider, error) {
	f.providerReads++
	return []CatalogProvider{f}, nil
}

// PlatformID returns the fixed OpenAI platform identity used by target-query
// model resolution.
func (f *targetQueryFixture) PlatformID() string { return PlatformOpenAI }

// AllowsProtocol admits every source protocol and fallback list in the query
// fixture.
func (f *targetQueryFixture) AllowsProtocol(protocol.ProtocolID, map[protocol.ProtocolID][]protocol.ProtocolID) bool {
	return true
}

// IsSchedulable returns true so query fixtures can focus on model lookup and
// enumeration.
func (f *targetQueryFixture) IsSchedulable() bool { return true }

// GetModelMapping counts an inventory read and returns a fresh
// irrelevant-to-image mapping. Tests require zero inventory calls when public
// admission validates its selected configured model directly.
func (f *targetQueryFixture) GetModelMapping() map[string]string {
	f.inventoryReads++
	return map[string]string{"irrelevant": "gpt-image-1"}
}

// GetConfiguredRequestModels counts an inventory read and returns two fresh
// concrete GPT Image names. Selected-model tests inspect that counter to
// detect catalogue enumeration separate from provider support checks.
func (f *targetQueryFixture) GetConfiguredRequestModels() []string {
	f.inventoryReads++
	return []string{"gpt-image-1", "gpt-image-2"}
}

// ResolveMappedModel returns its input spelling with matched false for this
// unmapped provider fixture. Group mapping and image-name checks can therefore
// be inspected without introducing provider remapping.
func (f *targetQueryFixture) ResolveMappedModel(model string) (string, bool) { return model, false }

// IsModelSupported appends the supplied name to shared support observations
// and admits it. The fixture separates these checks from candidate
// enumeration, and tests read checked after their query completes.
func (f *targetQueryFixture) IsModelSupported(model string) bool {
	f.checked = append(f.checked, model)
	return true
}

// IsCreativeEnabled returns true for the target-query fixture runtime feature
// switch.
func (f *targetQueryFixture) IsCreativeEnabled(context.Context) bool { return true }

// GetCreativeModelSettings returns a fresh group-one setting permitting GPT
// Image two generation. The exact selected name drives public queries
// independently from the provider two-entry candidate inventory.
func (f *targetQueryFixture) GetCreativeModelSettings(context.Context) []CreativeModelSetting {
	return []CreativeModelSetting{{GroupID: 1, Model: "gpt-image-2", Operations: []string{CreativeOperationGenerate}}}
}

// newTargetQueryPublic binds the counting fixture to query-only Public
// dependencies and a fixed positive quote. Its embedded repository supplies
// presence for Enabled; callers use discovery or validation rather than task
// persistence.
func newTargetQueryPublic(fixture *targetQueryFixture) *Public {
	return &Public{Attributes: &routing.ModelAttributeService{Catalog: routing.ModelAttributeCatalog{View: testkit.OfflineView}}, Repo: &targetQueryRunRepository{}, ProviderRepo: fixture, UserRepo: fixture, GroupRepo: fixture, Settings: fixture, Options: PublicOptions{Enabled: true}, ImageUnitPrice: func(context.Context, *GroupView, string, string) (float64, bool) { return 0.1, true }}
}

// SupportsImageGIF keeps the generic target-query fixture limited to static images.
func (f *targetQueryFixture) SupportsImageGIF(protocol.ProtocolID) bool { return false }
