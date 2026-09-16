//go:build unit

// 白盒测试保留必要旧名称，生产入口已使用唯一所属实现。
package service

import (
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	gemininative "github.com/TokenFlux/TokenRouter/internal/upstream/gemini"
	geminicli "github.com/TokenFlux/TokenRouter/internal/upstream/gemini/codeassist"
)

func parseCreativeGeminiImageOutputs(body []byte) ([]CreativeOutput, error) {
	return gemininative.ParseImageOutputs(body, func(status int, message string) error { return creativeHTTPStatusError(status, message) })
}
func validateTierID(tierID string) error { return accountcore.ValidateTierID(tierID) }
func canonicalGeminiTierIDForOAuthType(oauthType, tierID string) string {
	return accountcore.CanonicalGeminiTierIDForOAuthType(oauthType, tierID)
}
func extractTierIDFromAllowedTiers(allowedTiers []geminicli.AllowedTier) string {
	return accountcore.ExtractTierIDFromAllowedTiers(allowedTiers)
}
func inferGoogleOneTier(storageBytes int64) string {
	return accountcore.InferGoogleOneTier(storageBytes, func(format string, args ...any) { logger.LegacyPrintf("service.gemini_oauth", format, args...) })
}
func isNonRetryableGeminiOAuthError(err error) bool {
	return accountcore.IsNonRetryableGeminiOAuthError(err)
}
