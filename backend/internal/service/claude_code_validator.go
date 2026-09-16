// 旧 HTTP 入口只投影识别输入，文本规则唯一位于 gateway/clientmeta。
package service

import (
	"context"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/gateway/clientmeta"
	"github.com/TokenFlux/TokenRouter/internal/pkg/ctxkey"
)

type ClaudeCodeValidator struct{ clientmeta.ClaudeCodeValidator }

func NewClaudeCodeValidator() *ClaudeCodeValidator { return &ClaudeCodeValidator{} }
func (v *ClaudeCodeValidator) Validate(r *http.Request, body map[string]any) bool {
	bypass, _ := IsMaxTokensOneHaikuRequestFromContext(r.Context())
	return v.ClaudeCodeValidator.Validate(clientmeta.ClaudeCodeValidationInput{Path: r.URL.Path, UserAgent: r.Header.Get("User-Agent"), XApp: r.Header.Get("X-App"), AnthropicBeta: r.Header.Get("anthropic-beta"), AnthropicVersion: r.Header.Get("anthropic-version"), MaxTokensOneHaiku: bypass}, body)
}
func (v *ClaudeCodeValidator) bestSimilarityScore(text string) float64 {
	return v.BestSimilarityScore(text)
}

const systemPromptThreshold = clientmeta.ClaudeCodeSystemPromptThreshold
const claudeCodeSecurityMonitorPromptPrefix = clientmeta.ClaudeCodeSecurityMonitorPrefix

// IsClaudeCodeClient 从 context 中获取 Claude Code 客户端标识
func IsClaudeCodeClient(ctx context.Context) bool {
	if v, ok := ctx.Value(ctxkey.IsClaudeCodeClient).(bool); ok {
		return v
	}
	return false
}

// SetClaudeCodeClient 将 Claude Code 客户端标识设置到 context 中
func SetClaudeCodeClient(ctx context.Context, isClaudeCode bool) context.Context {
	return context.WithValue(ctx, ctxkey.IsClaudeCodeClient, isClaudeCode)
}

// SetClaudeCodeVersion 将 Claude Code 版本号设置到 context 中
func SetClaudeCodeVersion(ctx context.Context, version string) context.Context {
	return context.WithValue(ctx, ctxkey.ClaudeCodeVersion, version)
}

// GetClaudeCodeVersion 从 context 中获取 Claude Code 版本号
func GetClaudeCodeVersion(ctx context.Context) string {
	if v, ok := ctx.Value(ctxkey.ClaudeCodeVersion).(string); ok {
		return v
	}
	return ""
}

func CompareVersions(a, b string) int { return clientmeta.CompareVersions(a, b) }
