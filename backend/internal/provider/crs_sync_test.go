package provider

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/provider/transfer"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// crsExporterStub returns a preconfigured CRS export payload for sync tests.
type crsExporterStub struct {
	response *transfer.CRSExportResponse // configured export payload
}

// crsProviderStoreStub extends crsShadowStore with CRS account ID lookups for
// SyncFromCRS and PreviewFromCRS unit tests.
type crsProviderStoreStub struct {
	*crsShadowStore // embedded shadow and configuration store
}

// TestBuildSelectedSet supplies absent, empty, populated, and duplicate
// source-ID selections. It asserts nil and nonnil empty maps retain distinct
// creation meaning and every unique supplied ID appears in the result.
func TestBuildSelectedSet(t *testing.T) {
	tests := []struct {
		name     string   // table case display name
		ids      []string // supplied source identity selection
		wantNil  bool     // expect absent selection sentinel
		wantSize int      // expected unique membership count
	}{
		{
			name:    "nil input returns nil (backward compatible: create all)",
			ids:     nil,
			wantNil: true,
		},
		{
			name:     "empty slice returns empty map (create none)",
			ids:      []string{},
			wantNil:  false,
			wantSize: 0,
		},
		{
			name:     "single ID",
			ids:      []string{"abc-123"},
			wantNil:  false,
			wantSize: 1,
		},
		{
			name:     "multiple IDs",
			ids:      []string{"a", "b", "c"},
			wantNil:  false,
			wantSize: 3,
		},
		{
			name:     "duplicate IDs are deduplicated",
			ids:      []string{"a", "a", "b"},
			wantNil:  false,
			wantSize: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CRSBuildSelectedSet(tt.ids)
			if tt.wantNil {
				if got != nil {
					t.Errorf("buildSelectedSet(%v) = %v, want nil", tt.ids, got)
				}
				return
			}
			if got == nil {
				t.Fatalf("buildSelectedSet(%v) = nil, want non-nil map", tt.ids)
			}
			if len(got) != tt.wantSize {
				t.Errorf("buildSelectedSet(%v) has %d entries, want %d", tt.ids, len(got), tt.wantSize)
			}
			// Every distinct input ID appears in the result.
			for _, id := range tt.ids {
				if _, ok := got[id]; !ok {
					t.Errorf("buildSelectedSet(%v) missing key %q", tt.ids, id)
				}
			}
		})
	}
}

// TestShouldCreateProvider checks exact ID membership under absent, empty, and
// populated creation selections. It asserts nil admits every ID, explicit empty
// admits none, and unmatched IDs are rejected.
func TestShouldCreateProvider(t *testing.T) {
	tests := []struct {
		name  string // table case display name
		crsID string // queried source account identity
		// selectedSet chooses absent, explicit empty, or populated
		// membership
		// fixtures; each case exercises a distinct creation choice
		// returned by
		// CRSShouldCreateProvider.
		selectedSet map[string]struct{}
		want        bool // expected new creation eligibility
	}{
		{
			name:        "nil set allows all (backward compatible)",
			crsID:       "any-id",
			selectedSet: nil,
			want:        true,
		},
		{
			name:        "empty set blocks all",
			crsID:       "any-id",
			selectedSet: map[string]struct{}{},
			want:        false,
		},
		{
			name:        "ID in set is allowed",
			crsID:       "abc-123",
			selectedSet: map[string]struct{}{"abc-123": {}, "def-456": {}},
			want:        true,
		},
		{
			name:        "ID not in set is blocked",
			crsID:       "xyz-789",
			selectedSet: map[string]struct{}{"abc-123": {}, "def-456": {}},
			want:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CRSShouldCreateProvider(tt.crsID, tt.selectedSet)
			if got != tt.want {
				t.Errorf("shouldCreateProvider(%q, %v) = %v, want %v",
					tt.crsID, tt.selectedSet, got, tt.want)
			}
		})
	}
}

// TestGuardCRSShadowParentInvariant adds a Spark shadow to an OpenAI OAuth
// parent after checking unrestricted target conversion. It asserts incompatible
// platform/type targets are rejected while OpenAI OAuth remains accepted; this
// exercises the guard directly.
func TestGuardCRSShadowParentInvariant(t *testing.T) {
	ctx := context.Background()
	repo := newCRSShadowStore()

	mother := &Record{Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeOAuth}
	require.NoError(t, repo.Create(ctx, mother))
	parentID := mother.ID

	// Without shadows, platform and credential type may change.
	require.NoError(t, GuardCRSShadowParentInvariant(ctx, repo, mother, capability.PlatformOpenAI, capability.ProviderTypeAPIKey))
	require.NoError(t, GuardCRSShadowParentInvariant(ctx, repo, mother, capability.PlatformAnthropic, capability.ProviderTypeOAuth))

	// Add a shadow before checking the restrictions.
	shadow := &Record{
		Platform:         capability.PlatformOpenAI,
		Type:             capability.ProviderTypeOAuth,
		ParentProviderID: &parentID,
		QuotaDimension:   QuotaDimensionSpark,
	}
	require.NoError(t, repo.Create(ctx, shadow))

	// Reject conversion to an OpenAI API Key provider.
	err := GuardCRSShadowParentInvariant(ctx, repo, mother, capability.PlatformOpenAI, capability.ProviderTypeAPIKey)
	require.Error(t, err, "must reject converting a shadow parent to openai api_key")
	require.Contains(t, err.Error(), "spark-shadow parent")

	// Reject an Anthropic target even when its type remains OAuth.
	require.Error(t, GuardCRSShadowParentInvariant(ctx, repo, mother, capability.PlatformAnthropic, capability.ProviderTypeOAuth),
		"must reject moving a shadow parent to a non-OpenAI platform even if type stays oauth")

	// Reject a Gemini API Key target.
	require.Error(t, GuardCRSShadowParentInvariant(ctx, repo, mother, capability.PlatformGemini, capability.ProviderTypeAPIKey))

	// Allow OpenAI OAuth resynchronization while shadows exist.
	require.NoError(t, GuardCRSShadowParentInvariant(ctx, repo, mother, capability.PlatformOpenAI, capability.ProviderTypeOAuth))
}

// Fetch returns the stub's configured CRS export payload without network I/O.
func (s crsExporterStub) Fetch(context.Context, string, string, string) (*transfer.CRSExportResponse, error) {
	return s.response, nil
}

// GetByCRSAccountID scans stored records for a matching crs_account_id Extra
// value.
func (s crsProviderStoreStub) GetByCRSAccountID(_ context.Context, crsID string) (*Record, error) {
	for _, row := range s.rows {
		if row != nil && row.GetExtraString("crs_account_id") == crsID {
			return row, nil
		}
	}
	return nil, nil
}

// ListCRSAccountIDs returns every stored crs_account_id mapped to its record ID.
func (s crsProviderStoreStub) ListCRSAccountIDs(_ context.Context) (map[string]int64, error) {
	ids := make(map[string]int64, len(s.rows))
	for _, row := range s.rows {
		if row != nil && row.GetExtraString("crs_account_id") != "" {
			ids[row.GetExtraString("crs_account_id")] = row.ID
		}
	}
	return ids, nil
}

// TestSyncFromCRSOpenAIResponsesPreservesCallerCredentialsMap imports OpenAI
// Responses entries with omitted and /v1-suffixed base URLs. Persisted records
// receive normalized base_url values while the caller's source maps stay
// unchanged.
func TestSyncFromCRSOpenAIResponsesPreservesCallerCredentialsMap(t *testing.T) {
	omittedBase := map[string]any{"api_key": "sk-one"}
	versionedBase := map[string]any{"api_key": "sk-two", "base_url": "https://relay.example.com/v1"}
	export := &transfer.CRSExportResponse{Success: true}
	export.Data.OpenAIResponsesProviders = []transfer.CRSOpenAIResponsesProvider{
		{ID: "resp-1", Kind: "openai-responses", Name: "one", IsActive: true, Schedulable: true, Credentials: omittedBase},
		{ID: "resp-2", Kind: "openai-responses", Name: "two", IsActive: false, Schedulable: true, Credentials: versionedBase},
	}

	repo := crsProviderStoreStub{crsShadowStore: newCRSShadowStore()}
	syncer := NewCRSSync(repo, nil, crsExporterStub{response: export}, CRSOptions{})
	res, err := syncer.SyncFromCRS(context.Background(), SyncFromCRSInput{})
	require.NoError(t, err)
	require.Equal(t, 2, res.Created)

	require.NotContains(t, omittedBase, "base_url")
	require.Equal(t, "https://relay.example.com/v1", versionedBase["base_url"])

	createdOne, err := repo.GetByCRSAccountID(context.Background(), "resp-1")
	require.NoError(t, err)
	require.Equal(t, "https://api.openai.com", createdOne.Credentials["base_url"])
	require.Equal(t, "active", createdOne.Status)

	createdTwo, err := repo.GetByCRSAccountID(context.Background(), "resp-2")
	require.NoError(t, err)
	require.Equal(t, "https://relay.example.com", createdTwo.Credentials["base_url"])
	require.Equal(t, "inactive", createdTwo.Status)
}
