package bridge

import (
	"github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/protocol/gemini"
)

// 兼容 wire 变体保持原字段与省略语义，桥接不复制其编解码。

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

type ClaudeModel = anthropic.ClaudeModel

type GeminiModel = gemini.GeminiModel

type GeminiModelsListResponse = gemini.GeminiModelsListResponse

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

type GeminiResponse = gemini.GeminiResponse

type GeminiCandidate = gemini.GeminiCandidate

type GeminiTokenDetail = gemini.GeminiTokenDetail

type GeminiUsageMetadata = gemini.GeminiUsageMetadata

type GeminiGroundingMetadata = gemini.GeminiGroundingMetadata

type GeminiGroundingChunk = gemini.GeminiGroundingChunk

type GeminiGroundingWeb = gemini.GeminiGroundingWeb
