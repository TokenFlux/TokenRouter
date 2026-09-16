// SSE 计量 patch 保留原 message_start/message_delta 的覆盖和缺省语义。
package anthropic

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
)

func ParseSSEUsage(data string, usage *protocol.TokenUsage) {
	if usage == nil {
		return
	}

	var event map[string]any
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		return
	}

	if patch := ExtractSSEUsagePatch(event); patch != nil {
		MergeSSEUsagePatch(usage, patch)
	}
}

type SseUsagePatch struct {
	InputTokens              int
	HasInputTokens           bool
	OutputTokens             int
	HasOutputTokens          bool
	CacheCreationInputTokens int
	HasCacheCreationInput    bool
	CacheReadInputTokens     int
	HasCacheReadInput        bool
	CacheCreation5mTokens    int
	HasCacheCreation5m       bool
	CacheCreation1hTokens    int
	HasCacheCreation1h       bool
	Speed                    string
	HasSpeed                 bool
}

func ExtractSSEUsagePatch(event map[string]any) *SseUsagePatch {
	if len(event) == 0 {
		return nil
	}

	eventType, _ := event["type"].(string)
	switch eventType {
	case "message_start":
		msg, _ := event["message"].(map[string]any)
		usageObj, _ := msg["usage"].(map[string]any)
		if len(usageObj) == 0 {
			return nil
		}

		patch := &SseUsagePatch{}
		if speed, ok := usageObj["speed"].(string); ok && strings.TrimSpace(speed) != "" {
			patch.Speed = speed
			patch.HasSpeed = true
		}
		patch.HasInputTokens = true
		if v, ok := ParseSSEUsageInt(usageObj["input_tokens"]); ok {
			patch.InputTokens = v
		}
		patch.HasCacheCreationInput = true
		if v, ok := ParseSSEUsageInt(usageObj["cache_creation_input_tokens"]); ok {
			patch.CacheCreationInputTokens = v
		}
		patch.HasCacheReadInput = true
		if v, ok := ParseSSEUsageInt(usageObj["cache_read_input_tokens"]); ok {
			patch.CacheReadInputTokens = v
		}
		if cc, ok := usageObj["cache_creation"].(map[string]any); ok {
			if v, exists := ParseSSEUsageInt(cc["ephemeral_5m_input_tokens"]); exists {
				patch.CacheCreation5mTokens = v
				patch.HasCacheCreation5m = true
			}
			if v, exists := ParseSSEUsageInt(cc["ephemeral_1h_input_tokens"]); exists {
				patch.CacheCreation1hTokens = v
				patch.HasCacheCreation1h = true
			}
		}
		return patch

	case "message_delta":
		usageObj, _ := event["usage"].(map[string]any)
		if len(usageObj) == 0 {
			return nil
		}

		patch := &SseUsagePatch{}
		if speed, ok := usageObj["speed"].(string); ok && strings.TrimSpace(speed) != "" {
			patch.Speed = speed
			patch.HasSpeed = true
		}
		if v, ok := ParseSSEUsageInt(usageObj["input_tokens"]); ok && v > 0 {
			patch.InputTokens = v
			patch.HasInputTokens = true
		}
		if v, ok := ParseSSEUsageInt(usageObj["output_tokens"]); ok && v > 0 {
			patch.OutputTokens = v
			patch.HasOutputTokens = true
		}
		if v, ok := ParseSSEUsageInt(usageObj["cache_creation_input_tokens"]); ok && v > 0 {
			patch.CacheCreationInputTokens = v
			patch.HasCacheCreationInput = true
		}
		if v, ok := ParseSSEUsageInt(usageObj["cache_read_input_tokens"]); ok && v > 0 {
			patch.CacheReadInputTokens = v
			patch.HasCacheReadInput = true
		}
		if cc, ok := usageObj["cache_creation"].(map[string]any); ok {
			if v, exists := ParseSSEUsageInt(cc["ephemeral_5m_input_tokens"]); exists {
				patch.CacheCreation5mTokens = v
				patch.HasCacheCreation5m = true
			}
			if v, exists := ParseSSEUsageInt(cc["ephemeral_1h_input_tokens"]); exists {
				patch.CacheCreation1hTokens = v
				patch.HasCacheCreation1h = true
			}
		}
		return patch
	}

	return nil
}
func MergeSSEUsagePatch(usage *protocol.TokenUsage, patch *SseUsagePatch) {
	if usage == nil || patch == nil {
		return
	}

	if patch.HasInputTokens {
		usage.InputTokens = patch.InputTokens
	}
	if patch.HasSpeed {
		usage.Speed = patch.Speed
	}
	if patch.HasCacheCreationInput {
		usage.CacheCreationInputTokens = patch.CacheCreationInputTokens
	}
	if patch.HasCacheReadInput {
		usage.CacheReadInputTokens = patch.CacheReadInputTokens
	}
	if patch.HasOutputTokens {
		usage.OutputTokens = patch.OutputTokens
	}
	if patch.HasCacheCreation5m {
		usage.CacheCreation5mTokens = patch.CacheCreation5mTokens
	}
	if patch.HasCacheCreation1h {
		usage.CacheCreation1hTokens = patch.CacheCreation1hTokens
	}
}
func ParseSSEUsageInt(value any) (int, bool) {
	switch v := value.(type) {
	case float64:
		return int(v), true
	case float32:
		return int(v), true
	case int:
		return v, true
	case int64:
		return int(v), true
	case int32:
		return int(v), true
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return int(i), true
		}
		if f, err := v.Float64(); err == nil {
			return int(f), true
		}
	case string:
		if parsed, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return parsed, true
		}
	}
	return 0, false
}
