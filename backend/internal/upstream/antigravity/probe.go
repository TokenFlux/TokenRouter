// 指定账号的探测使用原生客户端及同一重试规则，账号资格由调用者先确认。
package antigravity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
)

// TestConnectionResult 测试连接结果
type TestConnectionResult struct {
	Text        string // 响应文本
	MappedModel string // 实际使用的模型
}

// BuildGeminiTestRequest 构建 Gemini 格式测试请求
// 使用最小 token 消耗：使用调用方提示词并限制 maxOutputTokens 为 1。
func BuildGeminiTestRequest(projectID, model string, prompts ...string) ([]byte, error) {
	prompt := "."
	if len(prompts) > 0 && strings.TrimSpace(prompts[0]) != "" {
		prompt = strings.TrimSpace(prompts[0])
	}
	payload := map[string]any{
		"contents": []map[string]any{
			{
				"role": "user",
				"parts": []map[string]any{
					{"text": prompt},
				},
			},
		},
		// Antigravity 上游要求必须包含身份提示词
		"systemInstruction": map[string]any{
			"parts": []map[string]any{
				{"text": GetDefaultIdentityPatch()},
			},
		},
		"generationConfig": map[string]any{
			"maxOutputTokens": 1,
		},
	}
	payloadBytes, _ := json.Marshal(payload)
	return WrapV1InternalRequest(projectID, model, payloadBytes)
}

// BuildClaudeTestRequest 构建 Claude 格式测试请求并转换为 Gemini 格式
// 使用最小 token 消耗：使用调用方提示词并限制 MaxTokens 为 1。
func BuildClaudeTestRequest(projectID, mappedModel string, prompts ...string) ([]byte, error) {
	prompt := "."
	if len(prompts) > 0 && strings.TrimSpace(prompts[0]) != "" {
		prompt = strings.TrimSpace(prompts[0])
	}
	promptJSON, _ := json.Marshal(prompt)
	claudeReq := &protocolanthropic.ClaudeRequest{
		Model: mappedModel,
		Messages: []protocolanthropic.ClaudeMessage{
			{
				Role:    "user",
				Content: json.RawMessage(promptJSON),
			},
		},
		MaxTokens: 1,
		Stream:    false,
	}
	return TransformClaudeToGemini(claudeReq, projectID, mappedModel)
}

// ExtractTextFromSSEResponse 从 SSE 流式响应中提取文本
func ExtractTextFromSSEResponse(respBody []byte) string {
	var texts []string
	lines := bytes.Split(respBody, []byte("\n"))

	for _, line := range lines {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		// 跳过 SSE 前缀
		if bytes.HasPrefix(line, []byte("data:")) {
			line = bytes.TrimPrefix(line, []byte("data:"))
			line = bytes.TrimSpace(line)
		}

		// 跳过非 JSON 行
		if len(line) == 0 || line[0] != '{' {
			continue
		}

		// 解析 JSON
		var data map[string]any
		if err := json.Unmarshal(line, &data); err != nil {
			continue
		}

		// 尝试从 response.candidates[0].content.parts[].text 提取
		response, ok := data["response"].(map[string]any)
		if !ok {
			// 尝试直接从 candidates 提取（某些响应格式）
			response = data
		}

		candidates, ok := response["candidates"].([]any)
		if !ok || len(candidates) == 0 {
			continue
		}

		candidate, ok := candidates[0].(map[string]any)
		if !ok {
			continue
		}

		content, ok := candidate["content"].(map[string]any)
		if !ok {
			continue
		}

		parts, ok := content["parts"].([]any)
		if !ok {
			continue
		}

		for _, part := range parts {
			if partMap, ok := part.(map[string]any); ok {
				if text, ok := partMap["text"].(string); ok && text != "" {
					texts = append(texts, text)
				}
			}
		}
	}

	return strings.Join(texts, "")
}

// Probe 复用平台账号内重试；不取得调度槽或注册计费会话。
func Probe(ctx context.Context, input RetryInput, options RetryOptions, model string, limit func() int64, enter func() (func(), error)) (*TestConnectionResult, error) {
	if enter != nil {
		done, err := enter()
		if err != nil {
			return nil, err
		}
		defer done()
	}
	input.Ctx = ctx
	result, err := (&RetryAdapter{Options: options}).AntigravityRetryLoop(input)
	if err != nil {
		var switchErr *AntigravityAccountSwitchError
		if errors.As(err, &switchErr) {
			return nil, fmt.Errorf("该账号模型 %s 当前限流中，请稍后重试", switchErr.RateLimitedModel)
		}
		return nil, err
	}
	if result == nil || result.Resp == nil {
		return nil, errors.New("upstream returned empty response")
	}
	defer func() { _ = result.Resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(result.Resp.Body, limit()))
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}
	if result.Resp.StatusCode >= 400 {
		return nil, fmt.Errorf("API 返回 %d: %s", result.Resp.StatusCode, string(body))
	}
	return &TestConnectionResult{Text: ExtractTextFromSSEResponse(body), MappedModel: model}, nil
}
