//go:build unit

// 仅保留既有测试的私有兼容入口；生产实现已迁出。
package handler

import (
	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
)

import (
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"

	gemini "github.com/TokenFlux/TokenRouter/internal/upstream/gemini"

	"github.com/gin-gonic/gin"
)

// geminiCLITmpDirRegex 用于从 Gemini CLI 请求体中提取 tmp 目录的哈希值
// 匹配格式: /Users/xxx/.gemini/tmp/[64位十六进制哈希]
var geminiCLITmpDirRegex = gatewayhttp.GeminiCLITmpDirRegex

func customGeminiModelsList(group *routing.Group) (gemini.ModelsListResponse, bool) {
	value, ok := newModelDisplayHandler().CustomGeminiModelsList(apikey.GroupFromRouting(group))
	var out []gemini.Model
	if value.Models != nil {
		out = make([]gemini.Model, len(value.Models))
	}
	for i, m := range value.Models {
		out[i] = gemini.Model(m)
	}
	return gemini.ModelsListResponse{Models: out}, ok
}
func shouldFallbackGeminiModel(modelName string, res *gemini.HTTPResult) bool {
	return newModelDisplayHandler().ShouldFallbackGeminiModel(modelName, modelHTTPResponse(res))
}

// extractGeminiCLISessionHash 从 Gemini CLI 请求中提取会话标识。
// 组合 x-gemini-api-privileged-user-id header 和请求体中的 tmp 目录哈希。
//
// 会话标识生成策略：
//  1. 从请求体中提取 tmp 目录哈希（64位十六进制）
//  2. 从 header 中提取 privileged-user-id（UUID）
//  3. 组合两者生成 SHA256 哈希作为最终的会话标识
//
// 如果找不到 tmp 目录哈希，返回空字符串（不使用粘性会话）。
//
// extractGeminiCLISessionHash extracts session identifier from Gemini CLI requests.
// Combines x-gemini-api-privileged-user-id header with tmp directory hash from request body.
func extractGeminiCLISessionHash(c *gin.Context, body []byte) string {
	return gatewayhttp.ExtractGeminiCLISessionHash(c, body)
}

// safeShortPrefix 返回字符串前 n 个字符；长度不足时返回原字符串。
// 用于日志展示，避免切片越界。
func safeShortPrefix(value string, n int) string {
	return gatewayhttp.GeminiShortPrefix(value, n)
}
