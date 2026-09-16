package antigravity

import (
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/protocol/gemini"
)

// 通用 wire 类型只保留别名；默认模型、安全配置及 v1internal 包装仍由平台拥有。

type ClaudeRequest = anthropic.ClaudeRequest

type ClaudeMessage = anthropic.ClaudeMessage

type ThinkingConfig = anthropic.ThinkingConfig

type ClaudeMetadata = anthropic.ClaudeMetadata

type ClaudeTool = anthropic.ClaudeTool

type CustomToolSpec = anthropic.CustomToolSpec

type ClaudeCustomToolSpec = anthropic.ClaudeCustomToolSpec

type SystemBlock = anthropic.SystemBlock

type ContentBlock = anthropic.ContentBlock

type ImageSource = anthropic.ImageSource

type ClaudeResponse = anthropic.ClaudeResponse

type ClaudeContentItem = anthropic.ClaudeContentItem

type ClaudeUsage = anthropic.ClaudeUsage

type ClaudeError = anthropic.ClaudeError

type ErrorDetail = anthropic.ErrorDetail

// modelDef Antigravity 模型定义（内部使用）
type modelDef struct {
	ID          string
	DisplayName string
	CreatedAt   string // 仅 Claude API 格式使用
	IsReasoning bool
}

// Antigravity 支持的 Claude 模型
var claudeModels = []modelDef{
	{ID: "claude-fable-5-1", DisplayName: "Claude Fable 5.1", CreatedAt: "2026-09-01T00:00:00Z"},
	{ID: "claude-fable-5", DisplayName: "Claude Fable 5", CreatedAt: "2026-06-09T00:00:00Z"},
	{ID: "claude-opus-4-5-thinking", DisplayName: "Claude Opus 4.5 Thinking", CreatedAt: "2025-11-01T00:00:00Z"},
	{ID: "claude-sonnet-4-5", DisplayName: "Claude Sonnet 4.5", CreatedAt: "2025-09-29T00:00:00Z"},
	{ID: "claude-sonnet-4-5-thinking", DisplayName: "Claude Sonnet 4.5 Thinking", CreatedAt: "2025-09-29T00:00:00Z"},
	{ID: "claude-opus-4-6", DisplayName: "Claude Opus 4.6", CreatedAt: "2026-02-05T00:00:00Z"},
	{ID: "claude-opus-4-6-thinking", DisplayName: "Claude Opus 4.6 Thinking", CreatedAt: "2026-02-05T00:00:00Z"},
	{ID: "claude-opus-4-7", DisplayName: "Claude Opus 4.7", CreatedAt: "2026-04-17T00:00:00Z"},
	{ID: "claude-opus-4-8", DisplayName: "Claude Opus 4.8", CreatedAt: "2026-05-29T00:00:00Z"},
	{ID: "claude-sonnet-4-6", DisplayName: "Claude Sonnet 4.6", CreatedAt: "2026-02-17T00:00:00Z"},
}

// Antigravity 支持的 Gemini 模型
var geminiModels = []modelDef{
	{ID: "gemini-2.5-flash", DisplayName: "Gemini 2.5 Flash", CreatedAt: "2025-01-01T00:00:00Z"},
	{ID: "gemini-2.5-flash-image", DisplayName: "Gemini 2.5 Flash Image", CreatedAt: "2025-01-01T00:00:00Z"},
	{ID: "gemini-2.5-flash-image-preview", DisplayName: "Gemini 2.5 Flash Image Preview", CreatedAt: "2025-01-01T00:00:00Z"},
	{ID: "gemini-2.5-flash-lite", DisplayName: "Gemini 2.5 Flash Lite", CreatedAt: "2025-01-01T00:00:00Z"},
	{ID: "gemini-2.5-flash-thinking", DisplayName: "Gemini 2.5 Flash Thinking", CreatedAt: "2025-01-01T00:00:00Z", IsReasoning: true},
	{ID: "gemini-3-flash", DisplayName: "Gemini 3 Flash", CreatedAt: "2025-06-01T00:00:00Z"},
	{ID: "gemini-3-pro-low", DisplayName: "Gemini 3 Pro Low", CreatedAt: "2025-06-01T00:00:00Z"},
	{ID: "gemini-3-pro-high", DisplayName: "Gemini 3 Pro High", CreatedAt: "2025-06-01T00:00:00Z", IsReasoning: true},
	{ID: "gemini-3.1-pro-low", DisplayName: "Gemini 3.1 Pro Low", CreatedAt: "2026-02-19T00:00:00Z"},
	{ID: "gemini-3.1-pro-high", DisplayName: "Gemini 3.1 Pro High", CreatedAt: "2026-02-19T00:00:00Z", IsReasoning: true},
	{ID: "gemini-3.1-flash-image", DisplayName: "Gemini 3.1 Flash Image", CreatedAt: "2026-02-19T00:00:00Z"},
	{ID: "gemini-3.1-flash-image-preview", DisplayName: "Gemini 3.1 Flash Image Preview", CreatedAt: "2026-02-19T00:00:00Z"},
	{ID: "gemini-3.6-flash", DisplayName: "Gemini 3.6 Flash", CreatedAt: "2026-07-21T00:00:00Z"},
	{ID: "gemini-3.6-flash-high", DisplayName: "Gemini 3.6 Flash High", CreatedAt: "2026-07-21T00:00:00Z", IsReasoning: true},
	{ID: "gemini-3.6-flash-low", DisplayName: "Gemini 3.6 Flash Low", CreatedAt: "2026-07-21T00:00:00Z", IsReasoning: true},
	{ID: "gemini-3.6-flash-medium", DisplayName: "Gemini 3.6 Flash Medium", CreatedAt: "2026-07-21T00:00:00Z", IsReasoning: true},
	{ID: "gemini-3.6-flash-tiered", DisplayName: "Gemini 3.6 Flash", CreatedAt: "2026-07-21T00:00:00Z", IsReasoning: true},
	{ID: "gemini-3-pro-preview", DisplayName: "Gemini 3 Pro Preview", CreatedAt: "2025-06-01T00:00:00Z", IsReasoning: true},
	{ID: "gemini-3-pro-image", DisplayName: "Gemini 3 Pro Image", CreatedAt: "2025-06-01T00:00:00Z"},
}

type ClaudeModel = anthropic.ClaudeModel

// DefaultModels 返回 Claude API 格式的模型列表（Claude + Gemini）
func DefaultModels() []ClaudeModel {
	all := append(claudeModels, geminiModels...)
	result := make([]ClaudeModel, len(all))
	for i, m := range all {
		result[i] = ClaudeModel{ID: m.ID, Type: "model", DisplayName: m.DisplayName, CreatedAt: m.CreatedAt}
	}
	return result
}

type GeminiModel = gemini.GeminiModel

type GeminiModelsListResponse = gemini.GeminiModelsListResponse

var defaultGeminiMethods = []string{"generateContent", "streamGenerateContent"}

// DefaultGeminiModels 返回 Gemini v1beta 格式的模型列表（仅 Gemini 模型）
func DefaultGeminiModels() []GeminiModel {
	result := make([]GeminiModel, len(geminiModels))
	for i, m := range geminiModels {
		result[i] = GeminiModel{Name: "models/" + m.ID, DisplayName: m.DisplayName, SupportedGenerationMethods: defaultGeminiMethods}
	}
	return result
}

// FallbackGeminiModelsList 返回 Gemini v1beta 格式的模型列表响应
func FallbackGeminiModelsList() GeminiModelsListResponse {
	return GeminiModelsListResponse{Models: DefaultGeminiModels()}
}

// FallbackGeminiModel 返回单个模型信息（v1beta 格式）
func FallbackGeminiModel(model string) GeminiModel {
	if model == "" {
		return GeminiModel{Name: "models/unknown", SupportedGenerationMethods: defaultGeminiMethods}
	}
	name := model
	if len(model) < 7 || model[:7] != "models/" {
		name = "models/" + model
	}
	return GeminiModel{Name: name, SupportedGenerationMethods: defaultGeminiMethods}
}

// IsGeminiReasoningModel 判断是否为不支持参数和强制 ToolConfig 的 Gemini 推理模型
func IsGeminiReasoningModel(modelID string) bool {
	lowerID := strings.ToLower(modelID)
	for _, m := range geminiModels {
		if strings.Contains(lowerID, m.ID) && m.IsReasoning {
			return true
		}
	}
	return false
}
