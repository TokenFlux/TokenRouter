package service

import (
	context "context"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	gjson "github.com/tidwall/gjson"
	sjson "github.com/tidwall/sjson"
	strings "strings"
)

const ReasoningEffortOverLimitDowngrade = routing.ReasoningEffortOverLimitDowngrade
const ReasoningEffortOverLimitDeny = routing.ReasoningEffortOverLimitDeny

type requestedReasoningEffortContextKey struct{}

type openAIReasoningEffortPolicyContextKey struct{}

// openAIReasoningEffortPolicy 保存请求级推理强度策略快照，避免异步转发期间
// 读取到已经被调用方修改的分组切片。
type openAIReasoningEffortPolicy struct {
	maxEffort    string
	overLimit    string
	requestModel string
	mappings     []ReasoningEffortMapping
}

// ReasoningEffortOverLimitError 保留原错误类型和 errors.As 行为。
type ReasoningEffortOverLimitError = routing.ReasoningEffortOverLimitError

// WithRequestedReasoningEffort 将请求进入策略层前捕获的客户端档位绑定到 context。
func WithRequestedReasoningEffort(ctx context.Context, effort string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	effort = strings.TrimSpace(effort)
	if effort == "" {
		return ctx
	}
	return context.WithValue(ctx, requestedReasoningEffortContextKey{}, effort)
}

// RequestedReasoningEffortFromContext 读取已绑定的客户端档位。
func RequestedReasoningEffortFromContext(ctx context.Context) *string {
	if ctx == nil {
		return nil
	}
	effort, ok := ctx.Value(requestedReasoningEffortContextKey{}).(string)
	if !ok {
		return nil
	}
	effort = strings.TrimSpace(effort)
	if effort == "" {
		return nil
	}
	return &effort
}

// NormalizeMaxReasoningEffort 委托纯管理员规则，旧调用者由 S06/S11 继续迁移。
func NormalizeMaxReasoningEffort(raw string) string { return routing.NormalizeMaxReasoningEffort(raw) }

// normalizeRequestedOpenAIReasoningEffort 委托纯管理员规则，旧调用者由 S06/S11 继续迁移。
func normalizeRequestedOpenAIReasoningEffort(raw string) string {
	return routing.NormalizeRequestedOpenAIReasoningEffort(raw)
}

// normalizeMaxReasoningEffortForPlatform 委托纯管理员规则，旧调用者由 S06/S11 继续迁移。
func normalizeMaxReasoningEffortForPlatform(platform, raw string) (string, error) {
	return routing.NormalizeMaxReasoningEffortForPlatform(platform, raw)
}

// NormalizeMaxReasoningEffortOverLimit 委托纯管理员规则，旧调用者由 S06/S11 继续迁移。
func NormalizeMaxReasoningEffortOverLimit(raw string) string {
	return routing.NormalizeMaxReasoningEffortOverLimit(raw)
}

// normalizeMaxReasoningEffortOverLimitForPlatform 委托纯管理员规则，旧调用者由 S06/S11 继续迁移。
func normalizeMaxReasoningEffortOverLimitForPlatform(platform, raw string) (string, error) {
	return routing.NormalizeMaxReasoningEffortOverLimitForPlatform(platform, raw)
}

// reasoningEffortRank 委托纯管理员规则，旧调用者由 S06/S11 继续迁移。
func reasoningEffortRank(raw string) (int, bool) { return routing.ReasoningEffortRank(raw) }

// NormalizeReasoningEffortMappings 委托纯管理员规则，旧调用者由 S06/S11 继续迁移。
func NormalizeReasoningEffortMappings(platform string, raw []ReasoningEffortMapping) ([]ReasoningEffortMapping, error) {
	return routing.NormalizeReasoningEffortMappings(platform, raw)
}

// WithOpenAIReasoningEffortPolicy 将分组策略绑定到请求上下文。
func WithOpenAIReasoningEffortPolicy(ctx context.Context, maxEffort string, mappings []ReasoningEffortMapping, overLimit string) context.Context {
	return withOpenAIReasoningEffortPolicyForModel(ctx, maxEffort, mappings, overLimit, "")
}

// WithOpenAIReasoningEffortPolicyForModel 绑定策略并保留客户端模型，供模型范围映射使用。
func WithOpenAIReasoningEffortPolicyForModel(ctx context.Context, maxEffort string, mappings []ReasoningEffortMapping, overLimit, requestModel string) context.Context {
	return withOpenAIReasoningEffortPolicyForModel(ctx, maxEffort, mappings, overLimit, requestModel)
}

func withOpenAIReasoningEffortPolicyForModel(ctx context.Context, maxEffort string, mappings []ReasoningEffortMapping, overLimit, requestModel string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, openAIReasoningEffortPolicyContextKey{}, openAIReasoningEffortPolicy{
		maxEffort:    maxEffort,
		overLimit:    overLimit,
		requestModel: strings.TrimSpace(requestModel),
		mappings:     append([]ReasoningEffortMapping(nil), mappings...),
	})
}

// ApplyOpenAIReasoningEffortPolicyFromContext 执行先前绑定的请求级策略。
func ApplyOpenAIReasoningEffortPolicyFromContext(ctx context.Context, body []byte) ([]byte, bool, error) {
	if ctx == nil {
		return body, false, nil
	}
	policy, ok := ctx.Value(openAIReasoningEffortPolicyContextKey{}).(openAIReasoningEffortPolicy)
	if !ok {
		return body, false, nil
	}
	return applyOpenAIReasoningEffortPolicy(body, policy.maxEffort, policy.mappings, policy.overLimit, policy.requestModel)
}

// mapReasoningEffort 委托纯管理员规则，旧调用者由 S06/S11 继续迁移。
func mapReasoningEffort(raw string, mappings []ReasoningEffortMapping, requestModel string) (string, bool) {
	return routing.MapReasoningEffort(raw, mappings, requestModel)
}

// ApplyOpenAIReasoningEffortPolicy 先应用模型范围映射，再按配置降档或拒绝超限请求。
// 未指定的值保持不变，继续由上游默认值控制。
func ApplyOpenAIReasoningEffortPolicy(body []byte, maxEffort string, mappings []ReasoningEffortMapping, overLimit string) ([]byte, bool, error) {
	return applyOpenAIReasoningEffortPolicy(body, maxEffort, mappings, overLimit, "")
}

func applyOpenAIReasoningEffortPolicy(body []byte, maxEffort string, mappings []ReasoningEffortMapping, overLimit, requestModel string) ([]byte, bool, error) {
	maxRank, hasMax := reasoningEffortRank(maxEffort)
	if len(body) == 0 || (!hasMax && len(mappings) == 0) {
		return body, false, nil
	}

	if strings.TrimSpace(requestModel) == "" {
		requestModel = strings.TrimSpace(gjson.GetBytes(body, "model").String())
	}
	deny := hasMax && NormalizeMaxReasoningEffortOverLimit(overLimit) == ReasoningEffortOverLimitDeny
	canonicalMax := NormalizeMaxReasoningEffort(maxEffort)
	result := body
	changed := false
	for _, path := range []string{"reasoning.effort", "reasoning_effort", "output_config.effort"} {
		field := gjson.GetBytes(result, path)
		if !field.Exists() || field.Type != gjson.String {
			continue
		}
		original := strings.TrimSpace(field.String())
		if original == "" {
			continue
		}

		effective, _ := mapReasoningEffort(original, mappings, requestModel)
		if currentRank, recognized := reasoningEffortRank(effective); recognized {
			effective = NormalizeMaxReasoningEffort(effective)
			if hasMax && currentRank > maxRank {
				if deny {
					return body, false, &ReasoningEffortOverLimitError{Requested: effective, Max: canonicalMax}
				}
				effective = canonicalMax
			}
		}
		if effective == original {
			continue
		}

		updated, err := sjson.SetBytes(result, path, effective)
		if err != nil {
			continue
		}
		result = updated
		changed = true
	}
	return result, changed, nil
}

// applyOpenAIWSReasoningEffortPolicy 将同一套分组策略应用到 WS 请求帧。
// requestModel 由调用方提供客户端模型，支持后续省略 model 的多轮帧。
func applyOpenAIWSReasoningEffortPolicy(payload []byte, hooks *OpenAIWSIngressHooks, requestModel string) ([]byte, error) {
	if hooks == nil || (hooks.MaxReasoningEffort == "" && len(hooks.ReasoningEffortMappings) == 0) {
		return payload, nil
	}
	updated, changed, err := applyOpenAIReasoningEffortPolicy(
		payload,
		hooks.MaxReasoningEffort,
		hooks.ReasoningEffortMappings,
		hooks.MaxReasoningEffortOverLimit,
		strings.TrimSpace(requestModel),
	)
	if err != nil {
		return payload, err
	}
	if changed {
		return updated, nil
	}
	return payload, nil
}
