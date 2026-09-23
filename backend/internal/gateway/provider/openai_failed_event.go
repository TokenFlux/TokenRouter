package provider

import (
	"encoding/json"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

// BuildOpenAIResponseFailedSSE 构造可被客户端解析的 Responses 失败事件。
func BuildOpenAIResponseFailedSSE(responseID, model string, source []byte, fallbackMessage string) string {
	responseID = strings.TrimSpace(responseID)
	if responseID == "" {
		responseID = "resp_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	}
	message := strings.TrimSpace(openai.ExtractOpenAISSEErrorMessage(source))
	if message == "" {
		message = strings.TrimSpace(fallbackMessage)
	}
	if message == "" {
		message = "Upstream response failed"
	}
	code := strings.TrimSpace(gjson.GetBytes(source, "error.code").String())
	if code == "" {
		code = strings.TrimSpace(gjson.GetBytes(source, "response.error.code").String())
	}
	if code == "" {
		code = "upstream_error"
	}
	errorBody := map[string]any{"code": code, "message": message}
	if errorType := strings.TrimSpace(gjson.GetBytes(source, "error.type").String()); errorType != "" {
		errorBody["type"] = errorType
	}
	response := map[string]any{
		"id":     responseID,
		"object": "response",
		"model":  strings.TrimSpace(model),
		"status": "failed",
		"error":  errorBody,
	}
	body, _ := json.Marshal(map[string]any{"type": "response.failed", "response": response})
	return "data: " + string(body) + "\n\n"
}
