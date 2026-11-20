package provider

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/protocol/google"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
)

// TestNormalizeTier supplies empty, recognized substring, mixed-case, and
// unknown account labels. It checks the configured uppercase category or empty
// value returned by the local display normalizer.
func TestNormalizeTier(t *testing.T) {
	tests := []struct {
		name     string // tier display case name
		raw      string // input upstream tier text
		expected string // expected normalized tier label
	}{
		{name: "empty string", raw: "", expected: ""},
		{name: "free-tier", raw: "free-tier", expected: "FREE"},
		{name: "g1-pro-tier", raw: "g1-pro-tier", expected: "PRO"},
		{name: "g1-ultra-tier", raw: "g1-ultra-tier", expected: "ULTRA"},
		{name: "unknown-something", raw: "unknown-something", expected: "UNKNOWN"},
		{name: "Google AI Pro contains pro keyword", raw: "Google AI Pro", expected: "PRO"},
		{name: "case insensitive FREE", raw: "FREE-TIER", expected: "FREE"},
		{name: "case insensitive Ultra", raw: "Ultra Plan", expected: "ULTRA"},
		{name: "arbitrary unrecognized string", raw: "enterprise-custom", expected: "UNKNOWN"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeAntigravityTier(tt.raw)
			require.Equal(t, tt.expected, got, "providercore.NormalizeAntigravityTier(%q)", tt.raw)
		})
	}
}

// aqfBoolPtr creates an independently allocated boolean fixture capability
// value.
func aqfBoolPtr(v bool) *bool { return &v }

// aqfIntPtr creates an independently allocated integer fixture capability
// value.
func aqfIntPtr(v int) *int { return &v }

// TestBuildUsageInfo_BasicModels combines two quota-bearing models with
// optional capability values. It checks observation/tier metadata, exact model
// utilization/reset text, selected pointers, MIME flags, and missing
// capability values.
func TestBuildUsageInfo_BasicModels(t *testing.T) {
	fetcher := &AntigravityQuota{}

	modelsResp := &antigravity.FetchAvailableModelsResponse{
		Models: map[string]antigravity.ModelInfo{
			"claude-sonnet-4-20250514": {
				QuotaInfo: &antigravity.ModelQuotaInfo{
					RemainingFraction: 0.75,
					ResetTime:         "2026-03-08T12:00:00Z",
				},
				DisplayName:      "Claude Sonnet 4",
				SupportsImages:   aqfBoolPtr(true),
				SupportsThinking: aqfBoolPtr(false),
				ThinkingBudget:   aqfIntPtr(0),
				Recommended:      aqfBoolPtr(true),
				MaxTokens:        aqfIntPtr(200000),
				MaxOutputTokens:  aqfIntPtr(16384),
				SupportedMimeTypes: map[string]bool{
					"image/png":  true,
					"image/jpeg": true,
				},
			},
			"gemini-2.5-pro": {
				QuotaInfo: &antigravity.ModelQuotaInfo{
					RemainingFraction: 0.50,
					ResetTime:         "2026-03-08T15:00:00Z",
				},
				DisplayName:     "Gemini 2.5 Pro",
				MaxTokens:       aqfIntPtr(1000000),
				MaxOutputTokens: aqfIntPtr(65536),
			},
		},
	}

	info := fetcher.BuildUsageInfo(modelsResp, "g1-pro-tier", "PRO", nil)

	// Check shared display fields.
	require.NotNil(t, info.UpdatedAt, "UpdatedAt should be set")
	require.Equal(t, "PRO", info.SubscriptionTier)
	require.Equal(t, "g1-pro-tier", info.SubscriptionTierRaw)

	// Check per-model used percentages.
	require.Len(t, info.AntigravityQuota, 2)

	sonnetQuota := info.AntigravityQuota["claude-sonnet-4-20250514"]
	require.NotNil(t, sonnetQuota)
	require.Equal(t, 25, sonnetQuota.Utilization) // (1 - 0.75) * 100 = 25
	require.Equal(t, "2026-03-08T12:00:00Z", sonnetQuota.ResetTime)

	geminiQuota := info.AntigravityQuota["gemini-2.5-pro"]
	require.NotNil(t, geminiQuota)
	require.Equal(t, 50, geminiQuota.Utilization) // (1 - 0.50) * 100 = 50
	require.Equal(t, "2026-03-08T15:00:00Z", geminiQuota.ResetTime)

	// Check per-model capability details.
	require.Len(t, info.AntigravityQuotaDetails, 2)

	sonnetDetail := info.AntigravityQuotaDetails["claude-sonnet-4-20250514"]
	require.NotNil(t, sonnetDetail)
	require.Equal(t, "Claude Sonnet 4", sonnetDetail.DisplayName)
	require.Equal(t, aqfBoolPtr(true), sonnetDetail.SupportsImages)
	require.Equal(t, aqfBoolPtr(false), sonnetDetail.SupportsThinking)
	require.Equal(t, aqfIntPtr(0), sonnetDetail.ThinkingBudget)
	require.Equal(t, aqfBoolPtr(true), sonnetDetail.Recommended)
	require.Equal(t, aqfIntPtr(200000), sonnetDetail.MaxTokens)
	require.Equal(t, aqfIntPtr(16384), sonnetDetail.MaxOutputTokens)
	require.Equal(t, map[string]bool{"image/png": true, "image/jpeg": true}, sonnetDetail.SupportedMimeTypes)

	geminiDetail := info.AntigravityQuotaDetails["gemini-2.5-pro"]
	require.NotNil(t, geminiDetail)
	require.Equal(t, "Gemini 2.5 Pro", geminiDetail.DisplayName)
	require.Nil(t, geminiDetail.SupportsImages)
	require.Nil(t, geminiDetail.SupportsThinking)
	require.Equal(t, aqfIntPtr(1000000), geminiDetail.MaxTokens)
	require.Equal(t, aqfIntPtr(65536), geminiDetail.MaxOutputTokens)
}

// TestBuildUsageInfo_DeprecatedModels supplies two deprecated-model
// replacements beside an available model. It checks both redirect entries
// carry the configured replacement identifiers in the constructed display map.
func TestBuildUsageInfo_DeprecatedModels(t *testing.T) {
	fetcher := &AntigravityQuota{}

	modelsResp := &antigravity.FetchAvailableModelsResponse{
		Models: map[string]antigravity.ModelInfo{
			"claude-sonnet-4-20250514": {
				QuotaInfo: &antigravity.ModelQuotaInfo{
					RemainingFraction: 1.0,
				},
			},
		},
		DeprecatedModelIDs: map[string]antigravity.DeprecatedModelInfo{
			"claude-3-sonnet-20240229": {NewModelID: "claude-sonnet-4-20250514"},
			"claude-3-haiku-20240307":  {NewModelID: "claude-haiku-3.5-latest"},
		},
	}

	info := fetcher.BuildUsageInfo(modelsResp, "", "", nil)

	require.Len(t, info.ModelForwardingRules, 2)
	require.Equal(t, "claude-sonnet-4-20250514", info.ModelForwardingRules["claude-3-sonnet-20240229"])
	require.Equal(t, "claude-haiku-3.5-latest", info.ModelForwardingRules["claude-3-haiku-20240307"])
}

// TestBuildUsageInfo_NoDeprecatedModels supplies model quota without any
// deprecated identifiers. It checks the constructed forwarding map remains
// nil, retaining absence for this response shape.
func TestBuildUsageInfo_NoDeprecatedModels(t *testing.T) {
	fetcher := &AntigravityQuota{}

	modelsResp := &antigravity.FetchAvailableModelsResponse{
		Models: map[string]antigravity.ModelInfo{
			"some-model": {
				QuotaInfo: &antigravity.ModelQuotaInfo{RemainingFraction: 0.9},
			},
		},
	}

	info := fetcher.BuildUsageInfo(modelsResp, "", "", nil)

	require.Nil(t, info.ModelForwardingRules, "ModelForwardingRules should be nil when no deprecated models")
}

// TestBuildUsageInfo_EmptyModels supplies an empty model response. It checks
// nonnil empty quota/capability maps and an absent FiveHour view when no
// fixed-priority model is available.
func TestBuildUsageInfo_EmptyModels(t *testing.T) {
	fetcher := &AntigravityQuota{}

	modelsResp := &antigravity.FetchAvailableModelsResponse{
		Models: map[string]antigravity.ModelInfo{},
	}

	info := fetcher.BuildUsageInfo(modelsResp, "", "", nil)

	require.NotNil(t, info)
	require.NotNil(t, info.AntigravityQuota)
	require.Empty(t, info.AntigravityQuota)
	require.NotNil(t, info.AntigravityQuotaDetails)
	require.Empty(t, info.AntigravityQuotaDetails)
	require.Nil(t, info.FiveHour, "FiveHour should be nil when no priority model exists")
}

// TestBuildUsageInfo_ModelWithNilQuotaInfo supplies a model whose capability
// data has no quota. It checks both quota and capability display maps omit
// that model in this configured response.
func TestBuildUsageInfo_ModelWithNilQuotaInfo(t *testing.T) {
	fetcher := &AntigravityQuota{}

	modelsResp := &antigravity.FetchAvailableModelsResponse{
		Models: map[string]antigravity.ModelInfo{
			"model-without-quota": {
				DisplayName: "No Quota Model",
				// This model omits QuotaInfo.
			},
		},
	}

	info := fetcher.BuildUsageInfo(modelsResp, "", "", nil)

	require.NotNil(t, info)
	require.Empty(t, info.AntigravityQuota, "models with nil QuotaInfo should be skipped")
	require.Empty(t, info.AntigravityQuotaDetails, "models with nil QuotaInfo should be skipped from details too")
}

// TestBuildUsageInfo_FiveHourPriorityOrder supplies the first and third
// priority models with differing balances. It checks the first model's
// floating utilization and a parsed reset pointer for the compatibility
// window.
func TestBuildUsageInfo_FiveHourPriorityOrder(t *testing.T) {
	fetcher := &AntigravityQuota{}

	// priorityModels = ["claude-sonnet-4-20250514", "claude-sonnet-4",
	// "gemini-2.5-pro"]
	// FiveHour uses the first available priority model's quota.
	modelsResp := &antigravity.FetchAvailableModelsResponse{
		Models: map[string]antigravity.ModelInfo{
			"gemini-2.5-pro": {
				QuotaInfo: &antigravity.ModelQuotaInfo{
					RemainingFraction: 0.40,
					ResetTime:         "2026-03-08T18:00:00Z",
				},
			},
			"claude-sonnet-4-20250514": {
				QuotaInfo: &antigravity.ModelQuotaInfo{
					RemainingFraction: 0.80,
					ResetTime:         "2026-03-08T12:00:00Z",
				},
			},
		},
	}

	info := fetcher.BuildUsageInfo(modelsResp, "", "", nil)

	require.NotNil(t, info.FiveHour, "FiveHour should be set when a priority model exists")
	// claude-sonnet-4-20250514 takes precedence over the other priority models.
	expectedUtilization := (1.0 - 0.80) * 100 // 20
	// Allow floating representation error in the converted percentage.
	require.InDelta(t, expectedUtilization, info.FiveHour.Utilization, 0.01)
	require.NotNil(t, info.FiveHour.ResetsAt, "ResetsAt should be parsed from ResetTime")
}

// TestBuildUsageInfo_FiveHourFallbackToClaude4 omits the first priority model
// but supplies the second and third. It checks the second model’s utilization
// determines the compatibility FiveHour view.
func TestBuildUsageInfo_FiveHourFallbackToClaude4(t *testing.T) {
	fetcher := &AntigravityQuota{}

	// The absent first candidate lets claude-sonnet-4 take precedence over the
	// supplied third candidate, so display selection does not depend on map order.
	modelsResp := &antigravity.FetchAvailableModelsResponse{
		Models: map[string]antigravity.ModelInfo{
			"claude-sonnet-4": {
				QuotaInfo: &antigravity.ModelQuotaInfo{
					RemainingFraction: 0.60,
					ResetTime:         "2026-03-08T14:00:00Z",
				},
			},
			"gemini-2.5-pro": {
				QuotaInfo: &antigravity.ModelQuotaInfo{
					RemainingFraction: 0.30,
				},
			},
		},
	}

	info := fetcher.BuildUsageInfo(modelsResp, "", "", nil)

	require.NotNil(t, info.FiveHour)
	expectedUtilization := (1.0 - 0.60) * 100 // 40
	// Allow floating representation error in the converted percentage.
	require.InDelta(t, expectedUtilization, info.FiveHour.Utilization, 0.01)
}

// TestBuildUsageInfo_FiveHourFallbackToGemini supplies the third priority
// model beside an unrelated model. It checks the third model’s utilization
// populates FiveHour when earlier candidates are absent.
func TestBuildUsageInfo_FiveHourFallbackToGemini(t *testing.T) {
	fetcher := &AntigravityQuota{}

	// Only the third priority model, gemini-2.5-pro, is available.
	modelsResp := &antigravity.FetchAvailableModelsResponse{
		Models: map[string]antigravity.ModelInfo{
			"gemini-2.5-pro": {
				QuotaInfo: &antigravity.ModelQuotaInfo{
					RemainingFraction: 0.30,
				},
			},
			"other-model": {
				QuotaInfo: &antigravity.ModelQuotaInfo{
					RemainingFraction: 0.90,
				},
			},
		},
	}

	info := fetcher.BuildUsageInfo(modelsResp, "", "", nil)

	require.NotNil(t, info.FiveHour)
	expectedUtilization := (1.0 - 0.30) * 100 // 70
	// Allow floating representation error in the converted percentage.
	require.InDelta(t, expectedUtilization, info.FiveHour.Utilization, 0.01)
}

// TestBuildUsageInfo_FiveHourNoPriorityModel supplies quota only for an
// unrelated model. It checks the compatibility FiveHour view remains nil
// rather than selecting arbitrary response-map order.
func TestBuildUsageInfo_FiveHourNoPriorityModel(t *testing.T) {
	fetcher := &AntigravityQuota{}

	// No priority model is present.
	modelsResp := &antigravity.FetchAvailableModelsResponse{
		Models: map[string]antigravity.ModelInfo{
			"some-other-model": {
				QuotaInfo: &antigravity.ModelQuotaInfo{
					RemainingFraction: 0.50,
				},
			},
		},
	}

	info := fetcher.BuildUsageInfo(modelsResp, "", "", nil)

	require.Nil(t, info.FiveHour, "FiveHour should be nil when no priority model exists")
}

// TestBuildUsageInfo_FiveHourWithEmptyResetTime supplies a priority model with
// quota and empty reset text. It checks a present window with nil reset
// pointer and zero countdown for unavailable timing.
func TestBuildUsageInfo_FiveHourWithEmptyResetTime(t *testing.T) {
	fetcher := &AntigravityQuota{}

	modelsResp := &antigravity.FetchAvailableModelsResponse{
		Models: map[string]antigravity.ModelInfo{
			"claude-sonnet-4-20250514": {
				QuotaInfo: &antigravity.ModelQuotaInfo{
					RemainingFraction: 0.50,
					ResetTime:         "", // empty reset time
				},
			},
		},
	}

	info := fetcher.BuildUsageInfo(modelsResp, "", "", nil)

	require.NotNil(t, info.FiveHour)
	require.Nil(t, info.FiveHour.ResetsAt, "ResetsAt should be nil when ResetTime is empty")
	require.Equal(t, 0, info.FiveHour.RemainingSeconds)
}

// TestBuildUsageInfo_FullUtilization supplies a model with zero remaining
// fraction. It checks the per-model entry exists and displays one hundred used
// under this exact quota observation.
func TestBuildUsageInfo_FullUtilization(t *testing.T) {
	fetcher := &AntigravityQuota{}

	modelsResp := &antigravity.FetchAvailableModelsResponse{
		Models: map[string]antigravity.ModelInfo{
			"claude-sonnet-4-20250514": {
				QuotaInfo: &antigravity.ModelQuotaInfo{
					RemainingFraction: 0.0, // fully used
					ResetTime:         "2026-03-08T12:00:00Z",
				},
			},
		},
	}

	info := fetcher.BuildUsageInfo(modelsResp, "", "", nil)

	quota := info.AntigravityQuota["claude-sonnet-4-20250514"]
	require.NotNil(t, quota)
	require.Equal(t, 100, quota.Utilization)
}

// TestBuildUsageInfo_ZeroUtilization supplies a model with complete remaining
// fraction. It checks the per-model entry exists and displays zero used for
// this available quota observation.
func TestBuildUsageInfo_ZeroUtilization(t *testing.T) {
	fetcher := &AntigravityQuota{}

	modelsResp := &antigravity.FetchAvailableModelsResponse{
		Models: map[string]antigravity.ModelInfo{
			"claude-sonnet-4-20250514": {
				QuotaInfo: &antigravity.ModelQuotaInfo{
					RemainingFraction: 1.0, // fully available
				},
			},
		},
	}

	info := fetcher.BuildUsageInfo(modelsResp, "", "", nil)
	quota := info.AntigravityQuota["claude-sonnet-4-20250514"]
	require.NotNil(t, quota)
	require.Equal(t, 0, quota.Utilization)
}

// TestBuildUsageInfo_AICredits supplies a paid-tier credit entry with numeric
// amount and minimum text. It checks one display credit retains its type and
// the configured converted balance values.
func TestBuildUsageInfo_AICredits(t *testing.T) {
	fetcher := &AntigravityQuota{}
	modelsResp := &antigravity.FetchAvailableModelsResponse{
		Models: map[string]antigravity.ModelInfo{},
	}
	loadResp := &antigravity.LoadCodeAssistResponse{
		PaidTier: &antigravity.PaidTierInfo{
			ID: "g1-pro-tier",
			AvailableCredits: []antigravity.AvailableCredit{
				{
					CreditType:                  "GOOGLE_ONE_AI",
					CreditAmount:                "25",
					MinimumCreditAmountForUsage: "5",
				},
			},
		},
	}

	info := fetcher.BuildUsageInfo(modelsResp, "g1-pro-tier", "PRO", loadResp)

	require.Len(t, info.AICredits, 1)
	require.Equal(t, "GOOGLE_ONE_AI", info.AICredits[0].CreditType)
	require.Equal(t, 25.0, info.AICredits[0].Amount)
	require.Equal(t, 5.0, info.AICredits[0].MinimumBalance)
}

// TestBuildUsageInfoWithSummary_GroupsWeeklyAndFiveHourWindows exercises
// official retrieveUserQuotaSummary groups including a proto3 zero-omitted
// remainingFraction bucket, verifying converted used-percentage windows and
// CloneUsageInfo isolation.
func TestBuildUsageInfoWithSummary_GroupsWeeklyAndFiveHourWindows(t *testing.T) {
	fetcher := &AntigravityQuota{}
	modelsResp := &antigravity.FetchAvailableModelsResponse{
		Models: map[string]antigravity.ModelInfo{},
	}
	rem12 := 0.12
	rem100 := 1.0
	rem50 := 0.50
	summaryResp := &antigravity.RetrieveUserQuotaSummaryResponse{
		Groups: []google.AntigravityQuotaSummaryGroup{
			{
				DisplayName: "Gemini Models",
				Buckets: []google.AntigravityQuotaSummaryBucket{
					{
						BucketID:          "gemini-weekly",
						Window:            "weekly",
						RemainingFraction: &rem12,
						ResetTime:         "2026-11-25T18:00:00Z",
					},
					{
						BucketID:          "gemini-5h",
						Window:            "5h",
						RemainingFraction: &rem100,
						ResetTime:         "2026-11-20T22:00:00Z",
					},
					{
						BucketID:          "gemini-argon-wtu-weekly",
						Window:            "weekly",
						RemainingFraction: &rem50,
						ResetTime:         "2026-11-25T18:00:00Z",
					},
				},
			},
			{
				DisplayName: "Claude and GPT models",
				Buckets: []google.AntigravityQuotaSummaryBucket{
					{
						BucketID:          "3p-weekly",
						Window:            "weekly",
						RemainingFraction: &rem50,
						ResetTime:         "2026-11-25T18:00:00Z",
					},
					{
						BucketID:  "3p-5h",
						Window:    "5h",
						ResetTime: "2026-11-20T22:00:00Z",
					},
				},
			},
		},
	}

	info := fetcher.BuildUsageInfoWithSummary(modelsResp, summaryResp, "g1-pro-tier", "PRO", nil)
	require.NotNil(t, info.AntigravityQuotaSummary)
	require.NotNil(t, info.AntigravityQuotaSummary.Gemini)
	require.NotNil(t, info.AntigravityQuotaSummary.ClaudeGPT)
	require.Equal(t, &AntigravityModelQuota{Utilization: 88, ResetTime: "2026-11-25T18:00:00Z"}, info.AntigravityQuotaSummary.Gemini.Weekly)
	require.Equal(t, &AntigravityModelQuota{Utilization: 0, ResetTime: "2026-11-20T22:00:00Z"}, info.AntigravityQuotaSummary.Gemini.FiveHour)
	require.Equal(t, &AntigravityModelQuota{Utilization: 50, ResetTime: "2026-11-25T18:00:00Z"}, info.AntigravityQuotaSummary.ClaudeGPT.Weekly)
	require.Equal(t, &AntigravityModelQuota{Utilization: 100, ResetTime: "2026-11-20T22:00:00Z"}, info.AntigravityQuotaSummary.ClaudeGPT.FiveHour)

	cloned := CloneUsageInfo(info)
	cloned.AntigravityQuotaSummary.Gemini.Weekly.Utilization = 10
	require.Equal(t, 88, info.AntigravityQuotaSummary.Gemini.Weekly.Utilization)
}
