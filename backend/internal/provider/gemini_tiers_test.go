package provider

import (
	"testing"
)

// gemini_tiers_test.go verifies canonical Gemini tier mapping and OAuth-variant
// family filtering for Code Assist and AI Studio accounts.

// TestCanonicalGeminiTierID varies current, uppercase, hyphenated, padded,
// mixed-case, blank, and unknown labels. Every table case checks its exact
// canonical identifier or empty unresolved result.
func TestCanonicalGeminiTierID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string // tier normalization case name
		raw  string // input tier identifier text
		want string // expected canonical tier identifier
	}{
		{name: "empty string", raw: "", want: ""},
		{name: "whitespace only", raw: "   ", want: ""},

		// Canonical IDs pass through.
		{name: "gcp_standard", raw: "gcp_standard", want: GeminiTierGCPStandard},
		{name: "gcp_enterprise", raw: "gcp_enterprise", want: GeminiTierGCPEnterprise},
		{name: "aistudio_free", raw: "aistudio_free", want: GeminiTierAIStudioFree},
		{name: "aistudio_paid", raw: "aistudio_paid", want: GeminiTierAIStudioPaid},

		// Tier IDs are case insensitive.
		{name: "GCP_STANDARD uppercase", raw: "GCP_STANDARD", want: GeminiTierGCPStandard},
		{name: "AISTUDIO_PAID uppercase", raw: "AISTUDIO_PAID", want: GeminiTierAIStudioPaid},

		// Uppercase Code Assist aliases.
		{name: "STANDARD -> gcp_standard", raw: "STANDARD", want: GeminiTierGCPStandard},
		{name: "PRO -> gcp_standard", raw: "PRO", want: GeminiTierGCPStandard},
		{name: "LEGACY -> gcp_standard", raw: "LEGACY", want: GeminiTierGCPStandard},
		{name: "FREE -> gcp_standard", raw: "FREE", want: GeminiTierGCPStandard},
		{name: "ENTERPRISE -> gcp_enterprise", raw: "ENTERPRISE", want: GeminiTierGCPEnterprise},
		{name: "ULTRA -> gcp_enterprise", raw: "ULTRA", want: GeminiTierGCPEnterprise},

		// Hyphenated upstream v1internal IDs.
		{name: "standard-tier -> gcp_standard", raw: "standard-tier", want: GeminiTierGCPStandard},
		{name: "pro-tier -> gcp_standard", raw: "pro-tier", want: GeminiTierGCPStandard},
		{name: "legacy-tier -> gcp_standard", raw: "legacy-tier", want: GeminiTierGCPStandard},
		{name: "free-tier -> gcp_standard", raw: "free-tier", want: GeminiTierGCPStandard},
		{name: "ultra-tier -> gcp_enterprise", raw: "ultra-tier", want: GeminiTierGCPEnterprise},
		{name: "enterprise-tier -> gcp_enterprise", raw: "enterprise-tier", want: GeminiTierGCPEnterprise},

		// Unknown IDs are rejected.
		{name: "unknown_value -> empty", raw: "unknown_value", want: ""},
		{name: "random-text -> empty", raw: "random-text", want: ""},

		// Surrounding whitespace is ignored.
		{name: "trimmed whitespace", raw: "  gcp_standard  ", want: GeminiTierGCPStandard},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := CanonicalGeminiTierID(tt.raw)
			if got != tt.want {
				t.Fatalf("CanonicalGeminiTierID(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

// TestCanonicalGeminiTierIDForOAuthType combines variant families with
// canonical and hyphenated tiers. It checks compatible labels, foreign-family
// rejection, unknown-variant pass-through, and case/whitespace normalization.
func TestCanonicalGeminiTierIDForOAuthType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string // tier normalization case name
		oauthType string // input OAuth variant text
		tierID    string // input account tier text
		want      string // expected canonical tier identifier
	}{
		// Code Assist rejects tiers belonging to AI Studio.
		{name: "code_assist + gcp_standard", oauthType: "code_assist", tierID: "gcp_standard", want: GeminiTierGCPStandard},
		{name: "code_assist + gcp_enterprise", oauthType: "code_assist", tierID: "gcp_enterprise", want: GeminiTierGCPEnterprise},
		{name: "code_assist + legacy-tier", oauthType: "code_assist", tierID: "legacy-tier", want: GeminiTierGCPStandard},
		{name: "code_assist + free-tier", oauthType: "code_assist", tierID: "free-tier", want: GeminiTierGCPStandard},
		{name: "code_assist + aistudio_free filtered", oauthType: "code_assist", tierID: "aistudio_free", want: ""},
		{name: "code_assist + STANDARD uppercase", oauthType: "code_assist", tierID: "STANDARD", want: GeminiTierGCPStandard},
		{name: "code_assist + standard-tier kebab", oauthType: "code_assist", tierID: "standard-tier", want: GeminiTierGCPStandard},

		// AI Studio accepts its free and paid tiers.
		{name: "ai_studio + aistudio_free", oauthType: "ai_studio", tierID: "aistudio_free", want: GeminiTierAIStudioFree},
		{name: "ai_studio + aistudio_paid", oauthType: "ai_studio", tierID: "aistudio_paid", want: GeminiTierAIStudioPaid},
		{name: "ai_studio + gcp_standard filtered", oauthType: "ai_studio", tierID: "gcp_standard", want: ""},

		// An unspecified or unknown OAuth type accepts a recognized tier.
		{name: "empty tierID", oauthType: "code_assist", tierID: "", want: ""},
		{name: "empty oauthType + valid tierID", oauthType: "", tierID: "gcp_standard", want: GeminiTierGCPStandard},
		{name: "unknown oauthType accepts canonical value", oauthType: "unknown_type", tierID: "gcp_standard", want: GeminiTierGCPStandard},

		// OAuth type matching ignores case and surrounding whitespace.
		{name: "CODE_ASSIST uppercase", oauthType: "CODE_ASSIST", tierID: "gcp_standard", want: GeminiTierGCPStandard},
		{name: "oauthType with whitespace", oauthType: "  code_assist  ", tierID: "gcp_standard", want: GeminiTierGCPStandard},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := CanonicalGeminiTierIDForOAuthType(tt.oauthType, tt.tierID)
			if got != tt.want {
				t.Fatalf("CanonicalGeminiTierIDForOAuthType(%q, %q) = %q, want %q", tt.oauthType, tt.tierID, got, tt.want)
			}
		})
	}
}
