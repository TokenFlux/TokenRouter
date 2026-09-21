package openai

import (
	"bytes"
	"strings"

	"github.com/tidwall/gjson"
)

func WSEventMayContainModel(eventType string) bool {
	switch eventType {
	case "response.created",
		"response.in_progress",
		"response.completed",
		"response.done",
		"response.failed",
		"response.incomplete",
		"response.cancelled",
		"response.canceled":
		return true
	default:
		trimmed := strings.TrimSpace(eventType)
		if trimmed == eventType {
			return false
		}
		switch trimmed {
		case "response.created",
			"response.in_progress",
			"response.completed",
			"response.done",
			"response.failed",
			"response.incomplete",
			"response.cancelled",
			"response.canceled":
			return true
		default:
			return false
		}
	}
}

func WSEventMayContainToolCalls(eventType string) bool {
	eventType = strings.TrimSpace(eventType)
	if eventType == "" {
		return false
	}
	if strings.Contains(eventType, "function_call") || strings.Contains(eventType, "tool_call") {
		return true
	}
	switch eventType {
	case "response.output_item.added", "response.output_item.done", "response.completed", "response.done":
		return true
	default:
		return false
	}
}

func WSEventShouldParseUsage(eventType string) bool {
	eventType = strings.TrimSpace(eventType)
	if eventType == "error" || IsWSTerminalEvent(eventType) {
		return true
	}
	return strings.HasPrefix(eventType, "response.") && !strings.HasSuffix(eventType, ".delta")
}

// WSMessageShouldParseUsage 只在事件声明可能携带 usage 且消息确实含有
// usage 字段时进入 JSON 用量解析，减少高频 delta 事件的热路径开销。
func WSMessageShouldParseUsage(eventType string, message []byte) bool {
	if !bytes.Contains(message, []byte(`"usage"`)) {
		return false
	}
	eventType = strings.TrimSpace(eventType)
	if eventType == "error" || IsWSTerminalEvent(eventType) {
		return true
	}
	return strings.HasPrefix(eventType, "response.") && !strings.HasSuffix(eventType, ".delta")
}

func ParseWSEventEnvelope(message []byte) (eventType string, responseID string, response gjson.Result) {
	if len(message) == 0 {
		return "", "", gjson.Result{}
	}
	values := gjson.GetManyBytes(message, "type", "response.id", "id", "response")
	eventType = strings.TrimSpace(values[0].String())
	if id := strings.TrimSpace(values[1].String()); id != "" {
		responseID = id
	} else {
		responseID = strings.TrimSpace(values[2].String())
	}
	return eventType, responseID, values[3]
}

func WSMessageLikelyContainsToolCalls(message []byte) bool {
	if len(message) == 0 {
		return false
	}
	return bytes.Contains(message, []byte(`"tool_calls"`)) ||
		bytes.Contains(message, []byte(`"tool_call"`)) ||
		bytes.Contains(message, []byte(`"function_call"`))
}

func ParseWSResponseUsageFromCompletedEvent(message []byte, usage *ForwardUsage) {
	if usage == nil || len(message) == 0 || !bytes.Contains(message, []byte(`"usage"`)) {
		return
	}
	if parsedUsage, ok := ExtractOpenAIUsageFromJSONBytes(message); ok {
		if OpenAIStreamEventTypeIsTerminal(EffectiveOpenAISSEEventType(message, "")) {
			if !OpenAIUsageHasTokens(&parsedUsage) && OpenAIUsageHasTokens(usage) {
				return
			}
			*usage = parsedUsage
		} else {
			MergeOpenAIUsageNonZero(usage, parsedUsage)
		}
	}
}

func ParseWSErrorEventFields(message []byte) (code string, errType string, errMessage string) {
	if len(message) == 0 {
		return "", "", ""
	}
	values := gjson.GetManyBytes(message, "error.code", "error.type", "error.message")
	return strings.TrimSpace(values[0].String()), strings.TrimSpace(values[1].String()), strings.TrimSpace(values[2].String())
}

func WSPayloadString(payload map[string]any, key string) string {
	if len(payload) == 0 {
		return ""
	}
	raw, ok := payload[key]
	if !ok {
		return ""
	}
	switch v := raw.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	case []byte:
		return strings.TrimSpace(string(v))
	default:
		return ""
	}
}

func WSPayloadBoolFromRaw(payload []byte, key string, defaultValue bool) bool {
	if len(payload) == 0 || strings.TrimSpace(key) == "" {
		return defaultValue
	}
	value := gjson.GetBytes(payload, key)
	if !value.Exists() {
		return defaultValue
	}
	if value.Type != gjson.True && value.Type != gjson.False {
		return defaultValue
	}
	return value.Bool()
}

// IsWSTerminalEvent 只判断 response 终态；独立 error 事件由调用方单独处理。
func IsWSTerminalEvent(eventType string) bool {
	switch strings.TrimSpace(eventType) {
	case "response.completed", "response.done", "response.failed", "response.incomplete", "response.cancelled", "response.canceled":
		return true
	default:
		return false
	}
}
