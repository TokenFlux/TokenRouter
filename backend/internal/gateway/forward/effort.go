package forward

import (
	"strings"

	"github.com/tidwall/gjson"
)

// ExtractEffort 保留 Responses/Chat 字段读取优先级，模型能力归外层明确的规范化函数。
func ExtractEffort(body []byte, chat bool, normalize func(string, string) string, models ...string) *string {
	raw := strings.TrimSpace(gjson.GetBytes(body, "reasoning.effort").String())
	if raw == "" && chat {
		raw = strings.TrimSpace(gjson.GetBytes(body, "reasoning_effort").String())
	}
	if raw == "" {
		return nil
	}
	model := ""
	for _, candidate := range models {
		if value := strings.TrimSpace(candidate); value != "" {
			model = value
			break
		}
	}
	if model == "" {
		model = strings.TrimSpace(gjson.GetBytes(body, "model").String())
	}
	value := normalize(raw, model)
	if value == "" {
		return nil
	}
	return &value
}
