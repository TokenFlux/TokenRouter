package googleforward

// 以下入口只在测试构建中开放私有边界，生产接口不因测试迁移扩大。
var ResolveProjectForTest = resolveAntigravityProjectID
var EnsureSignatureForTest = ensureGeminiFunctionCallThoughtSignatures
var ConvertClaudeForTest = convertClaudeMessagesToGeminiGenerateContent
var GeminiResponseForTest = (*Gemini).geminiResponseAdapter
var AntigravityResponseForTest = (*Antigravity).antigravityResponseAdapter
var ErrorBodyLimitForTest = (*Antigravity).upstreamErrorBodyReadLimit
var GeminiFailoverForTest = (*Gemini).shouldFailoverGeminiUpstreamError
var GeminiPolicyForTest = (*Gemini).applyGeminiUpstreamErrorPolicy
var AntigravityBodyForTest = (*Antigravity).buildAntigravityCompatGeminiBody

// AttemptForTest 允许断言状态复位及观测结果，仍使用同一实际状态类型。
type AttemptForTest = attempt

func (a *attempt) ObserveImagesForTest(body []byte) { a.observeImages(body) }
func (a *attempt) ImageCountForTest(original, mapped string) int {
	return a.imageCount(original, mapped)
}
