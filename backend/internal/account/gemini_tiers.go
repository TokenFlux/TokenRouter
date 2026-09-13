// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	strings "strings"
)

const (
	// 现有规范等级 ID，保持已存储字符串不变。
	GeminiTierGoogleOneFree    = "google_one_free"
	GeminiTierGoogleAIPro      = "google_ai_pro"
	GeminiTierGoogleAIUltra    = "google_ai_ultra"
	GeminiTierGCPStandard      = "gcp_standard"
	GeminiTierGCPEnterprise    = "gcp_enterprise"
	GeminiTierAIStudioFree     = "aistudio_free"
	GeminiTierAIStudioPaid     = "aistudio_paid"
	GeminiTierGoogleOneUnknown = "google_one_unknown"

	// 历史数据与上游响应中的兼容等级 ID。
	LegacyTierAIPremium          = "AI_PREMIUM"
	LegacyTierGoogleOneStandard  = "GOOGLE_ONE_STANDARD"
	LegacyTierGoogleOneBasic     = "GOOGLE_ONE_BASIC"
	LegacyTierFree               = "FREE"
	LegacyTierGoogleOneUnknown   = "GOOGLE_ONE_UNKNOWN"
	LegacyTierGoogleOneUnlimited = "GOOGLE_ONE_UNLIMITED"
)

func CanonicalGeminiTierID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}

	lower := strings.ToLower(raw)
	switch lower {
	case GeminiTierGoogleOneFree,
		GeminiTierGoogleAIPro,
		GeminiTierGoogleAIUltra,
		GeminiTierGCPStandard,
		GeminiTierGCPEnterprise,
		GeminiTierAIStudioFree,
		GeminiTierAIStudioPaid,
		GeminiTierGoogleOneUnknown:
		return lower
	}

	upper := strings.ToUpper(raw)
	switch upper {
	// Google One 的历史等级。
	case LegacyTierAIPremium:
		return GeminiTierGoogleAIPro
	case LegacyTierGoogleOneUnlimited:
		return GeminiTierGoogleAIUltra
	case LegacyTierFree, LegacyTierGoogleOneBasic, LegacyTierGoogleOneStandard:
		return GeminiTierGoogleOneFree
	case LegacyTierGoogleOneUnknown:
		return GeminiTierGoogleOneUnknown

	// Code Assist 的历史等级。
	case "STANDARD", "PRO", "LEGACY":
		return GeminiTierGCPStandard
	case "ENTERPRISE", "ULTRA":
		return GeminiTierGCPEnterprise
	}

	// 部分 Code Assist 响应使用短横线分隔的等级标识。
	switch lower {
	case "standard-tier", "pro-tier":
		return GeminiTierGCPStandard
	case "ultra-tier":
		return GeminiTierGCPEnterprise
	}

	return ""
}

func CanonicalGeminiTierIDForOAuthType(oauthType, tierID string) string {
	oauthType = strings.ToLower(strings.TrimSpace(oauthType))
	canonical := CanonicalGeminiTierID(tierID)
	if canonical == "" {
		return ""
	}

	switch oauthType {
	case "google_one":
		switch canonical {
		case GeminiTierGoogleOneFree, GeminiTierGoogleAIPro, GeminiTierGoogleAIUltra:
			return canonical
		default:
			return ""
		}
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
		// 未知 OAuth 类型继续接受规范等级。
		return canonical
	}
}
