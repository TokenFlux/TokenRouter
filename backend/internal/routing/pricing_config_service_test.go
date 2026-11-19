package routing

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
)

// Shared pricing lookup uses group associations and complete model
// identities, with exact matches ahead of wildcard cards in supplied order.
// Snapshot and lookup scenarios seed repository rows directly, including
// overlapping patterns that normal write validation rejects, to inspect
// runtime behavior for those inputs.
//
// Group mapping and restriction scenarios use an independent reader.
// makePolicyRepo deliberately derives that fixture policy from configuration
// status, billing source, and card names, so its price-shaped setup
// represents supplied test rules. Separate CRUD callbacks expose validation
// errors, reloads, snapshot rebuild counts, and authentication invalidation
// requests. Copy assertions exercise selected scalar or slice observations,
// and successful write callbacks may still reload an unchanged fixture when a
// test has not implemented storage.

// mockPricingConfigRepository supplies per-operation callbacks and empty
// successful defaults for pricing tests. GetByID is the exception: its absent
// callback reports ErrPricingConfigNotFound, while readGroup configures a
// separate service policy reader.
type mockPricingConfigRepository struct {
	// readGroup is installed as the service's policy reader. Nil leaves group
	// policy absent; configured callbacks supply that state separately from the
	// cached price rows.
	readGroup func(context.Context, int64) (*Group, error)
	// listAllFn supplies ListAll's snapshot rows when installed. The callback
	// chooses the returned slice and error, letting tests control stored input and
	// count cache loads.
	listAllFn func(ctx context.Context) ([]PricingConfig, error)
	// getGroupPlatformsFn supplies GetGroupPlatforms metadata when installed.
	// Tests can configure a failure or panic here to expose a pricing path that
	// consults the supplied platform metadata.
	getGroupPlatformsFn func(ctx context.Context, groupIDs []int64) (map[int64]string, error)
	// createFn receives Create's working configuration when installed. Tests
	// use its pointer to assign an ID or capture fields from the attempted write.
	createFn func(ctx context.Context, pricingConfig *PricingConfig) error
	// getByIDFn supplies GetByID's configuration and error when installed. Its
	// chosen value determines what CRUD reloads observe after a write callback
	// returns.
	getByIDFn func(ctx context.Context, id int64) (*PricingConfig, error)
	// updateFn receives Update's working configuration when installed. The
	// callback decides whether to capture its fields, change stored fixture data,
	// or report a write failure.
	updateFn func(ctx context.Context, pricingConfig *PricingConfig) error
	// deleteFn receives Delete's requested ID when installed. Tests use it to
	// record removal attempts or decide whether their stored fixture changes.
	deleteFn func(ctx context.Context, id int64) error
	// listFn receives List's pagination, status, and search filters when
	// installed. The callback controls result storage and supplies any page
	// metadata the test expects to return.
	listFn func(ctx context.Context, params pagination.PaginationParams, status, search string) ([]PricingConfig, *pagination.PaginationResult, error)
	// existsByNameFn controls ExistsByName's answer when installed. Its supplied
	// conflict or read error selects the creation validation path exercised by
	// the test.
	existsByNameFn func(ctx context.Context, name string) (bool, error)
	// existsByNameExcludingFn receives a changed name and its current ID when
	// installed. Tests use those inputs to supply a name conflict or read failure
	// for update validation.
	existsByNameExcludingFn func(ctx context.Context, name string, excludeID int64) (bool, error)
	// getGroupIDsFn supplies GetGroupIDs membership observations when installed.
	// Its chosen IDs determine authentication invalidation inputs, and the
	// callback can also report a membership-read failure.
	getGroupIDsFn func(ctx context.Context, pricingConfigID int64) ([]int64, error)
	// setGroupIDsFn receives the original membership slice when installed.
	// Callbacks choose whether to retain or copy its storage when recording a
	// replacement attempt.
	setGroupIDsFn func(ctx context.Context, pricingConfigID int64, groupIDs []int64) error
	// getPricingConfigIDByGroupIDFn controls association lookup when installed.
	// The callback supplies a configuration identity or read error for the
	// requested group, with any observations retained by the test.
	getPricingConfigIDByGroupIDFn func(ctx context.Context, groupID int64) (int64, error)
	// getGroupsInOtherPricingConfigsFn receives the current configuration and
	// requested group IDs when installed. Its supplied conflicting IDs or error
	// select the membership validation result.
	getGroupsInOtherPricingConfigsFn func(ctx context.Context, pricingConfigID int64, groupIDs []int64) ([]int64, error)
	// listModelPricingFn supplies ListModelPricing cards when installed. The
	// callback controls their backing storage and error, and its results are
	// forwarded directly to the caller.
	listModelPricingFn func(ctx context.Context, pricingConfigID int64) ([]ModelPricingEntry, error)
	// createModelPricingFn receives the card pointer when installed. The
	// callback can assign a card identity or capture submitted amounts through
	// that same working object.
	createModelPricingFn func(ctx context.Context, pricing *ModelPricingEntry) error
	// updateModelPricingFn receives the card pointer when installed. The
	// callback determines which submitted fields are observed and whether the
	// fixture's stored card changes.
	updateModelPricingFn func(ctx context.Context, pricing *ModelPricingEntry) error
	// deleteModelPricingFn receives the card ID when installed. The callback
	// controls failure injection and any removal observation retained by the
	// test.
	deleteModelPricingFn func(ctx context.Context, id int64) error
	// replaceModelPricingFn receives the original card slice when installed.
	// The callback chooses how to capture its storage and whether replacing the
	// fixture's stored cards succeeds.
	replaceModelPricingFn func(ctx context.Context, pricingConfigID int64, pricingList []ModelPricingEntry) error
}

// mockPricingConfigAuthCacheInvalidator records credential, user, and group
// invalidation requests for assertions.
type mockPricingConfigAuthCacheInvalidator struct {
	invalidatedGroupIDs []int64  // recorded group invalidation requests
	invalidatedKeys     []string // recorded credential invalidation requests
	invalidatedUserIDs  []int64  // recorded user invalidation requests
}

// TestPricingConfigPublicationOwnsSnapshot builds a cache from one associated
// configuration and mutates the input status afterward. Assertions check the
// cached active status and group association ID.
func TestPricingConfigPublicationOwnsSnapshot(t *testing.T) {
	pricingConfigs := []PricingConfig{{ID: 1, Status: StatusActive, GroupIDs: []int64{9}}}

	cache := populatePricingConfigCache(pricingConfigs)
	pricingConfigs[0].Status = "disabled"

	require.Equal(t, StatusActive, cache.byID[1].Status)
	require.Equal(t, int64(1), cache.pricingConfigByGroupID[9].ID)
}

// TestBuildModelMappingChain varies requested, group-mapped, and upstream
// names with and without a mapping flag. Assertions check an empty chain for
// unchanged identity and the ordered distinct stage text otherwise.
func TestBuildModelMappingChain(t *testing.T) {
	tests := []struct {
		name          string             // subtest model chain label
		result        GroupMappingResult // supplied group mapping metadata
		requestModel  string             // configured client request name
		upstreamModel string             // configured upstream execution name
		want          string             // expected displayed model chain
	}{
		{
			name:          "no mapping, no upstream diff",
			result:        GroupMappingResult{Mapped: false, MappedModel: "claude-sonnet-4"},
			requestModel:  "claude-sonnet-4",
			upstreamModel: "claude-sonnet-4",
			want:          "",
		},
		{
			name:          "no mapping, upstream differs",
			result:        GroupMappingResult{Mapped: false, MappedModel: "claude-sonnet-4"},
			requestModel:  "claude-sonnet-4",
			upstreamModel: "claude-sonnet-4-20250514",
			want:          "claude-sonnet-4\u2192claude-sonnet-4-20250514",
		},
		{
			name:          "mapped, upstream differs",
			result:        GroupMappingResult{Mapped: true, MappedModel: "claude-sonnet-4-20250514"},
			requestModel:  "my-model",
			upstreamModel: "actual-upstream",
			want:          "my-model\u2192claude-sonnet-4-20250514\u2192actual-upstream",
		},
		{
			name:          "mapped, upstream same as mapped",
			result:        GroupMappingResult{Mapped: true, MappedModel: "claude-sonnet-4-20250514"},
			requestModel:  "claude-sonnet-4",
			upstreamModel: "claude-sonnet-4-20250514",
			want:          "claude-sonnet-4\u2192claude-sonnet-4-20250514",
		},
		{
			name:          "mapped, upstream empty",
			result:        GroupMappingResult{Mapped: true, MappedModel: "target-model"},
			requestModel:  "my-model",
			upstreamModel: "",
			want:          "my-model\u2192target-model",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.result.BuildModelMappingChain(tt.requestModel, tt.upstreamModel)
			require.Equal(t, tt.want, got)
		})
	}
}

// TestGetPricingConfigForGroup_Success looks up a group-associated active
// configuration and changes the returned name. A subsequent read returns the
// configured name, demonstrating isolation for that scalar field.
func TestGetPricingConfigForGroup_Success(t *testing.T) {
	ch := PricingConfig{
		ID:       1,
		Name:     "test-price-config",
		Status:   StatusActive,
		GroupIDs: []int64{10},
	}
	repo := makeStandardRepo(ch, map[int64]string{10: "anthropic"})
	svc := newTestPricingConfigService(repo)

	result, err := svc.GetPricingConfigForGroup(context.Background(), 10)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, int64(1), result.ID)
	require.Equal(t, "test-price-config", result.Name)

	result.Name = "mutated"
	result2, err := svc.GetPricingConfigForGroup(context.Background(), 10)
	require.NoError(t, err)
	require.Equal(t, "test-price-config", result2.Name)
}

// TestGetPricingConfigForGroup_InactivePricingConfig supplies a disabled
// configuration still associated with the requested group. The lookup
// succeeds with nil configuration rather than exposing the disabled row.
func TestGetPricingConfigForGroup_InactivePricingConfig(t *testing.T) {
	ch := PricingConfig{
		ID:       1,
		Status:   StatusDisabled,
		GroupIDs: []int64{10},
	}
	repo := makeStandardRepo(ch, map[int64]string{10: "anthropic"})
	svc := newTestPricingConfigService(repo)

	result, err := svc.GetPricingConfigForGroup(context.Background(), 10)
	require.NoError(t, err)
	require.Nil(t, result)
}

// TestGetPricingConfigForGroup_NoPricingConfig requests group 999, which is
// absent from the supplied configuration's membership list. The lookup
// returns nil configuration with no error.
func TestGetPricingConfigForGroup_NoPricingConfig(t *testing.T) {
	ch := PricingConfig{
		ID:       1,
		Status:   StatusActive,
		GroupIDs: []int64{10},
	}
	repo := makeStandardRepo(ch, map[int64]string{10: "anthropic"})
	svc := newTestPricingConfigService(repo)

	result, err := svc.GetPricingConfigForGroup(context.Background(), 999)
	require.NoError(t, err)
	require.Nil(t, result)
}

// TestGetPricingConfigForGroup_CacheError injects failure into the
// configuration snapshot read. The initiating lookup returns an error
// containing the database message and a nil configuration.
func TestGetPricingConfigForGroup_CacheError(t *testing.T) {
	repo := &mockPricingConfigRepository{
		listAllFn: func(_ context.Context) ([]PricingConfig, error) {
			return nil, errors.New("db connection failed")
		},
	}
	svc := newTestPricingConfigService(repo)

	result, err := svc.GetPricingConfigForGroup(context.Background(), 10)
	require.Error(t, err)
	require.Nil(t, result)
	require.Contains(t, err.Error(), "db connection failed")
}

// TestGetConfigModelPricing_ExactMatch supplies one explicitly named price
// card for an active associated configuration. The model lookup returns that
// card ID and its small input amount within tolerance.
func TestGetConfigModelPricing_ExactMatch(t *testing.T) {
	ch := PricingConfig{
		ID:       1,
		Status:   StatusActive,
		GroupIDs: []int64{10},
		ModelPricing: []ModelPricingEntry{
			{ID: 100, Models: []string{"claude-opus-4"}, InputPrice: testPtrFloat64(15e-6)},
		},
	}
	repo := makeStandardRepo(ch, map[int64]string{10: "anthropic"})
	svc := newTestPricingConfigService(repo)

	result := svc.GetConfigModelPricing(context.Background(), 10, "claude-opus-4")
	require.NotNil(t, result)
	require.Equal(t, int64(100), result.ID)
	// The tiny absolute tolerance compares stored dollar-denominated price
	// amounts.
	require.InDelta(t, 15e-6, *result.InputPrice, 1e-12)
}

// TestGetConfigModelPricing_CaseInsensitive looks up a stored lowercase model
// name using mixed case. The returned card retains the configured ID,
// demonstrating case-insensitive price identity.
func TestGetConfigModelPricing_CaseInsensitive(t *testing.T) {
	ch := PricingConfig{
		ID:       1,
		Status:   StatusActive,
		GroupIDs: []int64{10},
		ModelPricing: []ModelPricingEntry{
			{ID: 100, Models: []string{"claude-opus-4"}, InputPrice: testPtrFloat64(15e-6)},
		},
	}
	repo := makeStandardRepo(ch, map[int64]string{10: "anthropic"})
	svc := newTestPricingConfigService(repo)

	result := svc.GetConfigModelPricing(context.Background(), 10, "Claude-Opus-4")
	require.NotNil(t, result)
	require.Equal(t, int64(100), result.ID)
}

// TestGetConfigModelPricing_NormalizesDotsAndHyphens stores a dotted full
// model ID and queries both dotted and hyphenated forms. Configured and
// effective lookups reject the hyphenated form and return the dotted
// per-request card.
func TestGetConfigModelPricing_NormalizesDotsAndHyphens(t *testing.T) {
	ch := PricingConfig{
		ID:       1,
		Status:   StatusActive,
		GroupIDs: []int64{10},
		ModelPricing: []ModelPricingEntry{
			{ID: 100, Models: []string{"claude-opus-4.8"}, BillingMode: BillingModePerRequest, PerRequestPrice: testPtrFloat64(0.007)},
		},
	}
	repo := makeStandardRepo(ch, map[int64]string{10: "anthropic"})
	svc := newTestPricingConfigService(repo)

	require.Nil(t, svc.GetConfigModelPricing(context.Background(), 10, "claude-opus-4-8"))
	result := svc.GetConfigModelPricing(context.Background(), 10, "claude-opus-4.8")
	require.NotNil(t, result)
	require.Equal(t, int64(100), result.ID)
	require.Equal(t, BillingModePerRequest, result.BillingMode)
	// The tiny absolute tolerance compares stored dollar-denominated price
	// amounts.
	require.InDelta(t, 0.007, *result.PerRequestPrice, 1e-12)

	require.Nil(t, svc.GetEffectiveConfigModelPricing(context.Background(), 10, "claude-opus-4-8"))
	effective := svc.GetEffectiveConfigModelPricing(context.Background(), 10, "claude-opus-4.8")
	require.NotNil(t, effective)
	require.Equal(t, int64(100), effective.ID)
	require.Equal(t, BillingModePerRequest, effective.BillingMode)
	// The tiny absolute tolerance compares stored dollar-denominated price
	// amounts.
	require.InDelta(t, 0.007, *effective.PerRequestPrice, 1e-12)
}

// TestGetConfigModelPricing_WildcardMatch supplies a trailing-wildcard price
// card and queries a matching Claude name. The lookup returns the ID of that
// configured card.
func TestGetConfigModelPricing_WildcardMatch(t *testing.T) {
	ch := PricingConfig{
		ID:       1,
		Status:   StatusActive,
		GroupIDs: []int64{10},
		ModelPricing: []ModelPricingEntry{
			{ID: 200, Models: []string{"claude-*"}, InputPrice: testPtrFloat64(10e-6)},
		},
	}
	repo := makeStandardRepo(ch, map[int64]string{10: "anthropic"})
	svc := newTestPricingConfigService(repo)

	result := svc.GetConfigModelPricing(context.Background(), 10, "claude-sonnet-4")
	require.NotNil(t, result)
	require.Equal(t, int64(200), result.ID)
}

// TestGetConfigModelPricing_WildcardFirstMatch seeds overlapping wildcard
// rows directly with the shorter prefix first. Runtime lookup returns that
// first card and its price, bypassing write-validation rejection of overlap.
func TestGetConfigModelPricing_WildcardFirstMatch(t *testing.T) {
	ch := PricingConfig{
		ID:       1,
		Status:   StatusActive,
		GroupIDs: []int64{10},
		ModelPricing: []ModelPricingEntry{
			{ID: 200, Models: []string{"claude-*"}, InputPrice: testPtrFloat64(10e-6)},
			{ID: 300, Models: []string{"claude-sonnet-*"}, InputPrice: testPtrFloat64(5e-6)},
		},
	}
	repo := makeStandardRepo(ch, map[int64]string{10: "anthropic"})
	svc := newTestPricingConfigService(repo)

	result := svc.GetConfigModelPricing(context.Background(), 10, "claude-sonnet-4-20250514")
	require.NotNil(t, result)
	require.Equal(t, int64(200), result.ID)
	// The tiny absolute tolerance compares stored dollar-denominated price
	// amounts.
	require.InDelta(t, 10e-6, *result.InputPrice, 1e-12)
}

// TestGetConfigModelPricing_NoMatch supplies a Claude card and queries an
// unrelated GPT model. The configured price lookup returns nil for the absent
// model identity.
func TestGetConfigModelPricing_NoMatch(t *testing.T) {
	ch := PricingConfig{
		ID:       1,
		Status:   StatusActive,
		GroupIDs: []int64{10},
		ModelPricing: []ModelPricingEntry{
			{ID: 100, Models: []string{"claude-opus-4"}, InputPrice: testPtrFloat64(15e-6)},
		},
	}
	repo := makeStandardRepo(ch, map[int64]string{10: "anthropic"})
	svc := newTestPricingConfigService(repo)

	result := svc.GetConfigModelPricing(context.Background(), 10, "gpt-5.1")
	require.Nil(t, result)
}

// TestGetConfigModelPricing_InactivePricingConfig supplies a matching card
// inside a disabled associated configuration. The lookup returns nil, so the
// inactive row provides no configured override.
func TestGetConfigModelPricing_InactivePricingConfig(t *testing.T) {
	ch := PricingConfig{
		ID:       1,
		Status:   StatusDisabled,
		GroupIDs: []int64{10},
		ModelPricing: []ModelPricingEntry{
			{ID: 100, Models: []string{"claude-opus-4"}},
		},
	}
	repo := makeStandardRepo(ch, map[int64]string{10: "anthropic"})
	svc := newTestPricingConfigService(repo)

	result := svc.GetConfigModelPricing(context.Background(), 10, "claude-opus-4")
	require.Nil(t, result)
}

// TestGetConfigModelPricing_UnifiedModelSpace associates two groups with one
// configuration containing Claude and GPT cards. Both groups can retrieve
// both families, including the asserted shared card IDs.
func TestGetConfigModelPricing_UnifiedModelSpace(t *testing.T) {
	ch := PricingConfig{
		ID:       1,
		Status:   StatusActive,
		GroupIDs: []int64{10, 20},
		ModelPricing: []ModelPricingEntry{
			{ID: 100, Models: []string{"gpt-5.1"}, InputPrice: testPtrFloat64(5e-6)},
			{ID: 200, Models: []string{"claude-opus-4"}, InputPrice: testPtrFloat64(15e-6)},
		},
	}
	repo := makeStandardRepo(ch, map[int64]string{10: "anthropic", 20: "openai"})
	svc := newTestPricingConfigService(repo)

	result := svc.GetConfigModelPricing(context.Background(), 10, "gpt-5.1")
	require.NotNil(t, result)

	result = svc.GetConfigModelPricing(context.Background(), 10, "claude-opus-4")
	require.NotNil(t, result)
	require.Equal(t, int64(200), result.ID)

	result = svc.GetConfigModelPricing(context.Background(), 20, "gpt-5.1")
	require.NotNil(t, result)
	require.Equal(t, int64(100), result.ID)

	result = svc.GetConfigModelPricing(context.Background(), 20, "claude-opus-4")
	require.NotNil(t, result)
}

// TestGetConfigModelPricing_ReturnsCopy appends a name and changes the ID in
// a returned card. A later lookup retains the cached model-list length and
// ID, checking those selected observations.
func TestGetConfigModelPricing_ReturnsCopy(t *testing.T) {
	ch := PricingConfig{
		ID:       1,
		Status:   StatusActive,
		GroupIDs: []int64{10},
		ModelPricing: []ModelPricingEntry{
			{ID: 100, Models: []string{"claude-opus-4"}, InputPrice: testPtrFloat64(15e-6)},
		},
	}
	repo := makeStandardRepo(ch, map[int64]string{10: "anthropic"})
	svc := newTestPricingConfigService(repo)

	result := svc.GetConfigModelPricing(context.Background(), 10, "claude-opus-4")
	require.NotNil(t, result)

	result.Models = append(result.Models, "hacked")
	result.ID = 999

	result2 := svc.GetConfigModelPricing(context.Background(), 10, "claude-opus-4")
	require.NotNil(t, result2)
	require.Equal(t, 1, len(result2.Models))
	require.Equal(t, int64(100), result2.ID)
}

// TestGetConfigModelPricing_EntryPointLoadBehavior exercises both card lookups
// with usable, absent, inactive, and failing configuration reads. It checks
// returned cards, diagnostic messages, and read counts before and after cache
// reuse.
func TestGetConfigModelPricing_EntryPointLoadBehavior(t *testing.T) {
	active := PricingConfig{
		ID:       1,
		Status:   StatusActive,
		GroupIDs: []int64{10},
		ModelPricing: []ModelPricingEntry{
			{ID: 100, Models: []string{"model"}, InputPrice: testPtrFloat64(0)},
		},
	}
	inactive := active
	inactive.Status = StatusDisabled
	loadErr := errors.New("price rows unavailable")
	now := time.Date(2026, time.October, 14, 0, 0, 0, 0, time.UTC)

	for _, method := range []struct {
		// public lookup entry name
		name string
		// selected public card lookup
		lookup func(*PricingConfigService, context.Context, int64, string) *ModelPricingEntry
	}{
		{name: "configured", lookup: (*PricingConfigService).GetConfigModelPricing},
		{name: "effective", lookup: (*PricingConfigService).GetEffectiveConfigModelPricing},
	} {
		t.Run(method.name, func(t *testing.T) {
			for _, scenario := range []struct {
				name    string          // configuration read scenario name
				configs []PricingConfig // repository configuration read rows
				err     error           // injected repository read failure
				wantID  int64           // expected returned card ID
			}{
				{name: "active", configs: []PricingConfig{active}, wantID: 100},
				{name: "inactive", configs: []PricingConfig{inactive}},
				{name: "missing", configs: nil},
				{name: "unassociated", configs: []PricingConfig{{ID: 1, Status: StatusActive}}},
				{name: "load_error", err: loadErr},
			} {
				t.Run(scenario.name, func(t *testing.T) {
					reads := 0
					clockReads := 0
					var messages []string
					var warningArgs [][]any
					repo := &mockPricingConfigRepository{
						listAllFn: func(context.Context) ([]PricingConfig, error) {
							reads++
							return scenario.configs, scenario.err
						},
					}
					svc := NewPricingConfigService(repo, nil, PricingConfigOptions{
						Now: func() time.Time {
							clockReads++
							return now
						},
						Warn: func(message string, args ...any) {
							messages = append(messages, message)
							warningArgs = append(warningArgs, args)
						},
					})

					result := method.lookup(svc, context.Background(), 10, "model")
					if scenario.wantID == 0 {
						require.Nil(t, result)
					} else {
						require.NotNil(t, result)
						require.Equal(t, scenario.wantID, result.ID)
					}
					require.Equal(t, 1, reads)
					require.Equal(t, 1, clockReads)

					if scenario.err == nil {
						require.Empty(t, messages)
					} else {
						require.Equal(t, []string{
							"failed to build pricing configuration cache",
							"failed to load pricing configuration cache",
						}, messages)
						require.Equal(t, []any{"error", loadErr}, warningArgs[0])
						require.Len(t, warningArgs[1], 4)
						require.Equal(t, []any{"group_id", int64(10), "error"}, warningArgs[1][:3])
						warnErr, ok := warningArgs[1][3].(error)
						require.True(t, ok)
						require.ErrorIs(t, warnErr, loadErr)
						require.EqualError(t, warnErr, "list all price configurations: price rows unavailable")
					}

					// Reuse the published success or empty error snapshot at the same clock
					// instant, making repeated repository reads and diagnostics observable.
					again := method.lookup(svc, context.Background(), 10, "model")
					require.Equal(t, result, again)
					require.Equal(t, 1, reads)
					require.Equal(t, 2, clockReads)
					if scenario.err != nil {
						require.Len(t, messages, 2)
					} else {
						require.Empty(t, messages)
					}
				})
			}
		})
	}
}

// TestGetConfigModelPricing_EntryPointSelection compares configured and
// effective lookup across exact and ordered wildcard cards. Blank cards and
// explicit zero prices protect the eligibility difference between the two
// public methods.
func TestGetConfigModelPricing_EntryPointSelection(t *testing.T) {
	for _, scenario := range []struct {
		name             string              // price card selection scenario
		cards            []ModelPricingEntry // configured card match order
		model            string              // supplied complete model identity
		wantConfiguredID int64               // expected configured card ID
		wantEffectiveID  int64               // expected effective card ID
	}{
		{
			name: "zero_exact_before_wildcard",
			cards: []ModelPricingEntry{
				{ID: 200, Models: []string{"model*"}, InputPrice: testPtrFloat64(5)},
				{ID: 100, Models: []string{"model.2"}, InputPrice: testPtrFloat64(0)},
			},
			model: " MODEL.2 ", wantConfiguredID: 100, wantEffectiveID: 100,
		},
		{
			name: "zero_request_exact_before_wildcard",
			cards: []ModelPricingEntry{
				{ID: 200, Models: []string{"model*"}, InputPrice: testPtrFloat64(5)},
				{ID: 100, Models: []string{"model.2"}, BillingMode: BillingModePerRequest, PerRequestPrice: testPtrFloat64(0)},
			},
			model: "model.2", wantConfiguredID: 100, wantEffectiveID: 100,
		},
		{
			name: "blank_exact_before_effective_wildcard",
			cards: []ModelPricingEntry{
				{ID: 200, Models: []string{"model*"}, InputPrice: testPtrFloat64(5)},
				{ID: 100, Models: []string{"model.2"}},
			},
			model: "model.2", wantConfiguredID: 100, wantEffectiveID: 200,
		},
		{
			name: "blank_wildcard_before_effective_wildcards",
			cards: []ModelPricingEntry{
				{ID: 100, Models: []string{"model*"}},
				{ID: 200, Models: []string{"model*"}, InputPrice: testPtrFloat64(5)},
				{ID: 300, Models: []string{"model.2*"}, InputPrice: testPtrFloat64(6)},
			},
			model: "model.2", wantConfiguredID: 100, wantEffectiveID: 200,
		},
		{
			name: "effective_wildcards_in_configuration_order",
			cards: []ModelPricingEntry{
				{ID: 200, Models: []string{"model*"}, InputPrice: testPtrFloat64(5)},
				{ID: 300, Models: []string{"model.2*"}, InputPrice: testPtrFloat64(6)},
			},
			model: "model.2", wantConfiguredID: 200, wantEffectiveID: 200,
		},
		{
			name: "supported_modifier_exact",
			cards: []ModelPricingEntry{
				{ID: 100, Models: []string{"model.2"}, FastMultiplier: testPtrFloat64(2)},
			},
			model: "model.2", wantConfiguredID: 100, wantEffectiveID: 100,
		},
		{
			name: "blank_exact_only",
			cards: []ModelPricingEntry{
				{ID: 100, Models: []string{"model.2"}},
			},
			model: "model.2", wantConfiguredID: 100,
		},
		{
			name: "different_complete_identity",
			cards: []ModelPricingEntry{
				{ID: 100, Models: []string{"model.2"}, InputPrice: testPtrFloat64(5)},
			},
			model: "model-2",
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Direct rows admit legacy overlaps rejected by management validation,
			// allowing these cases to exercise the configured order used by runtime
			// wildcard matching.
			repo := makeStandardRepo(PricingConfig{
				ID: 1, Status: StatusActive, GroupIDs: []int64{10}, ModelPricing: scenario.cards,
			}, nil)
			svc := newTestPricingConfigService(repo)
			for _, method := range []struct {
				// public lookup entry name
				name string
				// selected public card lookup
				lookup func(*PricingConfigService, context.Context, int64, string) *ModelPricingEntry
				// expected returned card ID
				wantID int64
			}{
				{name: "configured", lookup: (*PricingConfigService).GetConfigModelPricing, wantID: scenario.wantConfiguredID},
				{name: "effective", lookup: (*PricingConfigService).GetEffectiveConfigModelPricing, wantID: scenario.wantEffectiveID},
			} {
				t.Run(method.name, func(t *testing.T) {
					result := method.lookup(svc, context.Background(), 10, scenario.model)
					if method.wantID == 0 {
						require.Nil(t, result)
					} else {
						require.NotNil(t, result)
						require.Equal(t, method.wantID, result.ID)
					}
				})
			}
		})
	}
}

// TestGetConfigModelPricing_EntryPointNestedCopies mutates returned model,
// amount, interval, and time-pricing values through both public lookups. Later
// lookups and the repository fixture retain every seeded value and nullable
// distinction.
func TestGetConfigModelPricing_EntryPointNestedCopies(t *testing.T) {
	card := ModelPricingEntry{
		ID:                           100,
		Models:                       []string{"model"},
		BillingMode:                  BillingModeToken,
		PriceMultiplier:              testPtrFloat64(1.2),
		FastModeMultiplier:           testPtrFloat64(3),
		FastMultiplier:               testPtrFloat64(4),
		FlexMultiplier:               testPtrFloat64(0.5),
		MaxReasoningEffortMultiplier: testPtrFloat64(2),
		InputPrice:                   testPtrFloat64(1),
		OutputPrice:                  testPtrFloat64(2),
		CacheWritePrice:              testPtrFloat64(3),
		CacheWrite1hPrice:            testPtrFloat64(0),
		ImageInputPrice:              testPtrFloat64(4),
		ImageOutputPrice:             testPtrFloat64(5),
		PerRequestPrice:              testPtrFloat64(6),
		Intervals: []PricingInterval{{
			MaxTokens:            testPtrInt(1000),
			TierLabel:            "standard",
			InputPrice:           testPtrFloat64(7),
			OutputPrice:          testPtrFloat64(8),
			CacheWritePrice:      testPtrFloat64(9),
			CacheWrite1hPrice:    testPtrFloat64(0),
			CacheReadPrice:       testPtrFloat64(10),
			PerRequestPrice:      testPtrFloat64(11),
			InputMultiplier:      testPtrFloat64(1.1),
			OutputMultiplier:     testPtrFloat64(1.2),
			CacheWriteMultiplier: testPtrFloat64(1.3),
			CacheReadMultiplier:  testPtrFloat64(1.4),
		}},
		TimePricing: &TimePricingConfig{
			Timezone:     "UTC",
			WeekdaysOnly: true,
			Periods:      []TimePricingPeriod{{StartTime: "01:00", EndTime: "02:00", Multiplier: 2}},
		},
	}
	for _, method := range []struct {
		// public lookup entry name
		name string
		// selected public card lookup
		lookup func(*PricingConfigService, context.Context, int64, string) *ModelPricingEntry
	}{
		{name: "configured", lookup: (*PricingConfigService).GetConfigModelPricing},
		{name: "effective", lookup: (*PricingConfigService).GetEffectiveConfigModelPricing},
	} {
		t.Run(method.name, func(t *testing.T) {
			repo := makeStandardRepo(PricingConfig{
				ID: 1, Status: StatusActive, GroupIDs: []int64{10}, ModelPricing: []ModelPricingEntry{card},
			}, nil)
			svc := newTestPricingConfigService(repo)
			result := method.lookup(svc, context.Background(), 10, "model")
			require.NotNil(t, result)
			require.Equal(t, card, *result)

			result.ID = 999
			result.Models[0] = "edited"
			for _, amount := range []*float64{
				result.PriceMultiplier, result.FastModeMultiplier, result.FastMultiplier,
				result.FlexMultiplier, result.MaxReasoningEffortMultiplier,
				result.InputPrice, result.OutputPrice, result.CacheWritePrice,
				result.CacheWrite1hPrice, result.ImageInputPrice, result.ImageOutputPrice,
				result.PerRequestPrice, result.Intervals[0].InputPrice, result.Intervals[0].OutputPrice,
				result.Intervals[0].CacheWritePrice, result.Intervals[0].CacheWrite1hPrice,
				result.Intervals[0].CacheReadPrice, result.Intervals[0].PerRequestPrice,
				result.Intervals[0].InputMultiplier, result.Intervals[0].OutputMultiplier,
				result.Intervals[0].CacheWriteMultiplier, result.Intervals[0].CacheReadMultiplier,
			} {
				*amount = -1
			}
			*result.Intervals[0].MaxTokens = 1
			result.Intervals[0].TierLabel = "edited"
			result.TimePricing.Timezone = "edited"
			result.TimePricing.WeekdaysOnly = false
			result.TimePricing.Periods[0].StartTime = "edited"
			result.TimePricing.Periods[0].Multiplier = 9

			again := method.lookup(svc, context.Background(), 10, "model")
			require.NotNil(t, again)
			require.Equal(t, card, *again)
			require.Equal(t, []string{"model"}, card.Models)
			require.Equal(t, 1.0, *card.InputPrice)
			require.Equal(t, 1000, *card.Intervals[0].MaxTokens)
			require.Equal(t, "UTC", card.TimePricing.Timezone)
		})
	}
}

// TestResolveGroupMapping_NoGroupPolicy queries a group with no fixture
// policy reader and no pricing association. The result retains the requested
// name, reports no mapping, and carries zero configuration ID.
func TestResolveGroupMapping_NoGroupPolicy(t *testing.T) {
	ch := PricingConfig{
		ID:       1,
		Status:   StatusActive,
		GroupIDs: []int64{10},
	}
	repo := makeStandardRepo(ch, map[int64]string{10: "anthropic"})
	svc := newTestPricingConfigService(repo)

	result := svc.ResolveGroupMapping(context.Background(), 999, "claude-opus-4")
	require.Equal(t, "claude-opus-4", result.MappedModel)
	require.False(t, result.Mapped)
	require.Equal(t, int64(0), result.PricingConfigID)
}

// TestResolveGroupMapping_ExactMapping supplies a group policy with one exact
// rewrite and an active pricing association. The result reports mapping to
// its configured target and the associated configuration ID.
func TestResolveGroupMapping_ExactMapping(t *testing.T) {
	routingPolicy := GroupRoutingPolicy{ModelMapping: map[string]string{"claude-sonnet-4": "claude-sonnet-4-20250514"}}

	ch := PricingConfig{
		ID:       1,
		Status:   StatusActive,
		GroupIDs: []int64{10},
	}
	repo := makePolicyRepo(routingPolicy, ch, map[int64]string{10: "anthropic"})
	svc := newTestPricingConfigService(repo)

	result := svc.ResolveGroupMapping(context.Background(), 10, "claude-sonnet-4")
	require.True(t, result.Mapped)
	require.Equal(t, "claude-sonnet-4-20250514", result.MappedModel)
	require.Equal(t, int64(1), result.PricingConfigID)
}

// TestResolveGroupMapping_WildcardMapping supplies a catch-all source pattern
// in the fixture group policy. An arbitrary request name maps to the supplied
// target and reports mapping applied.
func TestResolveGroupMapping_WildcardMapping(t *testing.T) {
	routingPolicy := GroupRoutingPolicy{ModelMapping: map[string]string{"*": "gpt-5.4"}}

	ch := PricingConfig{
		ID:       1,
		Status:   StatusActive,
		GroupIDs: []int64{10},
	}
	repo := makePolicyRepo(routingPolicy, ch, map[int64]string{10: "anthropic"})
	svc := newTestPricingConfigService(repo)

	result := svc.ResolveGroupMapping(context.Background(), 10, "any-model-name")
	require.True(t, result.Mapped)
	require.Equal(t, "gpt-5.4", result.MappedModel)
}

// TestResolveGroupMapping_WildcardFirstMatch supplies two overlapping group
// mapping patterns directly through the fixture reader. It checks that
// mapping occurs and accepts either configured target, leaving precedence
// unasserted.
func TestResolveGroupMapping_WildcardFirstMatch(t *testing.T) {
	routingPolicy := GroupRoutingPolicy{ModelMapping: map[string]string{"claude-*": "target2", "claude-sonnet-*": "target1"}}

	ch := PricingConfig{
		ID:       1,
		Status:   StatusActive,
		GroupIDs: []int64{10},
	}
	repo := makePolicyRepo(routingPolicy, ch, map[int64]string{10: "anthropic"})
	svc := newTestPricingConfigService(repo)

	result := svc.ResolveGroupMapping(context.Background(), 10, "claude-sonnet-4")
	require.True(t, result.Mapped)
	require.Contains(t, []string{"target1", "target2"}, result.MappedModel)
}

// TestResolveGroupMapping_NoMapping requests a name outside the supplied
// policy's exact rewrite. The result retains the request, reports no mapping,
// and still carries its pricing association ID.
func TestResolveGroupMapping_NoMapping(t *testing.T) {
	routingPolicy := GroupRoutingPolicy{ModelMapping: map[string]string{"claude-sonnet-4": "mapped"}}

	ch := PricingConfig{
		ID:       1,
		Status:   StatusActive,
		GroupIDs: []int64{10},
	}
	repo := makePolicyRepo(routingPolicy, ch, map[int64]string{10: "anthropic"})
	svc := newTestPricingConfigService(repo)

	result := svc.ResolveGroupMapping(context.Background(), 10, "claude-opus-4")
	require.False(t, result.Mapped)
	require.Equal(t, "claude-opus-4", result.MappedModel)
	require.Equal(t, int64(1), result.PricingConfigID)
}

// TestResolveGroupMapping_DefaultBillingModelSource reads a configuration
// with an empty stored billing source. The mapping result selects
// group_mapped metadata for its associated group.
func TestResolveGroupMapping_DefaultBillingModelSource(t *testing.T) {
	ch := PricingConfig{
		ID:                 1,
		Status:             StatusActive,
		GroupIDs:           []int64{10},
		BillingModelSource: "", // empty
	}
	repo := makeStandardRepo(ch, map[int64]string{10: "anthropic"})
	svc := newTestPricingConfigService(repo)

	result := svc.ResolveGroupMapping(context.Background(), 10, "claude-opus-4")
	require.Equal(t, BillingModelSourceGroupMapped, result.BillingModelSource)
}

// TestResolveGroupMapping_UpstreamBillingModelSource supplies upstream as an
// active configuration's billing source. The group's mapping result retains
// that billing metadata independently of a group rewrite.
func TestResolveGroupMapping_UpstreamBillingModelSource(t *testing.T) {
	ch := PricingConfig{
		ID:                 1,
		Status:             StatusActive,
		GroupIDs:           []int64{10},
		BillingModelSource: BillingModelSourceUpstream,
	}
	repo := makeStandardRepo(ch, map[int64]string{10: "anthropic"})
	svc := newTestPricingConfigService(repo)

	result := svc.ResolveGroupMapping(context.Background(), 10, "claude-opus-4")
	require.Equal(t, BillingModelSourceUpstream, result.BillingModelSource)
}

// TestResolveGroupMapping_DisabledGroupPolicy uses makePolicyRepo, which
// derives a disabled fixture policy from the supplied configuration status.
// The request stays unmapped and its inactive pricing association produces
// zero configuration ID.
func TestResolveGroupMapping_DisabledGroupPolicy(t *testing.T) {
	routingPolicy := GroupRoutingPolicy{ModelMapping: map[string]string{"claude-sonnet-4": "mapped"}}

	ch := PricingConfig{
		ID:       1,
		Status:   StatusDisabled,
		GroupIDs: []int64{10},
	}
	repo := makePolicyRepo(routingPolicy, ch, map[int64]string{10: "anthropic"})
	svc := newTestPricingConfigService(repo)

	result := svc.ResolveGroupMapping(context.Background(), 10, "claude-sonnet-4")
	require.False(t, result.Mapped)
	require.Equal(t, "claude-sonnet-4", result.MappedModel)
	require.Equal(t, int64(0), result.PricingConfigID) // No associated pricing configuration.
}

// TestIsModelRestricted_NoPricingConfig queries a group absent from
// makePolicyRepo's configured membership list. Its missing policy view allows
// the supplied model despite a restriction on the fixture's associated group.
func TestIsModelRestricted_NoPricingConfig(t *testing.T) {
	routingPolicy := GroupRoutingPolicy{RestrictModels: true}

	ch := PricingConfig{
		ID:       1,
		Status:   StatusActive,
		GroupIDs: []int64{10},
	}
	repo := makePolicyRepo(routingPolicy, ch, map[int64]string{10: "anthropic"})
	svc := newTestPricingConfigService(repo)

	restricted := svc.IsModelRestricted(context.Background(), 999, "claude-opus-4")
	require.False(t, restricted)
}

// TestIsModelRestricted_RestrictDisabled supplies a fixture policy whose
// model restriction switch is false. A model absent from the derived
// allowlist remains permitted.
func TestIsModelRestricted_RestrictDisabled(t *testing.T) {
	routingPolicy := GroupRoutingPolicy{RestrictModels: false}

	ch := PricingConfig{
		ID:       1,
		Status:   StatusActive,
		GroupIDs: []int64{10},

		ModelPricing: []ModelPricingEntry{
			{Models: []string{"claude-opus-4"}},
		},
	}
	repo := makePolicyRepo(routingPolicy, ch, map[int64]string{10: "anthropic"})
	svc := newTestPricingConfigService(repo)

	// makePolicyRepo supplies the restriction switch independently of its
	// derived card-name allowlist. Disabling that switch makes the unknown
	// request permissible.
	restricted := svc.IsModelRestricted(context.Background(), 10, "nonexistent-model")
	require.False(t, restricted)
}

// TestIsModelRestricted_InactivePricingConfig uses makePolicyRepo to derive a
// disabled policy from a disabled row. The resulting zero policy allows the
// queried model.
func TestIsModelRestricted_InactivePricingConfig(t *testing.T) {
	routingPolicy := GroupRoutingPolicy{RestrictModels: true}

	ch := PricingConfig{
		ID:       1,
		Status:   StatusDisabled,
		GroupIDs: []int64{10},
	}
	repo := makePolicyRepo(routingPolicy, ch, map[int64]string{10: "anthropic"})
	svc := newTestPricingConfigService(repo)

	restricted := svc.IsModelRestricted(context.Background(), 10, "any-model")
	require.False(t, restricted)
}

// TestIsModelRestricted_ModelInPricing uses makePolicyRepo to copy price-card
// names into a fixture allowlist. A configured exact model name is permitted
// by that supplied policy.
func TestIsModelRestricted_ModelInPricing(t *testing.T) {
	routingPolicy := GroupRoutingPolicy{RestrictModels: true}

	ch := PricingConfig{
		ID:       1,
		Status:   StatusActive,
		GroupIDs: []int64{10},

		ModelPricing: []ModelPricingEntry{
			{Models: []string{"claude-opus-4", "claude-sonnet-4"}},
		},
	}
	repo := makePolicyRepo(routingPolicy, ch, map[int64]string{10: "anthropic"})
	svc := newTestPricingConfigService(repo)

	restricted := svc.IsModelRestricted(context.Background(), 10, "claude-opus-4")
	require.False(t, restricted)
}

// TestIsModelRestricted_QoderBlankPricingIsAllowlist derives fixture
// permissions from blank and explicitly free price-card names. Both names are
// admitted because the reader supplies them in its allowlist.
func TestIsModelRestricted_QoderBlankPricingIsAllowlist(t *testing.T) {
	routingPolicy := GroupRoutingPolicy{RestrictModels: true}

	zero := 0.0
	ch := PricingConfig{
		ID:       1,
		Status:   StatusActive,
		GroupIDs: []int64{10},

		ModelPricing: []ModelPricingEntry{
			{Models: []string{"qmodel"}, BillingMode: BillingModeToken},
			{Models: []string{"free-model"}, BillingMode: BillingModeToken, InputPrice: &zero},
		},
	}
	repo := makePolicyRepo(routingPolicy, ch, map[int64]string{10: PlatformQoder})
	svc := newTestPricingConfigService(repo)

	require.False(t, svc.IsModelRestricted(context.Background(), 10, "qmodel"))
	require.False(t, svc.IsModelRestricted(context.Background(), 10, "free-model"))
}

// TestIsModelRestricted_QoderBlankWildcardDoesNotMaskEffectiveWildcard copies
// two wildcard names into the fixture policy, one from a blank card. Models
// matching each supplied pattern remain permitted by that allowlist.
func TestIsModelRestricted_QoderBlankWildcardDoesNotMaskEffectiveWildcard(t *testing.T) {
	routingPolicy := GroupRoutingPolicy{RestrictModels: true}

	price := 1e-6
	ch := PricingConfig{
		ID:       1,
		Status:   StatusActive,
		GroupIDs: []int64{10},

		ModelPricing: []ModelPricingEntry{
			{Models: []string{"qwen3.*"}, BillingMode: BillingModeToken},
			{Models: []string{"qwen3.7-*"}, BillingMode: BillingModeToken, InputPrice: &price},
		},
	}
	repo := makePolicyRepo(routingPolicy, ch, map[int64]string{10: PlatformQoder})
	svc := newTestPricingConfigService(repo)

	require.False(t, svc.IsModelRestricted(context.Background(), 10, "qwen3.7-plus"))
	require.False(t, svc.IsModelRestricted(context.Background(), 10, "qwen3.6-plus"))
}

// TestIsModelRestricted_ModelInWildcard derives a trailing-wildcard allowlist
// from the supplied fixture card. The matching Claude request is permitted by
// the resulting group policy.
func TestIsModelRestricted_ModelInWildcard(t *testing.T) {
	routingPolicy := GroupRoutingPolicy{RestrictModels: true}

	ch := PricingConfig{
		ID:       1,
		Status:   StatusActive,
		GroupIDs: []int64{10},

		ModelPricing: []ModelPricingEntry{
			{Models: []string{"claude-*"}},
		},
	}
	repo := makePolicyRepo(routingPolicy, ch, map[int64]string{10: "anthropic"})
	svc := newTestPricingConfigService(repo)

	restricted := svc.IsModelRestricted(context.Background(), 10, "claude-sonnet-4")
	require.False(t, restricted)
}

// TestIsModelRestricted_ModelNotFound derives one exact allowed name from the
// fixture card and queries another family. The supplied enabled group policy
// rejects that unmatched model.
func TestIsModelRestricted_ModelNotFound(t *testing.T) {
	routingPolicy := GroupRoutingPolicy{RestrictModels: true}

	ch := PricingConfig{
		ID:       1,
		Status:   StatusActive,
		GroupIDs: []int64{10},

		ModelPricing: []ModelPricingEntry{
			{Models: []string{"claude-opus-4"}},
		},
	}
	repo := makePolicyRepo(routingPolicy, ch, map[int64]string{10: "anthropic"})
	svc := newTestPricingConfigService(repo)

	restricted := svc.IsModelRestricted(context.Background(), 10, "gpt-5.1")
	require.True(t, restricted)
}

// TestIsModelRestricted_CaseInsensitive derives a lowercase exact allowlist
// entry from the fixture card. A mixed-case request matches that entry and
// remains permitted.
func TestIsModelRestricted_CaseInsensitive(t *testing.T) {
	routingPolicy := GroupRoutingPolicy{RestrictModels: true}

	ch := PricingConfig{
		ID:       1,
		Status:   StatusActive,
		GroupIDs: []int64{10},

		ModelPricing: []ModelPricingEntry{
			{Models: []string{"claude-opus-4"}},
		},
	}
	repo := makePolicyRepo(routingPolicy, ch, map[int64]string{10: "anthropic"})
	svc := newTestPricingConfigService(repo)

	restricted := svc.IsModelRestricted(context.Background(), 10, "Claude-Opus-4")
	require.False(t, restricted)
}

// TestResolveGroupMapping_WithPolicy supplies an enabled restricted fixture
// policy and an exact group rewrite. The mapping result reports restriction
// and the expected mapped target.
func TestResolveGroupMapping_WithPolicy(t *testing.T) {
	routingPolicy := GroupRoutingPolicy{ModelMapping: map[string]string{"claude-sonnet-4": "claude-sonnet-4-20250514"}, RestrictModels: true}

	ch := PricingConfig{
		ID:       1,
		Status:   StatusActive,
		GroupIDs: []int64{10},

		ModelPricing: []ModelPricingEntry{
			{Models: []string{"claude-sonnet-4"}},
		},
	}
	repo := makePolicyRepo(routingPolicy, ch, map[int64]string{10: "anthropic"})
	svc := newTestPricingConfigService(repo)

	gid := int64(10)
	mapping := svc.ResolveGroupMapping(context.Background(), gid, "claude-sonnet-4")
	require.True(t, mapping.RestrictModels)
	require.True(t, mapping.Mapped)
	require.Equal(t, "claude-sonnet-4-20250514", mapping.MappedModel)
}

// TestResolveGroupMapping_UnmappedPolicy supplies a restricted fixture policy
// with no model rewrite. An unknown name stays unchanged while the result
// still reports that model restriction is enabled.
func TestResolveGroupMapping_UnmappedPolicy(t *testing.T) {
	routingPolicy := GroupRoutingPolicy{RestrictModels: true}

	ch := PricingConfig{
		ID:       1,
		Status:   StatusActive,
		GroupIDs: []int64{10},

		ModelPricing: []ModelPricingEntry{
			{Models: []string{"claude-sonnet-4"}},
		},
	}
	repo := makePolicyRepo(routingPolicy, ch, map[int64]string{10: "anthropic"})
	svc := newTestPricingConfigService(repo)

	gid := int64(10)
	mapping := svc.ResolveGroupMapping(context.Background(), gid, "unknown-model")
	require.True(t, mapping.RestrictModels)
	require.False(t, mapping.Mapped)
	require.Equal(t, "unknown-model", mapping.MappedModel)
}

// TestBuildCache_DBError makes the first snapshot query fail and counts
// repository reads. The next immediate lookup reuses the empty error
// snapshot, returning nil without error and without another database call.
func TestBuildCache_DBError(t *testing.T) {
	callCount := 0
	repo := &mockPricingConfigRepository{
		listAllFn: func(_ context.Context) ([]PricingConfig, error) {
			callCount++
			return nil, errors.New("database down")
		},
	}
	svc := newTestPricingConfigService(repo)

	// The initiating lookup sees the storage failure while the service publishes
	// its short-lived empty error snapshot for subsequent calls.
	_, err := svc.GetPricingConfigForGroup(context.Background(), 10)
	require.Error(t, err)
	require.Contains(t, err.Error(), "database down")
	require.Equal(t, 1, callCount)

	// Read again immediately so the empty error snapshot is still eligible;
	// that cache hit should supply no configuration and avoid another storage
	// attempt.
	result, err := svc.GetPricingConfigForGroup(context.Background(), 10)
	require.NoError(t, err)
	require.Nil(t, result)
	require.Equal(t, 1, callCount)
}

// TestBuildCacheDoesNotReadRetiredGroupPlatform configures a platform callback
// to return an error and supplies valid configuration rows. Two association
// lookups return nonnil configurations without surfacing that error.
func TestBuildCacheDoesNotReadRetiredGroupPlatform(t *testing.T) {
	ch := PricingConfig{
		ID:       1,
		Status:   StatusActive,
		GroupIDs: []int64{10},
		ModelPricing: []ModelPricingEntry{
			{ID: 100, Models: []string{"claude-opus-4"}},
		},
	}
	repo := &mockPricingConfigRepository{
		listAllFn: func(_ context.Context) ([]PricingConfig, error) {
			return []PricingConfig{ch}, nil
		},
		getGroupPlatformsFn: func(_ context.Context, _ []int64) (map[int64]string, error) {
			return nil, errors.New("group platforms failed")
		},
	}
	svc := newTestPricingConfigService(repo)

	result, err := svc.GetPricingConfigForGroup(context.Background(), 10)
	require.NoError(t, err)
	require.NotNil(t, result)

	result2, err2 := svc.GetPricingConfigForGroup(context.Background(), 10)
	require.NoError(t, err2)
	require.NotNil(t, result2)
}

// TestBuildCache_MultipleGroupsSamePricingConfig associates three groups with
// the same priced row. Each model lookup returns the same configured card ID
// through its group association.
func TestBuildCache_MultipleGroupsSamePricingConfig(t *testing.T) {
	ch := PricingConfig{
		ID:       1,
		Status:   StatusActive,
		GroupIDs: []int64{10, 20, 30},
		ModelPricing: []ModelPricingEntry{
			{ID: 100, Models: []string{"claude-opus-4"}, InputPrice: testPtrFloat64(15e-6)},
		},
	}
	repo := makeStandardRepo(ch, map[int64]string{
		10: "anthropic",
		20: "anthropic",
		30: "anthropic",
	})
	svc := newTestPricingConfigService(repo)

	for _, gid := range []int64{10, 20, 30} {
		result := svc.GetConfigModelPricing(context.Background(), gid, "claude-opus-4")
		require.NotNil(t, result, "group %d should have pricing", gid)
		require.Equal(t, int64(100), result.ID)
	}
}

// TestBuildCache_UnifiedModelSpace associates two differently labeled fixture
// groups with shared Claude and GPT cards. Every group-family lookup returns
// a configured card.
func TestBuildCache_UnifiedModelSpace(t *testing.T) {
	ch := PricingConfig{
		ID:       1,
		Status:   StatusActive,
		GroupIDs: []int64{10, 20},
		ModelPricing: []ModelPricingEntry{
			{ID: 100, Models: []string{"claude-opus-4"}},
			{ID: 200, Models: []string{"gpt-5.1"}},
		},
	}
	repo := makeStandardRepo(ch, map[int64]string{
		10: "anthropic",
		20: "openai",
	})
	svc := newTestPricingConfigService(repo)

	require.NotNil(t, svc.GetConfigModelPricing(context.Background(), 10, "claude-opus-4"))
	require.NotNil(t, svc.GetConfigModelPricing(context.Background(), 10, "gpt-5.1"))

	require.NotNil(t, svc.GetConfigModelPricing(context.Background(), 20, "gpt-5.1"))
	require.NotNil(t, svc.GetConfigModelPricing(context.Background(), 20, "claude-opus-4"))
}

// TestBuildCache_WildcardPreservesConfigOrder seeds three overlapping
// wildcard cards in increasing prefix length. Queries matching different
// subsets still select the earliest configured card rather than a later
// narrower prefix.
func TestBuildCache_WildcardPreservesConfigOrder(t *testing.T) {
	ch := PricingConfig{
		ID:       1,
		Status:   StatusActive,
		GroupIDs: []int64{10},
		ModelPricing: []ModelPricingEntry{
			// Seed overlapping wildcard rows directly so this lookup can exercise
			// supplied-order resolution even though write validation would reject
			// the intersecting model ranges.
			{ID: 100, Models: []string{"c-*"}, InputPrice: testPtrFloat64(1e-6)},
			{ID: 200, Models: []string{"c-son-*"}, InputPrice: testPtrFloat64(2e-6)},
			{ID: 300, Models: []string{"c-son-4-*"}, InputPrice: testPtrFloat64(3e-6)},
		},
	}
	repo := makeStandardRepo(ch, map[int64]string{10: "anthropic"})
	svc := newTestPricingConfigService(repo)

	// "c-son-4-xxx" matches all three wildcards, but "c-*" (ID=100) is first in config
	result := svc.GetConfigModelPricing(context.Background(), 10, "c-son-4-xxx")
	require.NotNil(t, result)
	require.Equal(t, int64(100), result.ID)

	// "c-son-yyy" matches "c-*" and "c-son-*", but "c-*" (ID=100) is first
	result = svc.GetConfigModelPricing(context.Background(), 10, "c-son-yyy")
	require.NotNil(t, result)
	require.Equal(t, int64(100), result.ID)

	// "c-other" only matches "c-*" (ID=100)
	result = svc.GetConfigModelPricing(context.Background(), 10, "c-other")
	require.NotNil(t, result)
	require.Equal(t, int64(100), result.ID)
}

// TestInvalidateCache counts snapshot reads before and after direct
// invalidation. Repeated lookups initially reuse one load, while invalidation
// and the following lookup leave the total at two.
func TestInvalidateCache(t *testing.T) {
	callCount := 0
	ch := PricingConfig{
		ID:       1,
		Status:   StatusActive,
		GroupIDs: []int64{10},
		ModelPricing: []ModelPricingEntry{
			{ID: 100, Models: []string{"claude-opus-4"}},
		},
	}
	repo := &mockPricingConfigRepository{
		listAllFn: func(_ context.Context) ([]PricingConfig, error) {
			callCount++
			return []PricingConfig{ch}, nil
		},
		getGroupPlatformsFn: func(_ context.Context, _ []int64) (map[int64]string, error) {
			return map[int64]string{10: "anthropic"}, nil
		},
	}
	svc := newTestPricingConfigService(repo)

	result := svc.GetConfigModelPricing(context.Background(), 10, "claude-opus-4")
	require.NotNil(t, result)
	require.Equal(t, 1, callCount)

	result = svc.GetConfigModelPricing(context.Background(), 10, "claude-opus-4")
	require.NotNil(t, result)
	require.Equal(t, 1, callCount) // no new DB call

	// Rebuild after clearing the published snapshot so the following lookup can
	// verify immediate refresh through its repository count rather than waiting
	// for normal expiry.
	svc.invalidateCache()

	result = svc.GetConfigModelPricing(context.Background(), 10, "claude-opus-4")
	require.NotNil(t, result)
	require.Equal(t, 2, callCount) // rebuilt
}

// TestCreate_Success supplies successful validation, creation, and reload
// callbacks, with creation assigning ID forty-two. The service returns a
// nonnil reloaded configuration carrying that assigned ID.
func TestCreate_Success(t *testing.T) {
	createdID := int64(42)
	repo := &mockPricingConfigRepository{
		existsByNameFn: func(_ context.Context, _ string) (bool, error) {
			return false, nil
		},
		getGroupsInOtherPricingConfigsFn: func(_ context.Context, _ int64, _ []int64) ([]int64, error) {
			return nil, nil
		},
		createFn: func(_ context.Context, ch *PricingConfig) error {
			ch.ID = createdID
			return nil
		},
		getByIDFn: func(_ context.Context, id int64) (*PricingConfig, error) {
			return &PricingConfig{ID: id, Name: "new-channel", Status: StatusActive}, nil
		},
		listAllFn: func(_ context.Context) ([]PricingConfig, error) {
			return nil, nil
		},
	}
	svc := newTestPricingConfigService(repo)

	result, err := svc.Create(context.Background(), &CreatePricingConfigInput{
		Name:     "new-channel",
		GroupIDs: []int64{10},
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, createdID, result.ID)
}

// TestCreate_NameExists makes the name-existence callback report a conflict
// for the supplied creation name. The service returns ErrPricingConfigExists
// before proceeding to repository creation.
func TestCreate_NameExists(t *testing.T) {
	repo := &mockPricingConfigRepository{
		existsByNameFn: func(_ context.Context, _ string) (bool, error) {
			return true, nil
		},
	}
	svc := newTestPricingConfigService(repo)

	_, err := svc.Create(context.Background(), &CreatePricingConfigInput{
		Name: "existing-channel",
	})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrPricingConfigExists)
}

// TestCreate_GroupConflict reports group ten already associated elsewhere
// when creation requests groups ten and twenty. The service returns
// ErrGroupAlreadyInPricingConfig for that supplied membership conflict.
func TestCreate_GroupConflict(t *testing.T) {
	repo := &mockPricingConfigRepository{
		existsByNameFn: func(_ context.Context, _ string) (bool, error) {
			return false, nil
		},
		getGroupsInOtherPricingConfigsFn: func(_ context.Context, _ int64, _ []int64) ([]int64, error) {
			return []int64{10}, nil // Group 10 already belongs to another pricing configuration.
		},
	}
	svc := newTestPricingConfigService(repo)

	_, err := svc.Create(context.Background(), &CreatePricingConfigInput{
		Name:     "new-channel",
		GroupIDs: []int64{10, 20},
	})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrGroupAlreadyInPricingConfig)
}

// TestCreate_DuplicateModel supplies duplicate exact model names in separate
// cards during creation. The validation error contains that model name,
// identifying the conflicting input.
func TestCreate_DuplicateModel(t *testing.T) {
	repo := &mockPricingConfigRepository{
		existsByNameFn: func(_ context.Context, _ string) (bool, error) {
			return false, nil
		},
	}
	svc := newTestPricingConfigService(repo)

	_, err := svc.Create(context.Background(), &CreatePricingConfigInput{
		Name: "new-channel",
		ModelPricing: []ModelPricingEntry{
			{Models: []string{"claude-opus-4"}},
			{Models: []string{"claude-opus-4"}}, // duplicate
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "claude-opus-4")
}

// TestCreate_InvalidPricingIntervals supplies two overlapping token intervals
// for one card. Creation returns the interval reason and overlap message for
// the rejected ranges.
func TestCreate_InvalidPricingIntervals(t *testing.T) {
	repo := &mockPricingConfigRepository{
		existsByNameFn: func(_ context.Context, _ string) (bool, error) {
			return false, nil
		},
	}
	svc := newTestPricingConfigService(repo)

	_, err := svc.Create(context.Background(), &CreatePricingConfigInput{
		Name: "new-channel",
		ModelPricing: []ModelPricingEntry{
			{
				Models: []string{"claude-opus-4"},
				Intervals: []PricingInterval{
					{MinTokens: 0, MaxTokens: testPtrInt(2000), InputPrice: testPtrFloat64(1e-6)},
					{MinTokens: 1000, MaxTokens: testPtrInt(3000), InputPrice: testPtrFloat64(2e-6)},
				},
			},
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "INVALID_PRICING_INTERVALS")
	require.Contains(t, err.Error(), "overlap")
}

// TestCreate_DefaultBillingModelSource omits billing source and captures the
// configuration passed to Create. The reloaded result contains group_mapped
// as the default source.
func TestCreate_DefaultBillingModelSource(t *testing.T) {
	var capturedPricingConfig *PricingConfig
	repo := &mockPricingConfigRepository{
		existsByNameFn: func(_ context.Context, _ string) (bool, error) {
			return false, nil
		},
		createFn: func(_ context.Context, ch *PricingConfig) error {
			capturedPricingConfig = ch
			ch.ID = 1
			return nil
		},
		getByIDFn: func(_ context.Context, id int64) (*PricingConfig, error) {
			return capturedPricingConfig, nil
		},
		listAllFn: func(_ context.Context) ([]PricingConfig, error) {
			return nil, nil
		},
	}
	svc := newTestPricingConfigService(repo)

	result, err := svc.Create(context.Background(), &CreatePricingConfigInput{
		Name:               "new-channel",
		BillingModelSource: "", // empty, should default to "group_mapped"
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, BillingModelSourceGroupMapped, result.BillingModelSource)
}

// TestCreate_InvalidatesCache primes one snapshot read before successful
// creation. Creation refreshes the snapshot, and the next model lookup leaves
// the observed load count at two.
func TestCreate_InvalidatesCache(t *testing.T) {
	loadCount := 0
	ch := PricingConfig{
		ID:       1,
		Status:   StatusActive,
		GroupIDs: []int64{10},
		ModelPricing: []ModelPricingEntry{
			{ID: 100, Models: []string{"claude-opus-4"}},
		},
	}
	repo := &mockPricingConfigRepository{
		listAllFn: func(_ context.Context) ([]PricingConfig, error) {
			loadCount++
			return []PricingConfig{ch}, nil
		},
		getGroupPlatformsFn: func(_ context.Context, _ []int64) (map[int64]string, error) {
			return map[int64]string{10: "anthropic"}, nil
		},
		existsByNameFn: func(_ context.Context, _ string) (bool, error) {
			return false, nil
		},
		createFn: func(_ context.Context, c *PricingConfig) error {
			c.ID = 2
			return nil
		},
		getByIDFn: func(_ context.Context, id int64) (*PricingConfig, error) {
			return &PricingConfig{ID: id, Name: "new", Status: StatusActive}, nil
		},
	}
	svc := newTestPricingConfigService(repo)

	_ = svc.GetConfigModelPricing(context.Background(), 10, "claude-opus-4")
	require.Equal(t, 1, loadCount)

	_, err := svc.Create(context.Background(), &CreatePricingConfigInput{Name: "new"})
	require.NoError(t, err)

	_ = svc.GetConfigModelPricing(context.Background(), 10, "claude-opus-4")
	require.Equal(t, 2, loadCount)
}

// TestUpdate_Success supplies successful update and reload callbacks while
// requesting name and description changes. It checks successful completion
// and a nonnil result; reload returns the fixture's unchanged stored value.
func TestUpdate_Success(t *testing.T) {
	existing := &PricingConfig{
		ID:     1,
		Name:   "original",
		Status: StatusActive,
	}
	repo := &mockPricingConfigRepository{
		getByIDFn: func(_ context.Context, id int64) (*PricingConfig, error) {
			return existing.Clone(), nil
		},
		updateFn: func(_ context.Context, _ *PricingConfig) error {
			return nil
		},
		getGroupIDsFn: func(_ context.Context, _ int64) ([]int64, error) {
			return nil, nil
		},
		listAllFn: func(_ context.Context) ([]PricingConfig, error) {
			return nil, nil
		},
	}
	svc := newTestPricingConfigService(repo)

	result, err := svc.Update(context.Background(), 1, &UpdatePricingConfigInput{
		Name:        "updated-name",
		Description: testPtrString("new desc"),
	})
	require.NoError(t, err)
	require.NotNil(t, result)
}

// TestUpdate_NotFound makes the initial configuration reader return
// ErrPricingConfigNotFound. The update exposes that sentinel through
// errors.Is for the absent requested ID.
func TestUpdate_NotFound(t *testing.T) {
	repo := &mockPricingConfigRepository{
		getByIDFn: func(_ context.Context, _ int64) (*PricingConfig, error) {
			return nil, ErrPricingConfigNotFound
		},
	}
	svc := newTestPricingConfigService(repo)

	_, err := svc.Update(context.Background(), 999, &UpdatePricingConfigInput{
		Name: "whatever",
	})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrPricingConfigNotFound)
}

// TestUpdate_NameConflict supplies a different requested name whose
// excluding-ID existence callback reports a conflict. The service returns
// ErrPricingConfigExists for that update.
func TestUpdate_NameConflict(t *testing.T) {
	existing := &PricingConfig{
		ID:     1,
		Name:   "original",
		Status: StatusActive,
	}
	repo := &mockPricingConfigRepository{
		getByIDFn: func(_ context.Context, _ int64) (*PricingConfig, error) {
			return existing.Clone(), nil
		},
		existsByNameExcludingFn: func(_ context.Context, _ string, _ int64) (bool, error) {
			return true, nil // Name conflicts with another pricing configuration.
		},
	}
	svc := newTestPricingConfigService(repo)

	_, err := svc.Update(context.Background(), 1, &UpdatePricingConfigInput{
		Name: "conflicting-name",
	})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrPricingConfigExists)
}

// TestUpdate_GroupConflict reports a requested replacement group already
// associated elsewhere. The service returns ErrGroupAlreadyInPricingConfig
// before applying that conflicting membership change to persistence.
func TestUpdate_GroupConflict(t *testing.T) {
	existing := &PricingConfig{
		ID:     1,
		Name:   "original",
		Status: StatusActive,
	}
	repo := &mockPricingConfigRepository{
		getByIDFn: func(_ context.Context, _ int64) (*PricingConfig, error) {
			return existing.Clone(), nil
		},
		getGroupsInOtherPricingConfigsFn: func(_ context.Context, _ int64, _ []int64) ([]int64, error) {
			return []int64{20}, nil // Group 20 belongs to another pricing configuration.
		},
	}
	svc := newTestPricingConfigService(repo)

	newGroupIDs := []int64{10, 20}
	_, err := svc.Update(context.Background(), 1, &UpdatePricingConfigInput{
		GroupIDs: &newGroupIDs,
	})
	require.Error(t, err)
	require.ErrorIs(t, err, ErrGroupAlreadyInPricingConfig)
}

// TestUpdate_DuplicateModel supplies two replacement cards with the same
// exact model name. The update returns a validation error containing that
// duplicated name.
func TestUpdate_DuplicateModel(t *testing.T) {
	existing := &PricingConfig{
		ID:     1,
		Name:   "original",
		Status: StatusActive,
	}
	repo := &mockPricingConfigRepository{
		getByIDFn: func(_ context.Context, _ int64) (*PricingConfig, error) {
			return existing.Clone(), nil
		},
	}
	svc := newTestPricingConfigService(repo)

	dupPricing := []ModelPricingEntry{
		{Models: []string{"claude-opus-4"}},
		{Models: []string{"claude-opus-4"}},
	}
	_, err := svc.Update(context.Background(), 1, &UpdatePricingConfigInput{
		ModelPricing: &dupPricing,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "claude-opus-4")
}

// TestUpdate_InvalidPricingIntervals supplies an unbounded interval followed
// by another token range. The update reports invalid intervals and identifies
// the unbounded-range ordering violation.
func TestUpdate_InvalidPricingIntervals(t *testing.T) {
	existing := &PricingConfig{
		ID:     1,
		Name:   "original",
		Status: StatusActive,
	}
	repo := &mockPricingConfigRepository{
		getByIDFn: func(_ context.Context, _ int64) (*PricingConfig, error) {
			return existing.Clone(), nil
		},
	}
	svc := newTestPricingConfigService(repo)

	invalidPricing := []ModelPricingEntry{
		{
			Models: []string{"claude-opus-4"},
			Intervals: []PricingInterval{
				{MinTokens: 0, MaxTokens: nil, InputPrice: testPtrFloat64(1e-6)},
				{MinTokens: 2000, MaxTokens: testPtrInt(4000), InputPrice: testPtrFloat64(2e-6)},
			},
		},
	}
	_, err := svc.Update(context.Background(), 1, &UpdatePricingConfigInput{
		ModelPricing: &invalidPricing,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "INVALID_PRICING_INTERVALS")
	require.Contains(t, err.Error(), "unbounded")
}

// TestUpdate_InvalidatesPricingConfigCache primes a snapshot and then submits
// a successful description update request through fixture callbacks. The
// subsequent lookup leaves the snapshot-load count at two after update
// refresh.
func TestUpdate_InvalidatesPricingConfigCache(t *testing.T) {
	existing := &PricingConfig{
		ID:     1,
		Name:   "original",
		Status: StatusActive,
	}
	loadCount := 0
	repo := &mockPricingConfigRepository{
		getByIDFn: func(_ context.Context, _ int64) (*PricingConfig, error) {
			return existing.Clone(), nil
		},
		updateFn: func(_ context.Context, _ *PricingConfig) error {
			return nil
		},
		getGroupIDsFn: func(_ context.Context, _ int64) ([]int64, error) {
			return []int64{10, 20}, nil
		},
		listAllFn: func(_ context.Context) ([]PricingConfig, error) {
			loadCount++
			return []PricingConfig{*existing}, nil
		},
		getGroupPlatformsFn: func(_ context.Context, _ []int64) (map[int64]string, error) {
			return nil, nil
		},
	}
	svc := newTestPricingConfigService(repo)

	_, _ = svc.GetPricingConfigForGroup(context.Background(), 10)
	require.Equal(t, 1, loadCount)

	result, err := svc.Update(context.Background(), 1, &UpdatePricingConfigInput{
		Description: testPtrString("updated"),
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	_, _ = svc.GetPricingConfigForGroup(context.Background(), 10)
	require.Equal(t, 2, loadCount)
}

// TestUpdate_InvalidatesAuthCache supplies two old group associations and a
// successful description update. Both group IDs appear in the recorded
// authentication invalidation requests.
func TestUpdate_InvalidatesAuthCache(t *testing.T) {
	existing := &PricingConfig{
		ID:     1,
		Name:   "original",
		Status: StatusActive,
	}
	auth := &mockPricingConfigAuthCacheInvalidator{}
	repo := &mockPricingConfigRepository{
		getByIDFn: func(_ context.Context, _ int64) (*PricingConfig, error) {
			return existing.Clone(), nil
		},
		updateFn: func(_ context.Context, _ *PricingConfig) error {
			return nil
		},
		getGroupIDsFn: func(_ context.Context, _ int64) ([]int64, error) {
			return []int64{10, 20}, nil
		},
		listAllFn: func(_ context.Context) ([]PricingConfig, error) {
			return nil, nil
		},
	}
	svc := newTestPricingConfigServiceWithAuth(repo, auth)

	result, err := svc.Update(context.Background(), 1, &UpdatePricingConfigInput{
		Description: testPtrString("updated"),
	})
	require.NoError(t, err)
	require.NotNil(t, result)

	require.ElementsMatch(t, []int64{10, 20}, auth.invalidatedGroupIDs)
}

// TestPricingConfigDelete_Success supplies a successful deletion callback
// that records invocation. The service returns no error and the observed
// deletion flag is set.
func TestPricingConfigDelete_Success(t *testing.T) {
	deleted := false
	repo := &mockPricingConfigRepository{
		getGroupIDsFn: func(_ context.Context, _ int64) ([]int64, error) {
			return nil, nil
		},
		deleteFn: func(_ context.Context, _ int64) error {
			deleted = true
			return nil
		},
		listAllFn: func(_ context.Context) ([]PricingConfig, error) {
			return nil, nil
		},
	}
	svc := newTestPricingConfigService(repo)

	err := svc.Delete(context.Background(), 1)
	require.NoError(t, err)
	require.True(t, deleted)
}

// TestPricingConfigDelete_InvalidatesCaches primes prices and supplies two
// associations before successful deletion. Both group invalidations are
// recorded and the following lookup observes a second snapshot load.
func TestPricingConfigDelete_InvalidatesCaches(t *testing.T) {
	auth := &mockPricingConfigAuthCacheInvalidator{}
	loadCount := 0
	repo := &mockPricingConfigRepository{
		getGroupIDsFn: func(_ context.Context, _ int64) ([]int64, error) {
			return []int64{10, 20}, nil
		},
		deleteFn: func(_ context.Context, _ int64) error {
			return nil
		},
		listAllFn: func(_ context.Context) ([]PricingConfig, error) {
			loadCount++
			return []PricingConfig{{ID: 1, Status: StatusActive, GroupIDs: []int64{10, 20}}}, nil
		},
		getGroupPlatformsFn: func(_ context.Context, _ []int64) (map[int64]string, error) {
			return nil, nil
		},
	}
	svc := newTestPricingConfigServiceWithAuth(repo, auth)

	_, _ = svc.GetPricingConfigForGroup(context.Background(), 10)
	require.Equal(t, 1, loadCount)

	err := svc.Delete(context.Background(), 1)
	require.NoError(t, err)

	require.ElementsMatch(t, []int64{10, 20}, auth.invalidatedGroupIDs)

	_, _ = svc.GetPricingConfigForGroup(context.Background(), 10)
	require.Equal(t, 2, loadCount)
}

// TestPricingConfigDelete_NotFound injects a repository removal error for the
// requested configuration. The service returns an error containing the
// repository's not-found message.
func TestPricingConfigDelete_NotFound(t *testing.T) {
	repo := &mockPricingConfigRepository{
		getGroupIDsFn: func(_ context.Context, _ int64) ([]int64, error) {
			return nil, nil
		},
		deleteFn: func(_ context.Context, _ int64) error {
			return errors.New("record not found")
		},
	}
	svc := newTestPricingConfigService(repo)

	err := svc.Delete(context.Background(), 999)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

// TestCreate_NoGroups requests an empty membership list and supplies
// successful creation and reload callbacks. The assigned ID is returned and
// the competing-membership query remains uncalled.
func TestCreate_NoGroups(t *testing.T) {
	createdID := int64(55)
	getGroupsInOtherPricingConfigsCalled := false
	repo := &mockPricingConfigRepository{
		existsByNameFn: func(_ context.Context, _ string) (bool, error) {
			return false, nil
		},
		getGroupsInOtherPricingConfigsFn: func(_ context.Context, _ int64, _ []int64) ([]int64, error) {
			getGroupsInOtherPricingConfigsCalled = true
			return nil, nil
		},
		createFn: func(_ context.Context, ch *PricingConfig) error {
			ch.ID = createdID
			return nil
		},
		getByIDFn: func(_ context.Context, id int64) (*PricingConfig, error) {
			return &PricingConfig{ID: id, Name: "no-groups-channel", Status: StatusActive}, nil
		},
		listAllFn: func(_ context.Context) ([]PricingConfig, error) {
			return nil, nil
		},
	}
	svc := newTestPricingConfigService(repo)

	result, err := svc.Create(context.Background(), &CreatePricingConfigInput{
		Name:     "no-groups-channel",
		GroupIDs: []int64{}, // empty slice
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, createdID, result.ID)
	// An empty membership request has no IDs to conflict with another saved
	// association, so creation should skip the competing-membership reader.
	require.False(t, getGroupsInOtherPricingConfigsCalled)
}

// TestUpdate_StatusOnly captures the write argument while requesting a
// disabled status. That argument has the disabled status and retains the
// existing name, even though reload uses the unchanged fixture.
func TestUpdate_StatusOnly(t *testing.T) {
	existing := &PricingConfig{
		ID:     1,
		Name:   "test-price-config",
		Status: StatusActive,
	}
	var capturedPricingConfig *PricingConfig
	repo := &mockPricingConfigRepository{
		getByIDFn: func(_ context.Context, id int64) (*PricingConfig, error) {
			return existing.Clone(), nil
		},
		updateFn: func(_ context.Context, ch *PricingConfig) error {
			capturedPricingConfig = ch
			return nil
		},
		getGroupIDsFn: func(_ context.Context, _ int64) ([]int64, error) {
			return nil, nil
		},
		listAllFn: func(_ context.Context) ([]PricingConfig, error) {
			return nil, nil
		},
	}
	svc := newTestPricingConfigService(repo)

	result, err := svc.Update(context.Background(), 1, &UpdatePricingConfigInput{
		Status: StatusDisabled,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotNil(t, capturedPricingConfig)
	require.Equal(t, StatusDisabled, capturedPricingConfig.Status)
	require.Equal(t, "test-price-config", capturedPricingConfig.Name)
}

// TestPricingConfigDelete_GetGroupIDsError fails membership lookup before a
// successful removal callback. The service still succeeds and the recorded
// flag shows that deletion was invoked.
func TestPricingConfigDelete_GetGroupIDsError(t *testing.T) {
	deleted := false
	repo := &mockPricingConfigRepository{
		getGroupIDsFn: func(_ context.Context, _ int64) ([]int64, error) {
			return nil, errors.New("group IDs lookup failed")
		},
		deleteFn: func(_ context.Context, _ int64) error {
			deleted = true
			return nil
		},
		listAllFn: func(_ context.Context) ([]PricingConfig, error) {
			return nil, nil
		},
	}
	svc := newTestPricingConfigService(repo)

	err := svc.Delete(context.Background(), 1)
	require.NoError(t, err)
	require.True(t, deleted)
}

// TestResolveGroupMappingUsesUnifiedModelNames supplies an exact full-name
// mapping through makePolicyRepo. The request resolves to the configured full
// target and reports mapping applied.
func TestResolveGroupMappingUsesUnifiedModelNames(t *testing.T) {
	routingPolicy := GroupRoutingPolicy{ModelMapping: map[string]string{"claude-opus-4-5": "claude-opus-4-6"}}

	ch := PricingConfig{
		ID:       1,
		Status:   StatusActive,
		GroupIDs: []int64{10},
	}
	repo := makePolicyRepo(routingPolicy, ch, map[int64]string{10: PlatformAntigravity})
	svc := newTestPricingConfigService(repo)

	result := svc.ResolveGroupMapping(context.Background(), 10, "claude-opus-4-5")
	require.True(t, result.Mapped)
	require.Equal(t, "claude-opus-4-6", result.MappedModel)
}

// TestResolveGroupMappingUsesExplicitAlias supplies a custom alias and exact
// target in the fixture group policy. The result returns that target and
// reports a successful mapping.
func TestResolveGroupMappingUsesExplicitAlias(t *testing.T) {
	routingPolicy := GroupRoutingPolicy{ModelMapping: map[string]string{"alias": "anthropic-target"}}

	ch := PricingConfig{
		ID:       1,
		Status:   StatusActive,
		GroupIDs: []int64{10},
	}
	repo := makePolicyRepo(routingPolicy, ch, map[int64]string{10: PlatformAntigravity})
	svc := newTestPricingConfigService(repo)

	result := svc.ResolveGroupMapping(context.Background(), 10, "alias")
	require.True(t, result.Mapped)
	require.Equal(t, "anthropic-target", result.MappedModel)
}

// TestCheckRestrictedUsesUnifiedAllowlist derives one fixture allowlist name
// from its shared card. The configured name is admitted and an unrelated
// request is rejected by that supplied policy.
func TestCheckRestrictedUsesUnifiedAllowlist(t *testing.T) {
	routingPolicy := GroupRoutingPolicy{RestrictModels: true}

	ch := PricingConfig{
		ID:     1,
		Status: StatusActive,

		GroupIDs: []int64{10},
		ModelPricing: []ModelPricingEntry{
			{ID: 500, Models: []string{"shared-model"}, InputPrice: testPtrFloat64(10e-6)},
		},
	}
	repo := makePolicyRepo(routingPolicy, ch, map[int64]string{10: PlatformAntigravity})
	svc := newTestPricingConfigService(repo)

	restricted := svc.IsModelRestricted(context.Background(), 10, "shared-model")
	require.False(t, restricted)

	restricted = svc.IsModelRestricted(context.Background(), 10, "unknown-model")
	require.True(t, restricted, "unknown-model should be restricted for antigravity")
}

// TestGetConfigModelPricing_AntigravityOwnPricingWorks supplies Claude and
// Gemini wildcard cards for one associated group. Each query selects its
// configured card ID and small input amount within tolerance.
func TestGetConfigModelPricing_AntigravityOwnPricingWorks(t *testing.T) {
	ch := PricingConfig{
		ID:       1,
		Status:   StatusActive,
		GroupIDs: []int64{10},
		ModelPricing: []ModelPricingEntry{
			{ID: 600, Models: []string{"claude-*"}, InputPrice: testPtrFloat64(15e-6)},
			{ID: 601, Models: []string{"gemini-*"}, InputPrice: testPtrFloat64(2e-6)},
		},
	}
	repo := makeStandardRepo(ch, map[int64]string{10: PlatformAntigravity})
	svc := newTestPricingConfigService(repo)

	result := svc.GetConfigModelPricing(context.Background(), 10, "claude-sonnet-4")
	require.NotNil(t, result)
	require.Equal(t, int64(600), result.ID)
	// The tiny absolute tolerance compares stored dollar-denominated price
	// amounts.
	require.InDelta(t, 15e-6, *result.InputPrice, 1e-12)

	result = svc.GetConfigModelPricing(context.Background(), 10, "gemini-2.5-flash")
	require.NotNil(t, result)
	require.Equal(t, int64(601), result.ID)
	// The tiny absolute tolerance compares stored dollar-denominated price
	// amounts.
	require.InDelta(t, 2e-6, *result.InputPrice, 1e-12)
}

// TestToUsageFields_NoMapping supplies unchanged requested and upstream names
// plus requested-source metadata. Usage fields retain the configuration ID,
// both model names, and source, while the mapping chain is empty.
func TestToUsageFields_NoMapping(t *testing.T) {
	r := GroupMappingResult{
		MappedModel:        "claude-opus-4",
		PricingConfigID:    1,
		Mapped:             false,
		BillingModelSource: BillingModelSourceRequested,
	}
	fields := r.ToUsageFields("claude-opus-4", "claude-opus-4")
	require.Equal(t, int64(1), fields.PricingConfigID)
	require.Equal(t, "claude-opus-4", fields.OriginalModel)
	require.Equal(t, "claude-opus-4", fields.GroupMappedModel)
	require.Equal(t, BillingModelSourceRequested, fields.BillingModelSource)
	require.Empty(t, fields.ModelMappingChain)
}

// TestToUsageFields_WithGroupMapping supplies a changed group target matching
// the upstream name. Usage fields retain the requested and group-mapped
// identities and contain a two-stage chain.
func TestToUsageFields_WithGroupMapping(t *testing.T) {
	r := GroupMappingResult{
		MappedModel:        "claude-sonnet-4-20250514",
		PricingConfigID:    2,
		Mapped:             true,
		BillingModelSource: BillingModelSourceGroupMapped,
	}
	fields := r.ToUsageFields("claude-sonnet-4", "claude-sonnet-4-20250514")
	require.Equal(t, int64(2), fields.PricingConfigID)
	require.Equal(t, "claude-sonnet-4", fields.OriginalModel)
	require.Equal(t, "claude-sonnet-4-20250514", fields.GroupMappedModel)
	require.Equal(t, "claude-sonnet-4→claude-sonnet-4-20250514", fields.ModelMappingChain)
}

// TestToUsageFields_WithUpstreamDifference supplies distinct alias, group
// target, and upstream names. Usage fields retain both requested and
// group-mapped names and expose the three-stage chain.
func TestToUsageFields_WithUpstreamDifference(t *testing.T) {
	r := GroupMappingResult{
		MappedModel:        "claude-sonnet-4",
		PricingConfigID:    3,
		Mapped:             true,
		BillingModelSource: BillingModelSourceUpstream,
	}
	fields := r.ToUsageFields("my-alias", "claude-sonnet-4-20250514")
	require.Equal(t, "my-alias", fields.OriginalModel)
	require.Equal(t, "claude-sonnet-4", fields.GroupMappedModel)
	require.Equal(t, "my-alias→claude-sonnet-4→claude-sonnet-4-20250514", fields.ModelMappingChain)
}

// TestResolveGroupMappingUnifiedWildcardsAcrossGroups supplies one fixture
// policy with Claude and Gemini wildcard rewrites to two groups. Assertions
// check both families in one group and the Claude rewrite in the other.
func TestResolveGroupMappingUnifiedWildcardsAcrossGroups(t *testing.T) {
	routingPolicy := GroupRoutingPolicy{ModelMapping: map[string]string{"claude-*": "claude-override", "gemini-*": "gemini-override"}}

	ch := PricingConfig{
		ID:       1,
		Status:   StatusActive,
		GroupIDs: []int64{10, 20},
	}
	repo := makePolicyRepo(routingPolicy, ch, map[int64]string{10: PlatformAntigravity, 20: PlatformAnthropic})
	svc := newTestPricingConfigService(repo)

	result := svc.ResolveGroupMapping(context.Background(), 10, "claude-opus-4")
	require.True(t, result.Mapped)
	require.Equal(t, "claude-override", result.MappedModel)

	result = svc.ResolveGroupMapping(context.Background(), 10, "gemini-2.5-pro")
	require.True(t, result.Mapped)
	require.Equal(t, "gemini-override", result.MappedModel)

	result = svc.ResolveGroupMapping(context.Background(), 20, "claude-opus-4")
	require.True(t, result.Mapped)
	require.Equal(t, "claude-override", result.MappedModel)
}

// TestGroupRoutingPolicyCreateMappingConflict directly validates a group
// policy containing overlapping wildcard sources. It checks the
// mapping-pattern reason returned by policy validation before any
// administrative creation is attempted.
func TestGroupRoutingPolicyCreateMappingConflict(t *testing.T) {
	err := ValidateGroupRoutingPolicy(GroupRoutingPolicy{ModelMapping: map[string]string{"claude-*": "a", "claude-opus-*": "b"}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "MAPPING_PATTERN_CONFLICT")
}

// TestGroupRoutingPolicyUpdateMappingConflict directly validates the same
// overlapping wildcard source shape through ValidateGroupRoutingPolicy. The
// assertion checks its mapping-pattern reason independently of an
// administrative update call.
func TestGroupRoutingPolicyUpdateMappingConflict(t *testing.T) {
	err := ValidateGroupRoutingPolicy(GroupRoutingPolicy{ModelMapping: map[string]string{"claude-*": "a", "claude-opus-*": "b"}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "MAPPING_PATTERN_CONFLICT")
}

// TestPricingConfigModelLookupIndependentOfGroupPlatform installs a platform
// callback that panics and shares two cards between groups. Exact and
// wildcard prices resolve for both groups without invoking that callback.
func TestPricingConfigModelLookupIndependentOfGroupPlatform(t *testing.T) {
	config := PricingConfig{ID: 1, Status: StatusActive, GroupIDs: []int64{10, 20}, ModelPricing: []ModelPricingEntry{{Models: []string{"claude-x"}, InputPrice: testPtrFloat64(3)}, {Models: []string{"gpt-*"}, InputPrice: testPtrFloat64(5)}}}
	repo := &mockPricingConfigRepository{listAllFn: func(context.Context) ([]PricingConfig, error) { return []PricingConfig{config}, nil }, getGroupPlatformsFn: func(context.Context, []int64) (map[int64]string, error) {
		panic("pricing must not read group platforms")
	}}
	service := newTestPricingConfigService(repo)
	for _, id := range []int64{10, 20} {
		require.Equal(t, 3.0, *service.GetConfigModelPricing(context.Background(), id, "claude-x").InputPrice)
		require.Equal(t, 5.0, *service.GetConfigModelPricing(context.Background(), id, "gpt-x").InputPrice)
	}
}

// Create invokes createFn with the supplied context and configuration when
// configured. A nil callback returns success while leaving identity
// assignment and stored fixture state untouched.
func (m *mockPricingConfigRepository) Create(ctx context.Context, pricingConfig *PricingConfig) error {
	if m.createFn != nil {
		return m.createFn(ctx, pricingConfig)
	}
	return nil
}

// GetByID delegates to getByIDFn when configured and forwards its result
// directly. A nil callback returns nil with ErrPricingConfigNotFound, giving
// absent fixture reads an explicit failure.
func (m *mockPricingConfigRepository) GetByID(ctx context.Context, id int64) (*PricingConfig, error) {
	if m.getByIDFn != nil {
		return m.getByIDFn(ctx, id)
	}
	return nil, ErrPricingConfigNotFound
}

// Update invokes updateFn with the supplied context and working configuration
// when configured. A nil callback succeeds and records no mutation, so tests
// install callbacks for observed writes.
func (m *mockPricingConfigRepository) Update(ctx context.Context, pricingConfig *PricingConfig) error {
	if m.updateFn != nil {
		return m.updateFn(ctx, pricingConfig)
	}
	return nil
}

// Delete invokes deleteFn with the supplied context and ID when configured. A
// nil callback returns success and leaves fixture state unchanged, so
// deletion observations require a callback.
func (m *mockPricingConfigRepository) Delete(ctx context.Context, id int64) error {
	if m.deleteFn != nil {
		return m.deleteFn(ctx, id)
	}
	return nil
}

// List forwards pagination, status, and search to listFn when configured. A
// nil callback returns nil rows and nil pagination with no error, leaving
// page metadata absent.
func (m *mockPricingConfigRepository) List(ctx context.Context, params pagination.PaginationParams, status, search string) ([]PricingConfig, *pagination.PaginationResult, error) {
	if m.listFn != nil {
		return m.listFn(ctx, params, status, search)
	}
	return nil, nil, nil
}

// ListAll returns the supplied callback's configuration rows and error
// directly. A nil listAllFn returns nil rows with no error, giving cache
// construction an empty successful input.
func (m *mockPricingConfigRepository) ListAll(ctx context.Context) ([]PricingConfig, error) {
	if m.listAllFn != nil {
		return m.listAllFn(ctx)
	}
	return nil, nil
}

// ExistsByName forwards the requested name to existsByNameFn when configured.
// A nil callback reports false with no error, allowing fixture creation to
// pass the name check.
func (m *mockPricingConfigRepository) ExistsByName(ctx context.Context, name string) (bool, error) {
	if m.existsByNameFn != nil {
		return m.existsByNameFn(ctx, name)
	}
	return false, nil
}

// ExistsByNameExcluding forwards the name and excluded ID to its callback
// when configured. A nil callback reports false with no error, allowing
// changed names through fixture validation.
func (m *mockPricingConfigRepository) ExistsByNameExcluding(ctx context.Context, name string, excludeID int64) (bool, error) {
	if m.existsByNameExcludingFn != nil {
		return m.existsByNameExcludingFn(ctx, name, excludeID)
	}
	return false, nil
}

// GetGroupIDs forwards the configuration ID to getGroupIDsFn when configured.
// A nil callback returns no IDs with no error, leaving group invalidation
// inputs empty for that fixture.
func (m *mockPricingConfigRepository) GetGroupIDs(ctx context.Context, pricingConfigID int64) ([]int64, error) {
	if m.getGroupIDsFn != nil {
		return m.getGroupIDsFn(ctx, pricingConfigID)
	}
	return nil, nil
}

// SetGroupIDs forwards configuration identity and membership storage to
// setGroupIDsFn when configured. A nil callback succeeds and records no
// membership changes; callbacks control any observed state mutation.
func (m *mockPricingConfigRepository) SetGroupIDs(ctx context.Context, pricingConfigID int64, groupIDs []int64) error {
	if m.setGroupIDsFn != nil {
		return m.setGroupIDsFn(ctx, pricingConfigID, groupIDs)
	}
	return nil
}

// GetPricingConfigIDByGroupID forwards the group ID to its callback when
// configured. A nil callback returns zero with no error, representing no
// supplied association identity in this fixture.
func (m *mockPricingConfigRepository) GetPricingConfigIDByGroupID(ctx context.Context, groupID int64) (int64, error) {
	if m.getPricingConfigIDByGroupIDFn != nil {
		return m.getPricingConfigIDByGroupIDFn(ctx, groupID)
	}
	return 0, nil
}

// GetGroupsInOtherPricingConfigs forwards configuration identity and
// requested groups to its callback when configured. A nil callback returns no
// conflicts with no error, allowing supplied memberships through validation.
func (m *mockPricingConfigRepository) GetGroupsInOtherPricingConfigs(ctx context.Context, pricingConfigID int64, groupIDs []int64) ([]int64, error) {
	if m.getGroupsInOtherPricingConfigsFn != nil {
		return m.getGroupsInOtherPricingConfigsFn(ctx, pricingConfigID, groupIDs)
	}
	return nil, nil
}

// GetGroupPlatforms forwards group IDs to the supplied platform callback when
// configured. A nil callback returns no metadata with no error; tests can
// install a failing callback to detect unwanted reads.
func (m *mockPricingConfigRepository) GetGroupPlatforms(ctx context.Context, groupIDs []int64) (map[int64]string, error) {
	if m.getGroupPlatformsFn != nil {
		return m.getGroupPlatformsFn(ctx, groupIDs)
	}
	return nil, nil
}

// ListModelPricing forwards the configuration ID to listModelPricingFn when
// configured. A nil callback returns no cards with no error, giving the
// caller an empty successful fixture read.
func (m *mockPricingConfigRepository) ListModelPricing(ctx context.Context, pricingConfigID int64) ([]ModelPricingEntry, error) {
	if m.listModelPricingFn != nil {
		return m.listModelPricingFn(ctx, pricingConfigID)
	}
	return nil, nil
}

// CreateModelPricing forwards the supplied card pointer to
// createModelPricingFn when configured. A nil callback succeeds and assigns
// no card identity; callbacks control any observable mutation of that
// pointer.
func (m *mockPricingConfigRepository) CreateModelPricing(ctx context.Context, pricing *ModelPricingEntry) error {
	if m.createModelPricingFn != nil {
		return m.createModelPricingFn(ctx, pricing)
	}
	return nil
}

// UpdateModelPricing forwards the supplied card pointer to
// updateModelPricingFn when configured. A nil callback succeeds and records
// no state change, so tests provide callbacks for observed writes.
func (m *mockPricingConfigRepository) UpdateModelPricing(ctx context.Context, pricing *ModelPricingEntry) error {
	if m.updateModelPricingFn != nil {
		return m.updateModelPricingFn(ctx, pricing)
	}
	return nil
}

// DeleteModelPricing forwards the supplied card ID to deleteModelPricingFn
// when configured. A nil callback succeeds and leaves fixture state
// unchanged; a callback supplies failure or removal observations.
func (m *mockPricingConfigRepository) DeleteModelPricing(ctx context.Context, id int64) error {
	if m.deleteModelPricingFn != nil {
		return m.deleteModelPricingFn(ctx, id)
	}
	return nil
}

// ReplaceModelPricing forwards configuration ID and the supplied card slice
// to replaceModelPricingFn when configured. A nil callback succeeds without
// recording replacement, while callbacks control any retained slice
// ownership.
func (m *mockPricingConfigRepository) ReplaceModelPricing(ctx context.Context, pricingConfigID int64, pricingList []ModelPricingEntry) error {
	if m.replaceModelPricingFn != nil {
		return m.replaceModelPricingFn(ctx, pricingConfigID, pricingList)
	}
	return nil
}

// InvalidateAuthCacheByKey records each credential string passed to the
// authentication invalidator.
func (m *mockPricingConfigAuthCacheInvalidator) InvalidateAuthCacheByKey(_ context.Context, key string) {
	m.invalidatedKeys = append(m.invalidatedKeys, key)
}

// InvalidateAuthCacheByUserID records each user ID passed to the
// authentication invalidator.
func (m *mockPricingConfigAuthCacheInvalidator) InvalidateAuthCacheByUserID(_ context.Context, userID int64) {
	m.invalidatedUserIDs = append(m.invalidatedUserIDs, userID)
}

// InvalidateAuthCacheByGroupID records each group ID passed to the
// authentication invalidator.
func (m *mockPricingConfigAuthCacheInvalidator) InvalidateAuthCacheByGroupID(_ context.Context, groupID int64) {
	m.invalidatedGroupIDs = append(m.invalidatedGroupIDs, groupID)
}

// newTestPricingConfigService constructs pricing with a nonnil repository's
// optional group-policy reader and time.LoadLocation. It installs no
// authentication invalidator, and the production constructor supplies the
// time.Now default clock.
func newTestPricingConfigService(repo *mockPricingConfigRepository) *PricingConfigService {
	return NewPricingConfigService(repo, nil, PricingConfigOptions{LoadLocation: time.LoadLocation, ReadGroup: repo.readGroup})
}

// newTestPricingConfigServiceWithAuth constructs pricing with the supplied
// authentication fixture, policy callback, and time.LoadLocation. Supply a
// nonnil repository and a nonnil auth pointer when exercising group
// invalidation; the pointer is passed through an interface to the service.
// A nil policy callback leaves policy absent, and the constructor supplies
// its default clock.
func newTestPricingConfigServiceWithAuth(repo *mockPricingConfigRepository, auth *mockPricingConfigAuthCacheInvalidator) *PricingConfigService {
	return NewPricingConfigService(repo, auth, PricingConfigOptions{LoadLocation: time.LoadLocation, ReadGroup: repo.readGroup})
}

// makeStandardRepo returns the supplied configuration from ListAll and the
// supplied platform map from GetGroupPlatforms. The outer row is copied into
// a new slice, while its nested references and the platform map remain
// shared.
func makeStandardRepo(ch PricingConfig, groupPlatforms map[int64]string) *mockPricingConfigRepository {
	return &mockPricingConfigRepository{
		listAllFn: func(_ context.Context) ([]PricingConfig, error) {
			return []PricingConfig{ch}, nil
		},
		getGroupPlatformsFn: func(_ context.Context, _ []int64) (map[int64]string, error) {
			return groupPlatforms, nil
		},
	}
}

// makePolicyRepo supplies the configuration rows and a group reader with a
// synthetic policy derived from those test rows. It sets Enabled from
// configuration status, replaces the restriction stage with billing source,
// and builds AllowedModels from all price-card names.
//
// Only IDs in config.GroupIDs receive a group; other reads return nil without
// error. Each matching read returns policy.Clone so supported policy
// collections can be edited separately. These fixture derivations supply the
// test input and do not establish a production dependency between pricing
// changes and group permissions.
func makePolicyRepo(policy GroupRoutingPolicy, config PricingConfig, platforms map[int64]string) *mockPricingConfigRepository {
	repo := makeStandardRepo(config, platforms)
	// Replace these policy fields together before installing readGroup so every
	// matching group receives the fixture's chosen enabled state, restriction
	// stage, and card-name allowlist.
	policy.Enabled = config.IsActive()
	policy.RestrictionModelSource = config.BillingModelSource
	policy.AllowedModels = []string{}
	for _, price := range config.ModelPricing {
		policy.AllowedModels = append(policy.AllowedModels, price.Models...)
	}
	repo.readGroup = func(_ context.Context, id int64) (*Group, error) {
		if slices.Contains(config.GroupIDs, id) {
			return &Group{ID: id, RoutingPolicy: policy.Clone()}, nil
		}
		return nil, nil
	}
	return repo
}

// testPtrString allocates a supplied string value for optional update fields.
func testPtrString(value string) *string { return &value }

// TestInvalidateCacheFencesConcurrentStaleBuilder starts an older snapshot
// load, runs InvalidateCache with updated repository rows before that older
// load finishes, and verifies that neither stale rows nor stale read errors can
// overwrite the newer post-invalidation snapshot.
func TestInvalidateCacheFencesConcurrentStaleBuilder(t *testing.T) {
	t.Run("stale_success_does_not_overwrite_post_invalidation_snapshot", func(t *testing.T) {
		staleEntered := make(chan struct{})
		releaseStale := make(chan struct{})
		var calls atomic.Int32

		repo := &mockPricingConfigRepository{
			listAllFn: func(_ context.Context) ([]PricingConfig, error) {
				n := calls.Add(1)
				if n == 1 {
					close(staleEntered)
					<-releaseStale
					return []PricingConfig{{
						ID:       1,
						Name:     "stale-v1",
						Status:   StatusActive,
						GroupIDs: []int64{10},
					}}, nil
				}
				return []PricingConfig{{
					ID:       1,
					Name:     "fresh-v2",
					Status:   StatusActive,
					GroupIDs: []int64{10},
				}}, nil
			},
		}
		svc := newTestPricingConfigService(repo)

		staleDone := make(chan *PricingConfig, 1)
		go func() {
			cfg, err := svc.GetPricingConfigForGroup(context.Background(), 10)
			require.NoError(t, err)
			staleDone <- cfg
		}()

		<-staleEntered
		svc.InvalidateCache()
		close(releaseStale)

		staleCallerResult := <-staleDone
		require.NotNil(t, staleCallerResult)
		require.Equal(t, "fresh-v2", staleCallerResult.Name)

		current, err := svc.GetPricingConfigForGroup(context.Background(), 10)
		require.NoError(t, err)
		require.NotNil(t, current)
		require.Equal(t, "fresh-v2", current.Name)
	})

	t.Run("stale_error_does_not_overwrite_post_invalidation_snapshot", func(t *testing.T) {
		staleEntered := make(chan struct{})
		releaseStale := make(chan struct{})
		var calls atomic.Int32

		repo := &mockPricingConfigRepository{
			listAllFn: func(_ context.Context) ([]PricingConfig, error) {
				n := calls.Add(1)
				if n == 1 {
					close(staleEntered)
					<-releaseStale
					return nil, errors.New("stale db timeout")
				}
				return []PricingConfig{{
					ID:       2,
					Name:     "recovered-v2",
					Status:   StatusActive,
					GroupIDs: []int64{10},
				}}, nil
			},
		}
		svc := newTestPricingConfigService(repo)

		staleErrCh := make(chan error, 1)
		go func() {
			_, err := svc.GetPricingConfigForGroup(context.Background(), 10)
			staleErrCh <- err
		}()

		<-staleEntered
		svc.InvalidateCache()
		close(releaseStale)
		require.ErrorContains(t, <-staleErrCh, "stale db timeout")

		current, err := svc.GetPricingConfigForGroup(context.Background(), 10)
		require.NoError(t, err)
		require.NotNil(t, current)
		require.Equal(t, "recovered-v2", current.Name)
	})
}

// TestResolveRoutingModelSkipsPricingCacheLookup configures ListAll to fail and
// installs a group routing policy rewrite. ResolveRoutingModel returns the
// mapped model without loading or cloning the shared pricing cache.
func TestResolveRoutingModelSkipsPricingCacheLookup(t *testing.T) {
	listAllCalls := 0
	repo := &mockPricingConfigRepository{
		listAllFn: func(_ context.Context) ([]PricingConfig, error) {
			listAllCalls++
			return nil, errors.New("pricing cache must not be queried")
		},
		readGroup: func(_ context.Context, id int64) (*Group, error) {
			require.Equal(t, int64(7), id)
			return &Group{
				ID: 7,
				RoutingPolicy: GroupRoutingPolicy{
					Enabled:      true,
					ModelMapping: map[string]string{"claude-*": "claude-sonnet-4-5"},
				},
			}, nil
		},
	}
	svc := newTestPricingConfigService(repo)
	groupID := int64(7)
	mapped := svc.ResolveRoutingModel(context.Background(), &groupID, "claude-opus")
	require.Equal(t, "claude-sonnet-4-5", mapped)
	require.Zero(t, listAllCalls)
}
