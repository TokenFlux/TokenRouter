package routing

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog/testkit"
	"github.com/TokenFlux/TokenRouter/internal/pkg/locale"
	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
)

// Model display attributes combine saved profile overrides with final upstream
// identities, independently of request aliases and pricing names. The fixtures
// cover language resolution, exact-rule priority, multi-route aggregation, and
// batched group reads; their benchmarks measure repeated equivalent routes and
// provider-address variants.

// attributeRepoFixture supplies mutable profiles and counts single or batch
// display reads.
type attributeRepoFixture struct {
	// config and groups supply shared profiles for single and batch calls. Tests
	// mutate profile status between completed queries so later display resolution
	// observes the same stored value changing.
	config      *ModelAttributeConfig           // shared single-group fixture profile
	groups      map[int64]*ModelAttributeConfig // shared profiles by group
	batchCalls  int                             // completed batch read count
	singleCalls int                             // completed single-group read count
	// requested borrows the ID slice from the latest batch read. The test inspects
	// it after resolution and keeps that input stable.
	requested []int64
	err       error // configured batch query failure
}

// List returns one value copied from the fixture's configured profile. The
// fixture requires a nonnil config; nested data remains shared because this
// helper models repository reads rather than isolation.
func (r *attributeRepoFixture) List(context.Context) ([]ModelAttributeConfig, error) {
	return []ModelAttributeConfig{*r.config}, nil
}

// Get returns the fixture's shared profile pointer for any identifier.
// Save-validation tests can inspect the same value and its retained timestamps
// before subsequent administrative operations.
func (r *attributeRepoFixture) Get(context.Context, int64) (*ModelAttributeConfig, error) {
	return r.config, nil
}

// ForGroup counts a single-group read and returns the shared fixture profile.
// The err field configures batch reads, while this single-group fixture returns
// nil as its error.
func (r *attributeRepoFixture) ForGroup(context.Context, int64) (*ModelAttributeConfig, error) {
	r.singleCalls++
	return r.config, nil
}

// ForGroups counts a batch read, retains the supplied ID slice, and returns its
// configured map and error. Tests inspect requested IDs after the call and keep
// their storage stable.
func (r *attributeRepoFixture) ForGroups(_ context.Context, ids []int64) (map[int64]*ModelAttributeConfig, error) {
	r.batchCalls++
	r.requested = ids
	return r.groups, r.err
}

// Save retains the supplied profile pointer for assertions after validation. It
// models a successful repository write without assigning timestamps or cloning
// configuration, so tests observe the service's prepared value.
func (r *attributeRepoFixture) Save(_ context.Context, value *ModelAttributeConfig) error {
	r.config = value
	return nil
}

// Delete clears the fixture's stored profile and reports success.
func (r *attributeRepoFixture) Delete(context.Context, int64) error { r.config = nil; return nil }

// TestModelAttributesRequestLanguage reads one localized profile in English and
// Simplified Chinese. Export and marketplace resolution must choose the same
// requested-language display name and return equal presentation values.
func TestModelAttributesRequestLanguage(t *testing.T) {
	en := "en"
	content := locale.Update[string]{Content: locale.Content[string]{
		SourceLocale: &en, Source: "English name", Revision: 1, SourceRevision: 1,
		Translations: map[string]locale.Translation[string]{"zh-Hans": {Value: "中文名称", SourceRevision: 1}},
	}}
	config := &ModelAttributeConfig{Status: StatusActive, Rules: []ModelAttributeRule{
		{Models: []string{"model"}, Attributes: modelcatalog.Attributes{DisplayNameLocalization: &content}},
		{Models: []string{"alias"}, ReferenceModel: "model"},
	}}
	service := ModelAttributeService{
		Repo:    &attributeRepoFixture{config: config, groups: map[int64]*ModelAttributeConfig{1: config}},
		Catalog: ModelAttributeCatalog{Lookup: func(string) modelcatalog.Attributes { return modelcatalog.Attributes{} }},
	}
	models := []RequestableModel{{ID: "model"}, {ID: "alias"}}
	for code, want := range map[string]string{"en": "English name", "zh-Hans": "中文名称"} {
		ctx := locale.WithLanguage(context.Background(), code)
		single, err := service.ResolveModels(ctx, 1, models)
		require.NoError(t, err)
		batch, err := service.ResolveGroups(ctx, map[int64][]RequestableModel{1: models})
		require.NoError(t, err)
		require.Equal(t, want, *single["model"].DisplayName)
		require.Equal(t, single["model"], batch[1]["model"])
		marketplace, err := service.ResolveMarketplaceGroups(ctx, map[int64][]RequestableModel{1: models})
		require.NoError(t, err)
		require.Equal(t, single["model"], marketplace[1]["model"].Presentation)
		require.Equal(t, single["model"], single["alias"])
		require.Equal(t, single["alias"], batch[1]["alias"])
		require.Equal(t, single["alias"], marketplace[1]["alias"].Presentation)
	}
}

// TestModelAttributesUseFinalModelsWithoutChangingRoutes resolves an alias with
// two upstream identities and independent price metadata. Results must combine
// their limits and support, preserve the input route value, and restore
// defaults after profile disabling.
func TestModelAttributesUseFinalModelsWithoutChangingRoutes(t *testing.T) {
	yes, no := true, false
	large, small := 100, 1
	config := &ModelAttributeConfig{Status: StatusActive, Rules: []ModelAttributeRule{
		{Models: []string{"upstream-*"}, Attributes: modelcatalog.Attributes{ToolCall: &no}},
		{Models: []string{"upstream-a"}, Attributes: modelcatalog.Attributes{OutputLimit: &small}},
	}}
	service := ModelAttributeService{Repo: &attributeRepoFixture{config: config}, Catalog: ModelAttributeCatalog{Lookup: func(string) modelcatalog.Attributes {
		return modelcatalog.Attributes{OutputLimit: &large, ToolCall: &yes}
	}}}
	models := []RequestableModel{{ID: "public-alias", PricingModel: "unrelated-price", UpstreamModels: []string{"upstream-a", "upstream-b"}}}
	before := models[0]
	result, err := service.ResolveModels(context.Background(), 1, models)
	require.NoError(t, err)
	require.Equal(t, before, models[0])
	require.Equal(t, 1, *result["public-alias"].OutputLimit)
	require.False(t, *result["public-alias"].ToolCall)
	require.True(t, result["public-alias"].RouteDifferences)
	// An exact rule supplies the output limit and takes precedence over wildcard
	// rules.
	result, err = service.ResolveModels(context.Background(), 1, []RequestableModel{{ID: "single", UpstreamModels: []string{"upstream-a"}}})
	require.NoError(t, err)
	require.True(t, *result["single"].ToolCall)
	config.Status = StatusDisabled
	result, err = service.ResolveModels(context.Background(), 1, models)
	require.NoError(t, err)
	require.Equal(t, 100, *result["public-alias"].OutputLimit)
	require.True(t, *result["public-alias"].ToolCall)
}

// TestModelAttributesBatchReads supplies shared profiles, an unconfigured
// group, and an empty group. Resolution must issue one batch read, omit the
// empty group, and observe profile disabling on the next query.
func TestModelAttributesBatchReads(t *testing.T) {
	no := false
	limit := 100
	config := &ModelAttributeConfig{Status: StatusActive, Rules: []ModelAttributeRule{
		{Models: []string{"upstream"}, Attributes: modelcatalog.Attributes{ToolCall: &no}},
	}}
	repo := &attributeRepoFixture{groups: map[int64]*ModelAttributeConfig{1: config, 2: config}}
	service := ModelAttributeService{Repo: repo, Catalog: ModelAttributeCatalog{Lookup: func(string) modelcatalog.Attributes {
		return modelcatalog.Attributes{OutputLimit: &limit}
	}}}
	groups := map[int64][]RequestableModel{
		1: {{ID: "alias-a", UpstreamModels: []string{"upstream"}}},
		2: {{ID: "alias-b", UpstreamModels: []string{"upstream"}}},
		3: {{ID: "upstream"}},
		4: {},
	}
	result, err := service.ResolveGroups(context.Background(), groups)
	require.NoError(t, err)
	require.Equal(t, 1, repo.batchCalls)
	require.Zero(t, repo.singleCalls)
	require.ElementsMatch(t, []int64{1, 2, 3}, repo.requested)
	require.Len(t, result, 3)
	require.False(t, *result[1]["alias-a"].ToolCall)
	require.False(t, *result[2]["alias-b"].ToolCall)
	require.Nil(t, result[3]["upstream"].ToolCall)
	require.Equal(t, 100, *result[3]["upstream"].OutputLimit)
	config.Status = StatusDisabled
	result, err = service.ResolveGroups(context.Background(), groups)
	require.NoError(t, err)
	require.Equal(t, 2, repo.batchCalls)
	require.Nil(t, result[1]["alias-a"].ToolCall)
}

// TestModelAttributesBatchEmptyAndFailure compares empty visible models with a
// failing repository. Empty input must skip the query, while nonempty input
// returns the configured failure and no result map.
func TestModelAttributesBatchEmptyAndFailure(t *testing.T) {
	repo := &attributeRepoFixture{err: errors.New("database unavailable")}
	service := ModelAttributeService{Repo: repo}
	result, err := service.ResolveGroups(context.Background(), map[int64][]RequestableModel{1: {}})
	require.NoError(t, err)
	require.Empty(t, result)
	require.Zero(t, repo.batchCalls)
	result, err = service.ResolveGroups(context.Background(), map[int64][]RequestableModel{1: {{ID: "model"}}})
	require.ErrorIs(t, err, repo.err)
	require.Nil(t, result)
	require.Equal(t, 1, repo.batchCalls)
}

// TestAttributeConfigValidation supplies invalid status, duplicate groups,
// internal stars, and a nonpositive output limit. Each invalid profile must
// fail, while a wildcard rule with an explicit false override saves
// successfully.
func TestAttributeConfigValidation(t *testing.T) {
	service := ModelAttributeService{Repo: &attributeRepoFixture{}}
	zero := 0
	for _, config := range []ModelAttributeConfig{
		{Name: "x", Status: "other"},
		{Name: "x", Status: StatusActive, GroupIDs: []int64{1, 1}},
		{Name: "x", Status: StatusActive, Rules: []ModelAttributeRule{{Models: []string{"a*b"}}}},
		{Name: "x", Status: StatusActive, Rules: []ModelAttributeRule{{Models: []string{"a"}, Attributes: modelcatalog.Attributes{OutputLimit: &zero}}}},
	} {
		require.Error(t, service.Save(context.Background(), &config))
	}
	no := false
	valid := ModelAttributeConfig{Name: "metadata", Status: StatusActive, Rules: []ModelAttributeRule{{Models: []string{"*"}, Attributes: modelcatalog.Attributes{ToolCall: &no}}}}
	require.NoError(t, service.Save(context.Background(), &valid))
}

// TestAttributeRulesSeparateRoutesAndStableIDs saves identical model patterns
// scoped to two platforms. Their generated IDs and output overrides must
// differ, and collapsing both selectors to unrestricted rules must be rejected.
func TestAttributeRulesSeparateRoutesAndStableIDs(t *testing.T) {
	one, two := 1, 2
	config := &ModelAttributeConfig{Name: "routes", Status: StatusActive, Rules: []ModelAttributeRule{
		{Models: []string{"model"}, Match: &ModelAttributeMatch{Platform: "openai"}, Attributes: modelcatalog.Attributes{OutputLimit: &one}},
		{Models: []string{"model"}, Match: &ModelAttributeMatch{Platform: "grok"}, Attributes: modelcatalog.Attributes{OutputLimit: &two}},
	}}
	require.NoError(t, (&ModelAttributeService{Repo: &attributeRepoFixture{}}).Save(context.Background(), config))
	require.NotEqual(t, config.Rules[0].ID, config.Rules[1].ID)
	require.Equal(t, one, *config.matchRule("model", nil, modelrules.Target{Platform: "openai"}).Attributes.OutputLimit)
	require.Equal(t, two, *config.matchRule("model", nil, modelrules.Target{Platform: "grok"}).Attributes.OutputLimit)
	config.Rules[0].Match = nil
	config.Rules[1].Match = &ModelAttributeMatch{}
	require.Error(t, (&ModelAttributeService{Repo: &attributeRepoFixture{}}).Save(context.Background(), config))
}

// BenchmarkModelAttributeRoutes measures batched display resolution for 186
// aliases with one or 65 repeated route sets. Setup stays outside the timer so
// measurements cover catalogue resolution and aggregation.
func BenchmarkModelAttributeRoutes(b *testing.B) {
	view := testkit.OfflineView()
	for _, count := range []int{1, 65} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			svc := &ModelAttributeService{Catalog: ModelAttributeCatalog{View: func() modelrules.View { return view }}}
			models := make([]RequestableModel, 186)
			for i := range models {
				models[i].ID = fmt.Sprint("alias-", i)
				for range count {
					for _, wire := range []string{"openai_responses", "openai_chat_completions", "anthropic_messages"} {
						models[i].AttributeTargets = append(models[i].AttributeTargets, modelrules.Target{Platform: "openai", ProviderType: "apikey", Model: "gpt-5.4", ClientProtocol: wire, UpstreamProtocol: "openai_responses"})
					}
				}
			}
			b.ResetTimer()
			for range b.N {
				_, err := svc.ResolveGroups(context.Background(), map[int64][]RequestableModel{1: models})
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// TestModelAttributesReuseEquivalentRelays supplies duplicated and distinct
// relay routes with one injected attribute lookup. Equivalent services must
// reuse attributes while keeping route inputs and searchable text; adding an
// official service requires another lookup.
func TestModelAttributesReuseEquivalentRelays(t *testing.T) {
	lookups := 0
	svc := &ModelAttributeService{Catalog: ModelAttributeCatalog{Lookup: func(string) modelcatalog.Attributes {
		lookups++
		return modelcatalog.Attributes{DisplayNameLocalization: &locale.Update[string]{Content: locale.Original("Model")}}
	}}}
	first := modelrules.Target{Platform: "openai", ProviderType: "apikey", Model: "model", BaseURL: "https://relay-a.example/v1"}
	second := first
	second.BaseURL = "https://relay-b.example/v1"
	models := []RequestableModel{{ID: "alias", AttributeTargets: []modelrules.Target{first, second, first}}}
	result, err := svc.ResolveModels(context.Background(), 1, models)
	require.NoError(t, err)
	require.Equal(t, 1, lookups)
	require.Equal(t, "Model", *result["alias"].DisplayName)
	require.Equal(t, []string{"Model", "Model"}, result["alias"].SearchTerms)
	require.False(t, result["alias"].RouteDifferences)
	require.Equal(t, []modelrules.Target{first, second, first}, models[0].AttributeTargets)

	official := first
	official.BaseURL = "https://api.openai.com/v1"
	models[0].AttributeTargets = append(models[0].AttributeTargets, official)
	lookups = 0
	_, err = svc.ResolveModels(context.Background(), 1, models)
	require.NoError(t, err)
	require.Equal(t, 2, lookups)
}

// BenchmarkModelAttributeDistinctRoutes measures 186 models across 65 providers
// and three client protocols with shared or distinct relay addresses. Setup is
// excluded so the workload measures display resolution and route-key reuse.
func BenchmarkModelAttributeDistinctRoutes(b *testing.B) {
	for _, distinctRelays := range []bool{false, true} {
		b.Run(fmt.Sprint(distinctRelays), func(b *testing.B) {
			svc := &ModelAttributeService{Catalog: ModelAttributeCatalog{Lookup: func(string) modelcatalog.Attributes {
				yes, limit, modalities := true, 128000, []string{"text", "image"}
				return modelcatalog.Attributes{Context: &limit, Reasoning: &yes, ToolCall: &yes, InputModalities: &modalities}
			}}}
			models := make([]RequestableModel, 186)
			for i := range models {
				models[i].ID = fmt.Sprint("model-", i)
				for provider := range 65 {
					baseURL := ""
					if distinctRelays {
						baseURL = fmt.Sprintf("https://relay-%d.example/v1", provider)
					}
					for _, wire := range []string{"openai_responses", "openai_chat_completions", "anthropic_messages"} {
						models[i].AttributeTargets = append(models[i].AttributeTargets, modelrules.Target{Platform: "openai", ProviderType: "apikey", Model: models[i].ID, BaseURL: baseURL, ClientProtocol: wire, UpstreamProtocol: "openai_responses"})
					}
				}
			}
			b.ResetTimer()
			for range b.N {
				_, err := svc.ResolveGroups(context.Background(), map[int64][]RequestableModel{1: models})
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
