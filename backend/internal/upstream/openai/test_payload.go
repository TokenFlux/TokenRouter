package openai

import (
	"strings"
)

// TestResponsesPayload 保留账号测试与用量探针共用的 Responses 报文。
func TestResponsesPayload(modelID string, prompt string, isOAuth bool) map[string]any {
	testPrompt := strings.TrimSpace(prompt)
	if testPrompt == "" {
		testPrompt = "hi"
	}
	payload := map[string]any{
		"model": modelID,
		"input": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{
						"type": "input_text",
						"text": testPrompt,
					},
				},
			},
		},
		"stream": true,
	}

	// OAuth 使用 ChatGPT 内部 API 时必须关闭服务端存储。
	if isOAuth {
		payload["store"] = false
	}

	// 两类账号的 Responses 测试均保留原指令。
	payload["instructions"] = DefaultInstructions

	return payload
}
