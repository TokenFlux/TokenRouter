package tierpolicy

import (
	"fmt"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/routing"

	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// DecisionInput 固定平台和分组投影；设置和价格能力按原判断顺序惰性读取。
type DecisionInput struct {
	Model            string
	GroupPolicy      string
	OpenAI           bool
	Evaluate         func(string) (string, string)
	KeyPolicy        func() string
	ForceOnSupported func() bool
}

// Decision 描述最终应写入、删除或拒绝的 OpenAI Fast 决策。
type Decision struct {
	Tier        string
	DeleteField bool
	Blocked     *BlockedError
}

// IsAcceleratedTier 同时覆盖 Fast 和 Ultra Fast。
func IsAcceleratedTier(tier string) bool {
	return tier == OpenAIFastTierPriority || tier == OpenAIFastTierUltrafast
}

// resolveOpenAIFastModeDecision 统一解析系统策略与单 Key 策略。
// 系统先裁决原始 tier；Key 改写后再裁决一次，避免 force_on 绕过系统 filter/block。
func Resolve(input DecisionInput, rawTier string, hasField bool) Decision {
	normTier := openai.ServiceTierValue(rawTier)
	groupPolicy := input.GroupPolicy
	switch groupPolicy {
	case routing.GroupOpenAIFastPolicyForcePriority:
		normTier, hasField = OpenAIFastTierPriority, true
	case routing.GroupOpenAIFastPolicyForceUltrafast:
		normTier, hasField = OpenAIFastTierUltrafast, true
	}
	applySystemAction := func(tier string) (Decision, bool) {
		action, errMsg := input.Evaluate(tier)
		switch action {
		case "block":
			if errMsg == "" {
				errMsg = fmt.Sprintf("openai service_tier=%s is not allowed for model %s", tier, input.Model)
			}
			return Decision{Blocked: &BlockedError{Message: errMsg}}, true
		case "filter":
			return Decision{DeleteField: true}, true
		case OpenAIFastPolicyActionForcePriority:
			return Decision{Tier: OpenAIFastTierPriority}, true
		case OpenAIFastPolicyActionForceUltrafast:
			return Decision{Tier: OpenAIFastTierUltrafast}, true
		default:
			return Decision{}, false
		}
	}

	// 原始请求已命中的非 pass 系统动作直接生效，Key 策略不能覆盖。
	if normTier != "" {
		if decision, handled := applySystemAction(normTier); handled {
			return decision
		}
	}

	// 全局先裁决；分组关闭后，单 Key 不得重新开启。
	if groupPolicy == routing.GroupOpenAIFastPolicyForceOff {
		if IsAcceleratedTier(normTier) {
			return Decision{DeleteField: hasField}
		}
		return Decision{Tier: normTier}
	}
	candidateTier := normTier
	policy := input.KeyPolicy()
	keyPolicyApplicable := false
	switch policy {
	case apikey.APIKeyFastModePolicyForceOn:
		keyPolicyApplicable = input.ForceOnSupported()
	case apikey.APIKeyFastModePolicyForceOff:
		// 强制关闭只净化真正代表 Fast 的 priority，不依赖定价文件中的能力标记。
		keyPolicyApplicable = input.OpenAI
	}
	candidateChanged := false
	if keyPolicyApplicable {
		switch policy {
		case apikey.APIKeyFastModePolicyForceOn:
			// 单 Key 开启 Fast 不降低分组强制的 Ultra Fast。
			if groupPolicy != routing.GroupOpenAIFastPolicyForceUltrafast {
				candidateTier = OpenAIFastTierPriority
			}
		case apikey.APIKeyFastModePolicyForceOff:
			// flex 是低优先级模式，auto/default/scale 也是官方合法 tier，均需保留。
			if IsAcceleratedTier(normTier) {
				candidateTier = ""
			}
		}
		candidateChanged = candidateTier != normTier
	}

	// Key 注入或改写出的 tier 必须重新接受系统策略裁决。
	if candidateTier != "" {
		if candidateChanged {
			if decision, handled := applySystemAction(candidateTier); handled {
				return decision
			}
		}
		return Decision{Tier: candidateTier}
	}
	if policy == apikey.APIKeyFastModePolicyForceOff && keyPolicyApplicable && IsAcceleratedTier(normTier) {
		return Decision{DeleteField: hasField}
	}
	return Decision{}
}

// applyOpenAIFastPolicyToBody 对原始请求体应用系统策略和单 Key Fast 策略。
//
// Rationale for normalize-on-pass: chat-completions / messages 入口在调用本
// 函数之前已经通过 normalizeResponsesBodyServiceTier 把 service_tier 归一化
// 到了上游可识别值；passthrough（OpenAI 自动透传） / native /responses 等
// 入口没有这一前置步骤，pass 路径下若不在此处归一化，"fast" 就会被原样
// 透传到 OpenAI 上游导致 400/拒绝。把归一化收敛到本函数，所有入口行为一致。
func ApplyBody(body []byte, input DecisionInput) ([]byte, error) {
	if len(body) == 0 {
		return body, nil
	}
	tierResult := gjson.GetBytes(body, "service_tier")
	decision := Resolve(input, tierResult.String(), tierResult.Exists())
	if decision.Blocked != nil {
		return body, decision.Blocked
	}
	if decision.DeleteField {
		trimmed, err := sjson.DeleteBytes(body, "service_tier")
		if err != nil {
			return body, fmt.Errorf("strip service_tier from body: %w", err)
		}
		return trimmed, nil
	}
	if decision.Tier != "" && (!tierResult.Exists() || decision.Tier != tierResult.String()) {
		updated, err := sjson.SetBytes(body, "service_tier", decision.Tier)
		if err != nil {
			return body, fmt.Errorf("apply service_tier to body: %w", err)
		}
		return updated, nil
	}
	return body, nil
}
