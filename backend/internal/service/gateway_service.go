package service

import (
	"strings"

	"time"

	s09openai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"

	"github.com/tidwall/gjson"
)

const (
	stickySessionTTL   = time.Hour // 粘性会话TTL
	defaultMaxLineSize = 500 * 1024 * 1024
)

func openAIStreamEventIsTerminal(data string) bool {
	trimmed := strings.TrimSpace(data)
	if trimmed == "" {
		return false
	}
	if trimmed == "[DONE]" {
		return true
	}
	return s09openai.OpenAIStreamEventTypeIsTerminal(gjson.Get(trimmed, "type").String())
}

// openAIStreamEventIsTerminalWithType 复用已提取的 type，避免 SSE 热路径重复扫描 JSON。
func openAIStreamEventIsTerminalWithType(data, eventType string) bool {
	trimmed := strings.TrimSpace(data)
	if trimmed == "" {
		return false
	}
	if trimmed == "[DONE]" {
		return true
	}
	return s09openai.OpenAIStreamEventTypeIsTerminal(eventType)
}

// sseDataRe matches SSE data lines with optional whitespace after colon.
// Some upstream APIs return non-standard "data:" without space (should be "data: ").
var (

// claudeCodePromptPrefixes 用于检测 Claude Code 系统提示词的前缀列表
// 支持多种变体：标准版、Agent SDK 版、Explore Agent 版、Compact 版等
// 注意：前缀之间不应存在包含关系，否则会导致冗余匹配

)

// ErrNoAvailableAccounts 表示没有可用的账号

var allowedHeaders = claude.AllowedHeaders

// derefGroupID safely dereferences *int64 to int64, returning 0 if nil
func derefGroupID(groupID *int64) int64 {
	if groupID == nil {
		return 0
	}
	return *groupID
}
