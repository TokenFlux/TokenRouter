package bridge

import (
	"encoding/json"
	"fmt"
	"strings"
)

// GeminiToAnthropicResponseProcessor 非流式响应处理器
type GeminiToAnthropicResponseProcessor struct {
	runtime           GeminiConversionRuntime
	diagnostics       []string
	contentBlocks     []ClaudeContentItem
	textBuilder       string
	thinkingBuilder   string
	thinkingSignature string
	trailingSignature string
	hasToolCall       bool
}

// NewGeminiToAnthropicResponseProcessor 创建非流式响应处理器
func NewGeminiToAnthropicResponseProcessor(runtime GeminiConversionRuntime) *GeminiToAnthropicResponseProcessor {
	return &GeminiToAnthropicResponseProcessor{runtime: runtime,
		contentBlocks: make([]ClaudeContentItem, 0),
	}
}

// Process 处理 Gemini 响应
func (p *GeminiToAnthropicResponseProcessor) Process(geminiResp *GeminiResponse, responseID, originalModel string) *ClaudeResponse {
	// 获取 parts
	var parts []GeminiPart
	if len(geminiResp.Candidates) > 0 && geminiResp.Candidates[0].Content != nil {
		parts = geminiResp.Candidates[0].Content.Parts
	}

	// 处理所有 parts
	for _, part := range parts {
		p.processPart(&part)
	}

	if len(geminiResp.Candidates) > 0 {
		if grounding := geminiResp.Candidates[0].GroundingMetadata; grounding != nil {
			p.processGrounding(grounding)
		}
	}

	// 刷新剩余内容
	p.flushThinking()
	p.flushText()

	// 处理 trailingSignature
	if p.trailingSignature != "" {
		p.contentBlocks = append(p.contentBlocks, ClaudeContentItem{
			Type:      "thinking",
			Thinking:  "",
			Signature: p.trailingSignature,
		})
	}

	// 构建响应
	return p.buildResponse(geminiResp, responseID, originalModel)
}

// processPart 处理单个 part
func (p *GeminiToAnthropicResponseProcessor) processPart(part *GeminiPart) {
	signature := part.ThoughtSignature

	// 1. FunctionCall 处理
	if part.FunctionCall != nil {
		p.flushThinking()
		p.flushText()

		// 处理 trailingSignature
		if p.trailingSignature != "" {
			p.contentBlocks = append(p.contentBlocks, ClaudeContentItem{
				Type:      "thinking",
				Thinking:  "",
				Signature: p.trailingSignature,
			})
			p.trailingSignature = ""
		}

		p.hasToolCall = true

		// 生成 tool_use id
		toolID := part.FunctionCall.ID
		if toolID == "" {
			toolID = fmt.Sprintf("%s-%s", part.FunctionCall.Name, p.runtime.RandomID())
		}

		item := ClaudeContentItem{
			Type:  "tool_use",
			ID:    toolID,
			Name:  part.FunctionCall.Name,
			Input: part.FunctionCall.Args,
		}

		if signature != "" {
			item.Signature = signature
		}

		p.contentBlocks = append(p.contentBlocks, item)
		return
	}

	// 2. Text 处理
	if part.Text != "" || part.Thought {
		if part.Thought {
			// Thinking part
			p.flushText()

			// 处理 trailingSignature
			if p.trailingSignature != "" {
				p.flushThinking()
				p.contentBlocks = append(p.contentBlocks, ClaudeContentItem{
					Type:      "thinking",
					Thinking:  "",
					Signature: p.trailingSignature,
				})
				p.trailingSignature = ""
			}

			p.thinkingBuilder += part.Text
			if signature != "" {
				p.thinkingSignature = signature
			}
		} else {
			// 普通 Text
			if part.Text == "" {
				// 空 text 带签名 - 暂存
				if signature != "" {
					p.trailingSignature = signature
				}
				return
			}

			p.flushThinking()

			// 处理之前的 trailingSignature
			if p.trailingSignature != "" {
				p.flushText()
				p.contentBlocks = append(p.contentBlocks, ClaudeContentItem{
					Type:      "thinking",
					Thinking:  "",
					Signature: p.trailingSignature,
				})
				p.trailingSignature = ""
			}

			// 非空 text 带签名 - 特殊处理：先输出 text，再输出空 thinking 块
			if signature != "" {
				p.contentBlocks = append(p.contentBlocks, ClaudeContentItem{
					Type: "text",
					Text: part.Text,
				})
				p.contentBlocks = append(p.contentBlocks, ClaudeContentItem{
					Type:      "thinking",
					Thinking:  "",
					Signature: signature,
				})
			} else {
				// 普通 text (无签名) - 累积到 builder
				p.textBuilder += part.Text
			}
		}
	}

	// 3. InlineData (Image) 处理
	if part.InlineData != nil && part.InlineData.Data != "" {
		p.flushThinking()
		markdownImg := fmt.Sprintf("![image](data:%s;base64,%s)",
			part.InlineData.MimeType, part.InlineData.Data)
		p.textBuilder += markdownImg
		p.flushText()
	}
}

func (p *GeminiToAnthropicResponseProcessor) processGrounding(grounding *GeminiGroundingMetadata) {
	groundingText := buildGroundingText(grounding)
	if groundingText == "" {
		return
	}

	p.flushThinking()
	p.flushText()
	p.textBuilder += groundingText
	p.flushText()
}

// flushText 刷新 text builder
func (p *GeminiToAnthropicResponseProcessor) flushText() {
	if p.textBuilder == "" {
		return
	}

	p.contentBlocks = append(p.contentBlocks, ClaudeContentItem{
		Type: "text",
		Text: p.textBuilder,
	})
	p.textBuilder = ""
}

// flushThinking 刷新 thinking builder
func (p *GeminiToAnthropicResponseProcessor) flushThinking() {
	if p.thinkingBuilder == "" && p.thinkingSignature == "" {
		return
	}

	p.contentBlocks = append(p.contentBlocks, ClaudeContentItem{
		Type:      "thinking",
		Thinking:  p.thinkingBuilder,
		Signature: p.thinkingSignature,
	})
	p.thinkingBuilder = ""
	p.thinkingSignature = ""
}

// buildResponse 构建最终响应
func (p *GeminiToAnthropicResponseProcessor) buildResponse(geminiResp *GeminiResponse, responseID, originalModel string) *ClaudeResponse {
	var finishReason string
	if len(geminiResp.Candidates) > 0 {
		finishReason = geminiResp.Candidates[0].FinishReason
		if finishReason == "MALFORMED_FUNCTION_CALL" {
			p.addDiagnostic("[Antigravity] MALFORMED_FUNCTION_CALL detected in response for model %s", originalModel)
			if geminiResp.Candidates[0].Content != nil {
				if b, err := json.Marshal(geminiResp.Candidates[0].Content); err == nil {
					p.addDiagnostic("[Antigravity] Malformed content: %s", string(b))
				}
			}
		}
	}

	stopReason := "end_turn"
	if p.hasToolCall {
		stopReason = "tool_use"
	} else if finishReason == "MAX_TOKENS" {
		stopReason = "max_tokens"
	}

	// 注意：Gemini 的 promptTokenCount 包含 cachedContentTokenCount，
	// 但 Claude 的 input_tokens 不包含 cache_read_input_tokens，需要减去
	usage := ClaudeUsage{}
	if geminiResp.UsageMetadata != nil {
		cached := geminiResp.UsageMetadata.CachedContentTokenCount
		usage.InputTokens = geminiResp.UsageMetadata.PromptTokenCount - cached
		usage.OutputTokens = geminiResp.UsageMetadata.CandidatesTokenCount + geminiResp.UsageMetadata.ThoughtsTokenCount
		usage.CacheReadInputTokens = cached
		usage.ImageOutputTokens = geminiResp.UsageMetadata.ImageOutputTokens()
	}

	// 生成响应 ID
	respID := responseID
	if respID == "" {
		respID = geminiResp.ResponseID
	}
	if respID == "" {
		respID = p.runtime.MessageID()
	}

	return &ClaudeResponse{
		ID:         respID,
		Type:       "message",
		Role:       "assistant",
		Model:      originalModel,
		Content:    p.contentBlocks,
		StopReason: stopReason,
		Usage:      usage,
	}
}

func buildGroundingText(grounding *GeminiGroundingMetadata) string {
	if grounding == nil {
		return ""
	}

	var builder strings.Builder

	if len(grounding.WebSearchQueries) > 0 {
		_, _ = builder.WriteString("\n\n---\nWeb search queries: ")
		_, _ = builder.WriteString(strings.Join(grounding.WebSearchQueries, ", "))
	}

	if len(grounding.GroundingChunks) > 0 {
		var links []string
		for i, chunk := range grounding.GroundingChunks {
			if chunk.Web == nil {
				continue
			}
			title := strings.TrimSpace(chunk.Web.Title)
			if title == "" {
				title = "Source"
			}
			uri := strings.TrimSpace(chunk.Web.URI)
			if uri == "" {
				uri = "#"
			}
			links = append(links, fmt.Sprintf("[%d] [%s](%s)", i+1, title, uri))
		}

		if len(links) > 0 {
			_, _ = builder.WriteString("\n\nSources:\n")
			_, _ = builder.WriteString(strings.Join(links, "\n"))
		}
	}

	return builder.String()
}

// GeminiConversionRuntime 由平台提供原有 ID 生成器，转换状态机不读取随机源。
type GeminiConversionRuntime struct {
	RandomID  func() string
	MessageID func() string
}

// TakeDiagnostics 交回本轮纯转换诊断，由平台适配按原日志格式输出。
func (p *GeminiToAnthropicResponseProcessor) TakeDiagnostics() []string {
	out := p.diagnostics
	p.diagnostics = nil
	return out
}
func (p *GeminiToAnthropicResponseProcessor) addDiagnostic(format string, args ...any) {
	p.diagnostics = append(p.diagnostics, fmt.Sprintf(format, args...))
}
