// Package tierpolicy 拥有入站服务档位策略的值类型、校验与设置编码。
package tierpolicy

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

// OpenAI Fast Policy 策略常量
// OpenAI 的 "fast 模式" 通过请求体中的 service_tier 字段识别：
//   - "priority"（客户端可传 "fast"，归一化为 "priority"）：fast 模式
//   - "ultrafast"：Codex/API 的 Ultrafast 档位
//   - "flex"：低优先级模式
//   - 省略：normal 默认
//
// 本策略复用 BetaPolicyAction*/BetaPolicyScope* 常量语义，只是匹配键从
// anthropic-beta header 换成 body 的 service_tier 字段。
const (
	OpenAIFastTierAny       = "all"                               // 匹配任意已识别的 service_tier
	OpenAIFastTierPriority  = protocolopenai.ServiceTierPriority  // 仅匹配 fast（priority）
	OpenAIFastTierUltrafast = protocolopenai.ServiceTierUltrafast // 仅匹配 ultrafast
	OpenAIFastTierFlex      = protocolopenai.ServiceTierFlex      // 仅匹配 flex

	// OpenAIFastPolicyActionForcePriority 会保留 service_tier 字段并强制写成
	// priority，用于把 flex/auto/default/scale 等已识别 tier 收敛为 fast。
	OpenAIFastPolicyActionForcePriority = "force_priority"
	// Ultra Fast 共用既有作用域和模型回退规则。
	OpenAIFastPolicyActionForceUltrafast = "force_ultrafast"
)

// OpenAIFastPolicyRule 单条 OpenAI fast/flex 策略规则
type OpenAIFastPolicyRule struct {
	ServiceTier          string   `json:"service_tier"`                     // "priority" | "ultrafast" | "flex" | "auto" | "default" | "scale" | "all"
	Action               string   `json:"action"`                           // "pass" | "filter" | "block" | "force_priority"
	Scope                string   `json:"scope"`                            // "all" | "oauth" | "apikey" | "bedrock"
	UserIDs              []int64  `json:"user_ids,omitempty"`               // 空=所有 Sub2API 用户；非空=仅指定 API Key 所属用户
	ErrorMessage         string   `json:"error_message,omitempty"`          // 自定义错误消息 (action=block 时生效)
	ModelWhitelist       []string `json:"model_whitelist,omitempty"`        // 模型匹配模式列表（为空=对所有模型生效）
	FallbackAction       string   `json:"fallback_action,omitempty"`        // 未匹配白名单的模型的处理方式
	FallbackErrorMessage string   `json:"fallback_error_message,omitempty"` // 未匹配白名单时的自定义错误消息 (fallback_action=block 时生效)
}

// OpenAIFastPolicySettings OpenAI fast 策略配置
type OpenAIFastPolicySettings struct {
	Rules []OpenAIFastPolicyRule `json:"rules"`
}

// Default 保留无规则时的上游档位语义。
func Default() *OpenAIFastPolicySettings {
	return &OpenAIFastPolicySettings{Rules: []OpenAIFastPolicyRule{}}
}

// Prepare 仅校验和编码，调用者统一提交，输入不会被规范化过程修改。
func Prepare(settings *OpenAIFastPolicySettings) (string, error) {
	if settings == nil {
		return "", fmt.Errorf("settings cannot be nil")
	}

	copySettings := *settings
	copySettings.Rules = slices.Clone(settings.Rules)
	for i := range copySettings.Rules {
		copySettings.Rules[i].UserIDs = slices.Clone(settings.Rules[i].UserIDs)
		copySettings.Rules[i].ModelWhitelist = slices.Clone(settings.Rules[i].ModelWhitelist)
	}
	settings = &copySettings

	validActions := map[string]bool{
		"pass": true, "filter": true, "block": true,
		OpenAIFastPolicyActionForcePriority: true, OpenAIFastPolicyActionForceUltrafast: true,
	}
	validScopes := map[string]bool{
		"all": true, "oauth": true, "apikey": true, "bedrock": true,
	}
	validTiers := map[string]bool{
		OpenAIFastTierAny: true, OpenAIFastTierPriority: true, OpenAIFastTierUltrafast: true, OpenAIFastTierFlex: true,
	}

	for i, rule := range settings.Rules {
		tier := strings.ToLower(strings.TrimSpace(rule.ServiceTier))
		if tier == "" {
			tier = OpenAIFastTierAny
		}
		if !validTiers[tier] {
			return "", fmt.Errorf("rule[%d]: invalid service_tier %q", i, rule.ServiceTier)
		}
		settings.Rules[i].ServiceTier = tier
		if !validActions[rule.Action] {
			return "", fmt.Errorf("rule[%d]: invalid action %q", i, rule.Action)
		}
		if !validScopes[rule.Scope] {
			return "", fmt.Errorf("rule[%d]: invalid scope %q", i, rule.Scope)
		}
		seenUserIDs := make(map[int64]struct{}, len(rule.UserIDs))
		for j, userID := range rule.UserIDs {
			if userID <= 0 {
				return "", fmt.Errorf("rule[%d]: user_ids[%d] must be positive", i, j)
			}
			if _, exists := seenUserIDs[userID]; exists {
				return "", fmt.Errorf("rule[%d]: user_ids[%d] duplicates user_id %d", i, j, userID)
			}
			seenUserIDs[userID] = struct{}{}
		}
		for j, pattern := range rule.ModelWhitelist {
			trimmed := strings.TrimSpace(pattern)
			if trimmed == "" {
				return "", fmt.Errorf("rule[%d]: model_whitelist[%d] cannot be empty", i, j)
			}
			settings.Rules[i].ModelWhitelist[j] = trimmed
		}
		if rule.FallbackAction != "" && !validActions[rule.FallbackAction] {
			return "", fmt.Errorf("rule[%d]: invalid fallback_action %q", i, rule.FallbackAction)
		}
	}

	data, err := json.Marshal(settings)
	if err != nil {
		return "", fmt.Errorf("marshal openai fast policy settings: %w", err)
	}

	return string(data), nil
}
