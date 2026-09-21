// 本文件维护 routing 的所属能力；兼容入口复用唯一实现。
package routing

import (
	strings "strings"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// 分组使用互斥策略，避免 Fast、Ultra Fast 和关闭开关冲突。
const (
	GroupOpenAIFastPolicyFollowRequest  = "follow_request"
	GroupOpenAIFastPolicyForcePriority  = "force_priority"
	GroupOpenAIFastPolicyForceUltrafast = "force_ultrafast"
	GroupOpenAIFastPolicyForceOff       = "force_off"
)

// EffectiveOpenAIFastPolicy 对旧实体兼容布尔值，显式新策略始终优先。
// @project-doc docs/interfaces/openai_upstream.md#openai_fast_policy
func (g *Group) EffectiveOpenAIFastPolicy() string {
	if g == nil {
		return GroupOpenAIFastPolicyFollowRequest
	}
	policy, err := ResolveGroupOpenAIFastPolicyInput(&g.OpenAIFastPolicy, g.ForceOpenAIFast)
	if err != nil {
		return GroupOpenAIFastPolicyFollowRequest
	}
	return policy
}

// ResolveGroupOpenAIFastPolicyInput 统一兼容输入和管理写入校验。
func ResolveGroupOpenAIFastPolicyInput(raw *string, legacyForce bool) (string, error) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		if legacyForce {
			return GroupOpenAIFastPolicyForcePriority, nil
		}
		return GroupOpenAIFastPolicyFollowRequest, nil
	}
	policy := strings.TrimSpace(*raw)
	switch policy {
	case GroupOpenAIFastPolicyFollowRequest, GroupOpenAIFastPolicyForcePriority, GroupOpenAIFastPolicyForceUltrafast, GroupOpenAIFastPolicyForceOff:
		return policy, nil
	default:
		return "", infraerrors.Newf(infraerrors.Category(400), "INVALID_OPENAI_FAST_POLICY", "invalid openai_fast_policy: %q", policy)
	}
}
