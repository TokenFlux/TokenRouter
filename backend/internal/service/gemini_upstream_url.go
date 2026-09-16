// 旧 Gemini URL 构造只转交原生平台，目标策略仍由外层提供。
package service

import gemininative "github.com/TokenFlux/TokenRouter/internal/upstream/gemini"

func buildGeminiAIStudioModelActionURL(baseURL, model, action string, stream bool) (string, error) {
	return gemininative.BuildGeminiAIStudioModelActionURL(baseURL, model, action, stream)
}
func IsSafeGeminiModelPathSegment(model string) bool {
	return gemininative.IsSafeGeminiModelPathSegment(model)
}
