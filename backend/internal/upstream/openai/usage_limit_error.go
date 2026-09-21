package openai

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// parseOpenAIRateLimitResetTime 解析 OpenAI 兼容格式的 429 响应，返回重置时间的 Unix 时间戳
// OpenAI 的 usage_limit_reached 错误格式：
//
//	{
//	  "error": {
//	    "message": "The usage limit has been reached",
//	    "type": "usage_limit_reached",
//	    "resets_at": 1769404154,
//	    "resets_in_seconds": 133107
//	  }
//	}
func ParseUsageLimitResetTime(body []byte, now func() time.Time) *int64 {
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil
	}

	errObj, ok := parsed["error"].(map[string]any)
	if !ok {
		return nil
	}

	// 检查是否为已知的账号用量限制类型。
	errType, _ := errObj["type"].(string)
	if errType != "usage_limit_reached" && errType != "rate_limit_exceeded" && errType != "GoUsageLimitError" {
		return nil
	}

	// 优先使用 resets_at（Unix 时间戳）
	if resetsAt, ok := errObj["resets_at"].(float64); ok {
		ts := int64(resetsAt)
		return &ts
	}
	if resetsAt, ok := errObj["resets_at"].(string); ok {
		if ts, err := strconv.ParseInt(resetsAt, 10, 64); err == nil {
			return &ts
		}
	}

	// 如果没有 resets_at，尝试使用 resets_in_seconds
	if resetsInSeconds, ok := errObj["resets_in_seconds"].(float64); ok {
		ts := now().Unix() + int64(resetsInSeconds)
		return &ts
	}
	if resetsInSeconds, ok := errObj["resets_in_seconds"].(string); ok {
		if sec, err := strconv.ParseInt(resetsInSeconds, 10, 64); err == nil {
			ts := now().Unix() + sec
			return &ts
		}
	}

	// OpenCode Go 订阅只在可读错误消息中提供重置时间，例如
	// "Weekly usage limit reached. Resets in 2 days."。
	if errType == "GoUsageLimitError" {
		message, _ := errObj["message"].(string)
		if resetAfter := parseOpenCodeGoUsageLimitResetDuration(message); resetAfter > 0 {
			ts := now().Add(resetAfter).Unix()
			return &ts
		}
	}

	return nil
}

// parseOpenCodeGoUsageLimitResetDuration 解析 OpenCode Go 消息中的单段或复合重置时长。
func parseOpenCodeGoUsageLimitResetDuration(message string) time.Duration {
	resetPrefix := openCodeGoUsageLimitResetPattern.FindStringIndex(message)
	if resetPrefix == nil {
		return 0
	}

	remainder := message[resetPrefix[1]:]
	var total time.Duration
	for {
		remainder = strings.TrimSpace(remainder)
		matches := openCodeGoUsageLimitDurationPartPattern.FindStringSubmatchIndex(remainder)
		if matches == nil {
			break
		}

		value, err := strconv.ParseFloat(remainder[matches[2]:matches[3]], 64)
		if err != nil || value <= 0 {
			return 0
		}

		unit := openCodeGoUsageLimitDurationUnit(remainder[matches[4]:matches[5]])
		if unit <= 0 {
			return 0
		}

		const maxDuration = time.Duration(1<<63 - 1)
		if value >= float64(maxDuration)/float64(unit) {
			return 0
		}
		part := time.Duration(value * float64(unit))
		if part <= 0 || total > maxDuration-part {
			return 0
		}
		total += part
		remainder = remainder[matches[1]:]
	}

	return total
}

func openCodeGoUsageLimitDurationUnit(raw string) time.Duration {
	switch strings.ToLower(raw) {
	case "s", "sec", "secs", "second", "seconds":
		return time.Second
	case "m", "min", "mins", "minute", "minutes":
		return time.Minute
	case "h", "hr", "hrs", "hour", "hours":
		return time.Hour
	case "d", "day", "days":
		return 24 * time.Hour
	case "w", "week", "weeks":
		return 7 * 24 * time.Hour
	default:
		return 0
	}
}

func ParseUsageLimitPlanType(body []byte) string {
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return ""
	}

	errObj, ok := parsed["error"].(map[string]any)
	if !ok {
		return ""
	}

	errType, _ := errObj["type"].(string)
	if errType != "usage_limit_reached" && errType != "rate_limit_exceeded" {
		return ""
	}

	planType, _ := errObj["plan_type"].(string)
	return strings.ToLower(strings.TrimSpace(planType))
}

var openCodeGoUsageLimitResetPattern = regexp.MustCompile(`(?i)\bresets\s+in\s+`)
var openCodeGoUsageLimitDurationPartPattern = regexp.MustCompile(`(?i)^([0-9]+(?:\.[0-9]+)?)\s*(s|sec|secs|second|seconds|m|min|mins|minute|minutes|h|hr|hrs|hour|hours|d|day|days|w|week|weeks)\b`)
