package clientmeta

import (
	"strings"

	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/tidwall/gjson"
)

// Codex 审查线索沿用原字段；这里只解释声明，不授予权限或解析账号。
const (
	CodexAutoReviewModel      = "codex-auto-review"
	OpenAISubagentHeader      = "x-openai-subagent"
	CodexParentThreadIDHeader = "x-codex-parent-thread-id"
	CodexTurnMetadataHeader   = "x-codex-turn-metadata"
)

type CodexReviewInput struct {
	Model, Subagent, ParentThreadID, TurnMetadata string
	Body                                          []byte
}

// CodexReviewParent 返回无歧义的父线程标识，原始模型和每类声明必须同时满足原约束。
func CodexReviewParent(input CodexReviewInput) string {
	if !IsCodexReviewModel(input.Model) {
		return ""
	}
	headerMetadata := input.TurnMetadata
	bodyMetadata := protocolopenai.RequestPayloadView(input.Body).Get("client_metadata.x-codex-turn-metadata").String()
	if !hasUnambiguousOpenAICodexReviewSubagent(
		input.Subagent,
		codexSubagentKindFromMetadata(headerMetadata),
		codexSubagentKindFromMetadata(bodyMetadata),
	) {
		return ""
	}

	parentID := ""
	for _, candidate := range []string{
		strings.TrimSpace(input.ParentThreadID),
		codexParentThreadIDFromMetadata(headerMetadata),
		codexParentThreadIDFromMetadata(bodyMetadata),
	} {
		if candidate == "" {
			continue
		}
		if parentID != "" && parentID != candidate {
			return ""
		}
		parentID = candidate
	}
	if parentID == "" {
		return ""
	}

	return parentID
}
func codexParentThreadIDFromMetadata(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || !gjson.Valid(raw) {
		return ""
	}
	return strings.TrimSpace(gjson.Get(raw, "parent_thread_id").String())
}

func codexSubagentKindFromMetadata(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || !gjson.Valid(raw) {
		return ""
	}
	return strings.TrimSpace(gjson.Get(raw, "subagent_kind").String())
}

func hasUnambiguousOpenAICodexReviewSubagent(candidates ...string) bool {
	subagent := ""
	for _, candidate := range candidates {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if candidate == "" {
			continue
		}
		if subagent != "" && subagent != candidate {
			return false
		}
		subagent = candidate
	}
	return subagent == "guardian" || subagent == "review"
}

// IsCodexReviewModel 供 HTTP 边界在读取线索前保持原模型短路。
func IsCodexReviewModel(model string) bool {
	return strings.EqualFold(strings.TrimSpace(model), CodexAutoReviewModel)
}
