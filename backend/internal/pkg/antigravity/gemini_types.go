package antigravity

import (
	"github.com/TokenFlux/TokenRouter/internal/protocol/gemini"
)

// 通用 wire 类型只保留别名；默认模型、安全配置及 v1internal 包装仍由平台拥有。

// V1InternalRequest v1internal 请求包装
type V1InternalRequest struct {
	Project     string        `json:"project"`
	RequestID   string        `json:"requestId"`
	UserAgent   string        `json:"userAgent"`
	RequestType string        `json:"requestType,omitempty"`
	Model       string        `json:"model"`
	Request     GeminiRequest `json:"request"`
}

type GeminiRequest = gemini.GeminiRequest

type GeminiContent = gemini.GeminiContent

type GeminiPart = gemini.GeminiPart

type GeminiInlineData = gemini.GeminiInlineData

type GeminiFunctionCall = gemini.GeminiFunctionCall

type GeminiFunctionResponse = gemini.GeminiFunctionResponse

type GeminiGenerationConfig = gemini.GeminiGenerationConfig

type GeminiImageConfig = gemini.GeminiImageConfig

type GeminiThinkingConfig = gemini.GeminiThinkingConfig

type GeminiToolDeclaration = gemini.GeminiToolDeclaration

type GeminiCodeExecution = gemini.GeminiCodeExecution

type GeminiFunctionDecl = gemini.GeminiFunctionDecl

type GeminiGoogleSearch = gemini.GeminiGoogleSearch

type GeminiEnhancedContent = gemini.GeminiEnhancedContent

type GeminiImageSearch = gemini.GeminiImageSearch

type GeminiToolConfig = gemini.GeminiToolConfig

type GeminiFunctionCallingConfig = gemini.GeminiFunctionCallingConfig

type GeminiSafetySetting = gemini.GeminiSafetySetting

// V1InternalResponse v1internal 响应包装
type V1InternalResponse struct {
	Response     GeminiResponse `json:"response"`
	ResponseID   string         `json:"responseId,omitempty"`
	ModelVersion string         `json:"modelVersion,omitempty"`
}

type GeminiResponse = gemini.GeminiResponse

type GeminiCandidate = gemini.GeminiCandidate

type GeminiTokenDetail = gemini.GeminiTokenDetail

type GeminiUsageMetadata = gemini.GeminiUsageMetadata

type GeminiGroundingMetadata = gemini.GeminiGroundingMetadata

type GeminiGroundingChunk = gemini.GeminiGroundingChunk

type GeminiGroundingWeb = gemini.GeminiGroundingWeb

// DefaultSafetySettings 默认安全设置（关闭所有过滤）
var DefaultSafetySettings = []GeminiSafetySetting{
	{Category: "HARM_CATEGORY_HARASSMENT", Threshold: "OFF"},
	{Category: "HARM_CATEGORY_HATE_SPEECH", Threshold: "OFF"},
	{Category: "HARM_CATEGORY_SEXUALLY_EXPLICIT", Threshold: "OFF"},
	{Category: "HARM_CATEGORY_DANGEROUS_CONTENT", Threshold: "OFF"},
	{Category: "HARM_CATEGORY_CIVIC_INTEGRITY", Threshold: "OFF"},
}

// DefaultStopSequences 默认停止序列
var DefaultStopSequences = []string{
	"<|user|>",
	"<|endoftext|>",
	"<|end_of_turn|>",
	"\n\nHuman:",
}
