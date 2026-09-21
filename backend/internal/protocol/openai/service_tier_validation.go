// service_tier 的报文值与字段校验不读取管理员策略或账号状态。
package openai

import (
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
)

func NormalizeServiceTier(raw string) *string {
	value := strings.ToLower(strings.TrimSpace(raw))
	if value == "" {
		return nil
	}
	if value == "fast" {
		value = "priority"
	}
	// 放过 OpenAI 官方文档定义的合法 tier 值，以及 Codex/API 新增的 ultrafast。
	// Codex 客户端会发 priority、flex 或 ultrafast；直连 OpenAI SDK 的用户还会
	// 透传 auto/default/scale。真未知值仍返回 nil，由
	// normalizeResponsesBodyServiceTier 从 body 中删除。
	switch value {
	case "priority", "flex", "auto", "default", "scale", ServiceTierUltrafast:
		return &value
	default:
		return nil
	}
}

// InvalidServiceTierError 表示请求携带了未知的 service_tier。handler 会将其
// 转换为 400 invalid_request_error，避免静默剥离字段而掩盖客户端意图。
type InvalidServiceTierError struct {
	Value string
}

func (e *InvalidServiceTierError) Error() string {
	return fmt.Sprintf("invalid service_tier %q: must be one of auto, default, fast, flex, priority, scale, ultrafast", e.Value)
}

const invalidOpenAIServiceTierValueMaxLen = 64

func boundInvalidOpenAIServiceTierValue(raw string) string {
	if len(raw) <= invalidOpenAIServiceTierValueMaxLen {
		return raw
	}
	return raw[:invalidOpenAIServiceTierValueMaxLen] + "..."
}

// ValidateServiceTierField 校验 OpenAI 兼容请求体中的 service_tier 字段。
//
// 空值或 null 保持兼容；fast 归一化为 priority；priority、flex、auto、default、
// scale、ultrafast 原样通过。显式的非字符串、空字符串或未知值返回校验错误。
func ValidateServiceTierField(body []byte) (string, error) {
	tierResult := gjson.GetBytes(body, "service_tier")
	if !tierResult.Exists() || tierResult.Type == gjson.Null {
		return "", nil
	}
	if tierResult.Type != gjson.String {
		return "", &InvalidServiceTierError{Value: "<non-string>"}
	}
	raw := strings.TrimSpace(tierResult.String())
	if raw == "" {
		return "", &InvalidServiceTierError{Value: raw}
	}
	norm := ServiceTierValue(raw)
	if norm == "" {
		return "", &InvalidServiceTierError{Value: boundInvalidOpenAIServiceTierValue(raw)}
	}
	return norm, nil
}

// ServiceTierValue 在不合法时返回空值，保留原归一化调用方的缺省判断。
func ServiceTierValue(raw string) string {
	normalized := NormalizeServiceTier(raw)
	if normalized == nil {
		return ""
	}
	return *normalized
}
