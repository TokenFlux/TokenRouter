package provider

import (
	"strings"
)

// gemini_tiers.go normalizes Gemini account-tier identifiers and filters them
// by OAuth variant.
// Gemini account tier labels arrive as canonical IDs, uppercase Code Assist
// names, and hyphenated v1internal tier identifiers. Normalization maps those
// aliases to local credential IDs, then OAuth-specific filtering rejects
// recognized tiers belonging to another variant while allowing unknown variants
// to carry any recognized ID.
//
// Code Assist accepts GCP Standard and Enterprise tiers, while AI Studio
// accepts Free and Paid tiers. Empty or unknown variant text accepts recognized
// labels from either family, leaving the owning caller to apply its default;
// quota values are selected separately by GeminiQuotaPolicy.

// Gemini tier constants name stored canonical labels used by Code Assist and AI
// Studio authorization discovery, quota policy lookup, and credential normalization.
const (
	GeminiTierGCPStandard   = "gcp_standard"   // Code Assist standard account
	GeminiTierGCPEnterprise = "gcp_enterprise" // Code Assist enterprise account
	GeminiTierAIStudioFree  = "aistudio_free"  // AI Studio free account
	GeminiTierAIStudioPaid  = "aistudio_paid"  // AI Studio paid account
)

// CanonicalGeminiTierID trims input and maps recognized canonical, uppercase,
// or hyphenated Code Assist tier text to a local ID. Matching ignores case, and
// blank or unknown text returns empty.
func CanonicalGeminiTierID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	lower := strings.ToLower(raw)
	switch lower {
	case GeminiTierGCPStandard,
		GeminiTierGCPEnterprise,
		GeminiTierAIStudioFree,
		GeminiTierAIStudioPaid:
		return lower
	}

	// Uppercase Code Assist labels map to their corresponding GCP tier family.
	switch strings.ToUpper(raw) {
	case "STANDARD", "PRO", "LEGACY", "FREE":
		return GeminiTierGCPStandard
	case "ENTERPRISE", "ULTRA":
		return GeminiTierGCPEnterprise
	}

	// Hyphenated v1internal Code Assist IDs map to the same canonical GCP families.
	switch lower {
	case "standard-tier", "pro-tier", "legacy-tier", "free-tier":
		return GeminiTierGCPStandard
	case "ultra-tier", "enterprise-tier":
		return GeminiTierGCPEnterprise
	}

	return ""
}

// CanonicalGeminiTierIDForOAuthType normalizes tier text and filters it for
// the trimmed lowercase OAuth variant. Unknown or empty variants accept any
// recognized ID; known variants reject foreign-family tiers.
//
// Code Assist accepts GCP Standard and Enterprise, and AI Studio accepts Free
// and Paid. No quota values are inferred here; callers use the resulting
// identifier for their own policy or metadata selection.
func CanonicalGeminiTierIDForOAuthType(oauthType, tierID string) string {
	oauthType = strings.ToLower(strings.TrimSpace(oauthType))
	canonical := CanonicalGeminiTierID(tierID)
	if canonical == "" {
		return ""
	}

	switch oauthType {
	case "code_assist":
		switch canonical {
		case GeminiTierGCPStandard, GeminiTierGCPEnterprise:
			return canonical
		default:
			return ""
		}
	case "ai_studio":
		switch canonical {
		case GeminiTierAIStudioFree, GeminiTierAIStudioPaid:
			return canonical
		default:
			return ""
		}
	default:
		// Blank or unknown variants leave a recognized tier available for the
		// owning authorization caller to interpret before applying its variant policy.
		return canonical
	}
}
