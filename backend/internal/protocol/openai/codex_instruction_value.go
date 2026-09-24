package openai

// EnsureCodexInstructionsField 保留缺失、null 和非字符串时的空字符串回退。
func EnsureCodexInstructionsField(reqBody map[string]any) {
	if reqBody == nil {
		return
	}
	if value, ok := reqBody["instructions"]; !ok || value == nil {
		reqBody["instructions"] = ""
		return
	}
	if _, ok := reqBody["instructions"].(string); !ok {
		reqBody["instructions"] = ""
	}
}
