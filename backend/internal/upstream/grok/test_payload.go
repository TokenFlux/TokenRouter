package grok

import (
	"encoding/json"
	"strings"
)

// AccountTestBody 保留 Responses 探测所需字段，同时使用管理端自定义提示词。
func AccountTestBody(model, prompt string) ([]byte, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		model = DefaultResponsesModel
	}
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		prompt = "hi"
	}
	return json.Marshal(map[string]any{
		"model":  model,
		"input":  prompt,
		"stream": true,
	})
}
