package routing

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Validation tests check exact and wildcard model pattern conflicts, group
// mapping pattern conflicts, billing-mode price and multiplier rules, provider
// statistics restrictions, daily time-pricing eligibility, and video tier-label
// canonicalization.

// TestValidateNoConflictingModels supplies exact, case, whitespace,
// punctuation, overlapping wildcard, disjoint, and empty price-card patterns.
// It checks rejection of intersecting normalized ranges and acceptance of
// distinct punctuation identities.
func TestValidateNoConflictingModels(t *testing.T) {
	tests := []struct {
		name        string              // subtest model conflict label
		pricingList []ModelPricingEntry // supplied model pattern cards
		wantErr     bool                // expected model conflict presence
		errContains string              // expected conflict message fragment
	}{
		{
			name: "no duplicates",
			pricingList: []ModelPricingEntry{
				{Models: []string{"claude-sonnet-4", "claude-opus-4"}},
				{Models: []string{"gpt-5.1"}},
			},
			wantErr: false,
		},
		{
			name: "same platform duplicate",
			pricingList: []ModelPricingEntry{
				{Models: []string{"claude-sonnet-4"}},
				{Models: []string{"claude-sonnet-4"}},
			},
			wantErr:     true,
			errContains: "claude-sonnet-4",
		},
		{
			name: "same model duplicate in unified space",
			pricingList: []ModelPricingEntry{
				{Models: []string{"model-a"}},
				{Models: []string{"model-a"}},
			},
			wantErr: true,
		},
		{
			name: "case insensitive",
			pricingList: []ModelPricingEntry{
				{Models: []string{"Claude"}},
				{Models: []string{"claude"}},
			},
			wantErr: true,
		},
		{
			name:        "empty list (nil)",
			pricingList: nil,
			wantErr:     false,
		},
		{
			name: "wildcard_vs_wildcard_conflict",
			pricingList: []ModelPricingEntry{
				{Models: []string{"claude-*"}},
				{Models: []string{"claude-opus-*"}},
			},
			wantErr:     true,
			errContains: "conflict",
		},
		{
			name: "wildcard_vs_exact_conflict",
			pricingList: []ModelPricingEntry{
				{Models: []string{"claude-*"}},
				{Models: []string{"claude-opus-4-6"}},
			},
			wantErr:     true,
			errContains: "conflict",
		},
		{
			name: "overlapping wildcard in unified space",
			pricingList: []ModelPricingEntry{
				{Models: []string{"claude-opus-*"}},
				{Models: []string{"claude-*"}},
			},
			wantErr: true,
		},
		{
			name: "no_conflict_same_platform_different_prefix",
			pricingList: []ModelPricingEntry{
				{Models: []string{"claude-opus-*"}},
				{Models: []string{"gpt-*"}},
			},
			wantErr: false,
		},
		{
			name: "catch_all_wildcard_conflicts_with_everything",
			pricingList: []ModelPricingEntry{
				{Models: []string{"*"}},
				{Models: []string{"gpt-5"}},
			},
			wantErr:     true,
			errContains: "conflict",
		},
		// Conflict detection uses the cache's model identity rules.
		// Distinct punctuation remains distinct, while normalization collisions are rejected.
		{
			name: "claude_dot_and_hyphen_spelling_conflict",
			pricingList: []ModelPricingEntry{
				{Models: []string{"claude-sonnet-4.5"}},
				{Models: []string{"claude-sonnet-4-5"}},
			},
			wantErr:     false,
			errContains: "conflict",
		},
		{
			name: "claude_dot_and_hyphen_spelling_conflict_wildcard",
			pricingList: []ModelPricingEntry{
				{Models: []string{"claude-sonnet-4.5*"}},
				{Models: []string{"claude-sonnet-4-5-x"}},
			},
			wantErr:     false,
			errContains: "conflict",
		},
		{
			name: "surrounding_whitespace_conflict",
			pricingList: []ModelPricingEntry{
				{Models: []string{"gpt-5.6"}},
				{Models: []string{" gpt-5.6 "}},
			},
			wantErr:     true,
			errContains: "conflict",
		},
		{
			// GPT model punctuation also remains part of model identity.
			name: "non_claude_dot_spelling_is_not_normalized",
			pricingList: []ModelPricingEntry{
				{Models: []string{"gpt-5.6"}},
				{Models: []string{"gpt-5-6"}},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateNoConflictingModels(tt.pricingList)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					require.Contains(t, err.Error(), tt.errContains)
				}
			} else {
				require.NoError(t, err)
			}
		})
	}

	t.Run("empty list (empty slice)", func(t *testing.T) {
		err := validateNoConflictingModels([]ModelPricingEntry{})
		require.NoError(t, err)
	})
}

// TestValidateNoConflictingMappings supplies disjoint and intersecting source
// patterns plus case and punctuation variants. Assertions check mapping
// conflicts for overlap and case collisions while dot and hyphen spellings
// remain distinct.
func TestValidateNoConflictingMappings(t *testing.T) {
	tests := []struct {
		name     string            // subtest mapping conflict label
		mapping  map[string]string // supplied source rewrite map
		conflict bool              // expected mapping conflict presence
	}{
		{"nil", nil, false},
		{"separate families", map[string]string{"claude-*": "a", "gpt-*": "b"}, false},
		{"overlapping wildcard", map[string]string{"claude-*": "a", "claude-opus-*": "b"}, true},
		{"exact inside wildcard", map[string]string{"gpt-*": "a", "gpt-4o": "b"}, true},
		{"case duplicate", map[string]string{"GPT-4": "a", "gpt-4": "b"}, true},
		{"dot and hyphen remain distinct", map[string]string{"claude-4.5": "a", "claude-4-5": "b"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateNoConflictingMappings(tt.mapping)
			if tt.conflict {
				require.ErrorContains(t, err, "conflict")
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestConflictsBetween compares prepared exact and wildcard entries for
// identical, disjoint, prefix-overlapping, and catch-all ranges. Each case
// checks the resulting conflict boolean.
func TestConflictsBetween(t *testing.T) {
	tests := []struct {
		name string     // subtest prepared range label
		a, b modelEntry // first and second prepared ranges
		want bool       // expected range overlap result
	}{
		{
			name: "exact same",
			a:    modelEntry{prefix: "claude-opus-4", wildcard: false},
			b:    modelEntry{prefix: "claude-opus-4", wildcard: false},
			want: true,
		},
		{
			name: "exact different",
			a:    modelEntry{prefix: "claude-opus-4", wildcard: false},
			b:    modelEntry{prefix: "gpt-4o", wildcard: false},
			want: false,
		},
		{
			name: "wildcard matches exact",
			a:    modelEntry{prefix: "claude-", wildcard: true},
			b:    modelEntry{prefix: "claude-opus-4", wildcard: false},
			want: true,
		},
		{
			name: "exact does not match unrelated wildcard",
			a:    modelEntry{prefix: "gpt-4o", wildcard: false},
			b:    modelEntry{prefix: "claude-", wildcard: true},
			want: false,
		},
		{
			name: "wildcard prefix overlap",
			a:    modelEntry{prefix: "claude-", wildcard: true},
			b:    modelEntry{prefix: "claude-opus-", wildcard: true},
			want: true,
		},
		{
			name: "wildcards no overlap",
			a:    modelEntry{prefix: "claude-", wildcard: true},
			b:    modelEntry{prefix: "gpt-", wildcard: true},
			want: false,
		},
		{
			name: "catch-all wildcard vs any",
			a:    modelEntry{prefix: "", wildcard: true},
			b:    modelEntry{prefix: "anything", wildcard: false},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, conflictsBetween(tt.a, tt.b))
		})
	}
}

// TestValidatePricingBillingMode varies charging modes, explicit amount
// presence, intervals, negative amounts, and modifiers. It checks accepted
// combinations and the reported reason text for each invalid supplied card.
func TestValidatePricingBillingMode(t *testing.T) {
	tests := []struct {
		name    string              // subtest billing validation label
		pricing []ModelPricingEntry // supplied charging mode cards
		wantErr bool                // expected validation error presence
		errMsg  string              // expected validation message fragment
	}{
		{
			name:    "token mode - valid",
			pricing: []ModelPricingEntry{{BillingMode: BillingModeToken}},
		},
		{
			name: "per_request with price - valid",
			pricing: []ModelPricingEntry{{
				BillingMode:     BillingModePerRequest,
				PerRequestPrice: testPtrFloat64(0.5),
			}},
		},
		{
			name: "per_request with intervals - valid",
			pricing: []ModelPricingEntry{{
				BillingMode: BillingModePerRequest,
				Intervals:   []PricingInterval{{MinTokens: 0, MaxTokens: testPtrInt(1000), PerRequestPrice: testPtrFloat64(0.1)}},
			}},
		},
		{
			name:    "per_request no price no intervals - invalid",
			pricing: []ModelPricingEntry{{BillingMode: BillingModePerRequest}},
			wantErr: true,
			errMsg:  "per-request price or intervals required",
		},
		{
			name:    "image no price no intervals - invalid",
			pricing: []ModelPricingEntry{{BillingMode: BillingModeImage}},
			wantErr: true,
			errMsg:  "per-request price or intervals required",
		},
		{
			name:    "empty list - valid",
			pricing: []ModelPricingEntry{},
		},
		{
			name: "negative input_price - invalid",
			pricing: []ModelPricingEntry{{
				BillingMode: BillingModeToken,
				InputPrice:  testPtrFloat64(-0.01),
			}},
			wantErr: true,
			errMsg:  "input_price must be >= 0",
		},
		{
			name: "negative price_multiplier - invalid",
			pricing: []ModelPricingEntry{{
				BillingMode:     BillingModeToken,
				PriceMultiplier: testPtrFloat64(-1),
				InputPrice:      testPtrFloat64(0.01),
			}},
			wantErr: true,
			errMsg:  "price_multiplier must be >= 0",
		},
		{
			name: "price_multiplier without explicit price - invalid",
			pricing: []ModelPricingEntry{{
				BillingMode:     BillingModeToken,
				PriceMultiplier: testPtrFloat64(1.5),
			}},
			wantErr: true,
			errMsg:  "price_multiplier requires at least one explicit price",
		},
		{
			name: "price_multiplier with interval price - valid",
			pricing: []ModelPricingEntry{{
				BillingMode:     BillingModeToken,
				PriceMultiplier: testPtrFloat64(1.5),
				Intervals: []PricingInterval{{
					MinTokens:  0,
					InputPrice: testPtrFloat64(0.01),
				}},
			}},
		},
		{
			name: "price_multiplier with image input price - valid",
			pricing: []ModelPricingEntry{{
				BillingMode:     BillingModeToken,
				PriceMultiplier: testPtrFloat64(1.5),
				ImageInputPrice: testPtrFloat64(0.01),
			}},
		},
		{
			name: "OpenAI token fast_mode_multiplier with explicit price - valid",
			pricing: []ModelPricingEntry{{
				BillingMode:        BillingModeToken,
				FastModeMultiplier: testPtrFloat64(2),
				InputPrice:         testPtrFloat64(0.01),
			}},
		},
		{
			name: "fast_mode_multiplier in unified pricing - valid",
			pricing: []ModelPricingEntry{{
				BillingMode:        BillingModeToken,
				FastModeMultiplier: testPtrFloat64(2),
				InputPrice:         testPtrFloat64(0.01),
			}},
		},
		{
			name: "fast_mode_multiplier on per-request pricing - invalid",
			pricing: []ModelPricingEntry{{
				BillingMode:        BillingModePerRequest,
				FastModeMultiplier: testPtrFloat64(2),
				PerRequestPrice:    testPtrFloat64(0.01),
			}},
			wantErr: true,
			errMsg:  "fast_mode_multiplier is only supported for token billing mode",
		},
		{
			name: "fast_mode_multiplier without explicit price - invalid",
			pricing: []ModelPricingEntry{{
				BillingMode:        BillingModeToken,
				FastModeMultiplier: testPtrFloat64(2),
			}},
			wantErr: true,
			errMsg:  "fast_mode_multiplier requires at least one explicit price",
		},
		{
			name: "negative fast_mode_multiplier - invalid",
			pricing: []ModelPricingEntry{{
				BillingMode:        BillingModeToken,
				FastModeMultiplier: testPtrFloat64(-1),
				InputPrice:         testPtrFloat64(0.01),
			}},
			wantErr: true,
			errMsg:  "fast_mode_multiplier must be >= 0",
		},
		{
			name: "interval with no price fields - invalid",
			pricing: []ModelPricingEntry{{
				BillingMode:     BillingModePerRequest,
				PerRequestPrice: testPtrFloat64(0.5),
				Intervals:       []PricingInterval{{MinTokens: 0, MaxTokens: testPtrInt(1000)}},
			}},
			wantErr: true,
			errMsg:  "has no price fields set",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePricingBillingMode(tt.pricing)
			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// TestValidateProviderStatsPricingEntries_RejectsFastModeMultiplier supplies
// a user Fast multiplier of two in a provider-cost card. Validation rejects
// it with the provider statistics restriction message.
func TestValidateProviderStatsPricingEntries_RejectsFastModeMultiplier(t *testing.T) {
	err := (PricingConfigValidation{LoadLocation: time.LoadLocation}).ProviderStatsPricing([]ModelPricingEntry{{
		BillingMode:        BillingModeToken,
		FastModeMultiplier: testPtrFloat64(2),
		InputPrice:         testPtrFloat64(0.01),
	}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "fast_mode_multiplier is not supported for provider stats pricing")
}

// TestValidatePricingEntries_RejectsTimePricingForNonTokenMode supplies an
// explicit per-request amount and populated daily multiplier periods on a
// per-request card. Entry validation returns the designated unsupported-mode
// reason.
func TestValidatePricingEntries_RejectsTimePricingForNonTokenMode(t *testing.T) {
	err := (PricingConfigValidation{LoadLocation: time.LoadLocation}).PricingEntries([]ModelPricingEntry{{
		BillingMode:     BillingModePerRequest,
		PerRequestPrice: testPtrFloat64(0.05),
		TimePricing: &TimePricingConfig{
			Timezone: "Asia/Shanghai",
			Periods:  []TimePricingPeriod{{StartTime: "09:00", EndTime: "12:00", Multiplier: 2}},
		},
	}})
	require.Error(t, err)
	require.Contains(t, err.Error(), "TIME_PRICING_UNSUPPORTED_MODE")
}

// TestModelPricingAcceptsTimePricing supplies a daily multiplier with an
// empty billing mode and a valid timezone. Validation succeeds because the
// omitted mode uses token requirements.
func TestModelPricingAcceptsTimePricing(t *testing.T) {
	err := (PricingConfigValidation{LoadLocation: time.LoadLocation}).PricingEntries([]ModelPricingEntry{{
		Models: []string{"gpt-5"},
		TimePricing: &TimePricingConfig{
			Timezone: "Asia/Shanghai",
			Periods:  []TimePricingPeriod{{StartTime: "09:00", EndTime: "12:00", Multiplier: 2}},
		},
	}})
	require.NoError(t, err)
}

// TestVideoPriceConfigurationCanonicalizesStoredLabels validates a working video
// price card containing padded labels. Persistence receives canonical keys, and
// collisions or oversized dimensions fail before configuration publication.
func TestVideoPriceConfigurationCanonicalizesStoredLabels(t *testing.T) {
	validator := PricingConfigValidation{LoadLocation: time.LoadLocation}
	entries := []ModelPricingEntry{{Models: []string{"custom-video"}, BillingMode: BillingModeVideo, Intervals: []PricingInterval{
		{TierLabel: " 768P ", PerRequestPrice: testPtrFloat64(0.1)},
		{TierLabel: " 2K ", PerRequestPrice: testPtrFloat64(0)},
	}}}
	require.NoError(t, validator.PricingEntries(entries))
	require.Equal(t, "768p", entries[0].Intervals[0].TierLabel)
	require.Equal(t, "2k", entries[0].Intervals[1].TierLabel)
	entries[0].Intervals[1].TierLabel = "768p"
	require.ErrorContains(t, validator.PricingEntries(entries), "duplicate video resolution")
}
