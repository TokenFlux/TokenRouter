// 旧入口只投影账号配置并委托唯一 Bedrock 算法。
package service

import (
	native "github.com/TokenFlux/TokenRouter/internal/upstream/bedrock"
)

const defaultBedrockRegion = native.DefaultBedrockRegion
const featureKeyBedrockCCCompat = native.FeatureKeyBedrockCCCompat

func bedrockRuntimeRegion(account *Account) string {
	return native.BedrockRuntimeRegion(bedrockRouteInput(account, ""))
}

func ResolveBedrockModelID(account *Account, requestedModel string) (string, bool) {
	return native.ResolveBedrockModelID(bedrockRouteInput(account, requestedModel), requestedModel)
}
func BuildBedrockURL(region, modelID string, stream bool) string {
	return native.BuildBedrockURL(region, modelID, stream)
}
func PrepareBedrockRequestBody(body []byte, modelID string, betaHeader string) ([]byte, error) {
	return native.PrepareBedrockRequestBody(body, modelID, betaHeader)
}
func PrepareBedrockRequestBodyWithTokens(body []byte, modelID string, betaTokens []string, ccCompat bool) ([]byte, error) {
	return native.PrepareBedrockRequestBodyWithTokens(body, modelID, betaTokens, ccCompat)
}
func ResolveBedrockBetaTokens(betaHeader string, body []byte, modelID string) []string {
	return native.ResolveBedrockBetaTokens(betaHeader, body, modelID)
}

func removeCustomFieldFromTools(body []byte) []byte { return native.RemoveCustomFieldFromTools(body) }

func isBedrockClaude45OrNewer(modelID string) bool { return native.IsBedrockClaude45OrNewer(modelID) }
func sanitizeBedrockCacheControl(body []byte, modelID string) []byte {
	return native.SanitizeBedrockCacheControl(body, modelID)
}

func parseAnthropicBetaHeader(header string) []string { return native.ParseAnthropicBetaHeader(header) }

const bedrockContextManagementBetaToken = native.BedrockContextManagementBetaToken

func autoInjectBedrockBetaTokens(tokens []string, body []byte, modelID string) []string {
	return native.AutoInjectBedrockBetaTokens(tokens, body, modelID)
}

func filterBedrockBetaTokens(tokens []string) []string { return native.FilterBedrockBetaTokens(tokens) }

func isBedrockOpus47OrNewer(modelID string) bool { return native.IsBedrockOpus47OrNewer(modelID) }

const defaultThinkingBudgetTokens = native.DefaultThinkingBudgetTokens

func sanitizeBedrockThinking(body []byte, modelID string) []byte {
	return native.SanitizeBedrockThinking(body, modelID)
}
func sanitizeBedrockToolUseIDs(body []byte) []byte { return native.SanitizeBedrockToolUseIDs(body) }

const defaultCCMaxTokens = native.DefaultCCMaxTokens

func sanitizeBedrockCCFields(body []byte) []byte { return native.SanitizeBedrockCCFields(body) }
func sanitizeBedrockCCBetaTokens(body []byte, modelID string) []byte {
	return native.SanitizeBedrockCCBetaTokens(body, modelID)
}

func bedrockRouteInput(value *Account, model string) *native.RouteInput {
	if value == nil {
		return nil
	}
	return &native.RouteInput{Region: value.GetCredential("aws_region"), ForceGlobal: value.GetCredential("aws_force_global") == "true", Model: value.GetMappedModel(model)}
}
