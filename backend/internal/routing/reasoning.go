package routing

import (
	"fmt"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

const (
	MaxReasoningEffortMappings = 64
	MaxReasoningEffortValueLen = 64
	MaxReasoningEffortModelLen = 200

	// ReasoningEffortOverLimitDowngrade 表示超出上限时改写为上限值。
	ReasoningEffortOverLimitDowngrade = "downgrade"
	// ReasoningEffortOverLimitDeny 表示超出上限时拒绝请求。
	ReasoningEffortOverLimitDeny = "deny"
)

// ReasoningEffortOverLimitError 表示显式推理强度超过分组上限且策略要求拒绝。
type ReasoningEffortOverLimitError struct {
	Requested string
	Max       string
}

func (e *ReasoningEffortOverLimitError) Error() string {
	if e == nil {
		return "reasoning effort exceeds this group's limit"
	}
	requested := strings.TrimSpace(e.Requested)
	max := strings.TrimSpace(e.Max)
	if requested == "" && max == "" {
		return "reasoning effort exceeds this group's limit"
	}
	if requested == "" {
		return fmt.Sprintf("reasoning effort exceeds this group's limit of %q", max)
	}
	if max == "" {
		return fmt.Sprintf("reasoning effort %q exceeds this group's limit", requested)
	}
	return fmt.Sprintf("reasoning effort %q exceeds this group's limit of %q", requested, max)
}

// NormalizeMaxReasoningEffort 校验并标准化分组策略值。
// 空字符串表示分组不设置上限。
func NormalizeMaxReasoningEffort(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	value = strings.NewReplacer("-", "", "_", "", " ", "").Replace(value)
	switch value {
	case "":
		return ""
	case "minimal":
		return "minimal"
	case "low":
		return "low"
	case "medium":
		return "medium"
	case "high":
		return "high"
	case "xhigh", "extrahigh":
		return "xhigh"
	case "max":
		return "max"
	default:
		return ""
	}
}

// NormalizeReasoningEffortMappingValue 标准化映射规则使用的值。none 只表示
// 上游协议中的关闭推理状态，不能参与分组上限的强度比较。
func NormalizeReasoningEffortMappingValue(raw string) string {
	value := strings.ToLower(strings.TrimSpace(raw))
	value = strings.NewReplacer("-", "", "_", "", " ", "").Replace(value)
	if value == "none" {
		return "none"
	}
	return NormalizeMaxReasoningEffort(value)
}

// NormalizeRequestedOpenAIReasoningEffort 仅用于记录客户端显式请求值。none 没有
// 可比较的强度排名，不能作为分组上限，但必须保留在请求审计中。
func NormalizeRequestedOpenAIReasoningEffort(raw string) string {
	return NormalizeReasoningEffortMappingValue(raw)
}

func ReasoningEffortValuesForPlatform(platform string) []string {
	switch platform {
	case capability.PlatformOpenAI:
		return protocol.OpenAIReasoningEfforts()
	case capability.PlatformAnthropic:
		return protocol.AnthropicReasoningEfforts()
	default:
		return nil
	}
}

func ReasoningEffortMappingValuesForPlatform(platform string) []string {
	switch platform {
	case capability.PlatformOpenAI:
		return protocol.OpenAIReasoningMappingValues()
	case capability.PlatformAnthropic:
		return protocol.AnthropicReasoningEfforts()
	default:
		return nil
	}
}

func NormalizeMaxReasoningEffortForPlatform(platform, raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}

	allowedValues := ReasoningEffortValuesForPlatform(platform)
	if len(allowedValues) == 0 {
		return "", fmt.Errorf("reasoning effort policy is only supported for platforms %q and %q", capability.PlatformAnthropic, capability.PlatformOpenAI)
	}

	value := NormalizeMaxReasoningEffort(raw)
	for _, allowed := range allowedValues {
		if value == allowed {
			return value, nil
		}
	}
	return "", fmt.Errorf(
		"reasoning effort %q is not supported for platform %q; allowed values: %s",
		raw,
		platform,
		strings.Join(allowedValues, ", "),
	)
}

func NormalizeReasoningEffortMappingValueForPlatform(platform, raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}

	allowedValues := ReasoningEffortMappingValuesForPlatform(platform)
	if len(allowedValues) == 0 {
		return "", fmt.Errorf("reasoning effort policy is only supported for platforms %q and %q", capability.PlatformAnthropic, capability.PlatformOpenAI)
	}

	value := NormalizeReasoningEffortMappingValue(raw)
	for _, allowed := range allowedValues {
		if value == allowed {
			return value, nil
		}
	}
	return "", fmt.Errorf(
		"reasoning effort mapping value %q is not supported for platform %q; allowed values: %s",
		raw,
		platform,
		strings.Join(allowedValues, ", "),
	)
}

// NormalizeMaxReasoningEffortOverLimit 将超限动作归一化；空值兼容历史默认降档。
func NormalizeMaxReasoningEffortOverLimit(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", ReasoningEffortOverLimitDowngrade:
		return ReasoningEffortOverLimitDowngrade
	case ReasoningEffortOverLimitDeny:
		return ReasoningEffortOverLimitDeny
	default:
		return ""
	}
}

// NormalizeMaxReasoningEffortOverLimitForPlatform 校验动作是否能用于目标平台。
// 降档是无副作用的兼容默认值；拒绝仅对当前 fork 支持的 OpenAI 分组开放。
func NormalizeMaxReasoningEffortOverLimitForPlatform(platform, raw string) (string, error) {
	value := NormalizeMaxReasoningEffortOverLimit(raw)
	if value == "" {
		return "", fmt.Errorf(
			"reasoning effort over-limit action %q is not supported; allowed values: %s, %s",
			raw,
			ReasoningEffortOverLimitDowngrade,
			ReasoningEffortOverLimitDeny,
		)
	}
	if value == ReasoningEffortOverLimitDowngrade {
		return value, nil
	}
	if platform != capability.PlatformAnthropic && platform != capability.PlatformOpenAI {
		return "", fmt.Errorf(
			"reasoning effort over-limit deny is only supported for platforms %q and %q",
			capability.PlatformAnthropic,
			capability.PlatformOpenAI,
		)
	}
	return value, nil
}

func ReasoningEffortRank(raw string) (int, bool) {
	switch NormalizeMaxReasoningEffort(raw) {
	case "minimal":
		return 1, true
	case "low":
		return 2, true
	case "medium":
		return 3, true
	case "high":
		return 4, true
	case "xhigh":
		return 5, true
	case "max":
		return 6, true
	default:
		return 0, false
	}
}

// NormalizeReasoningEffortMappings 根据 OpenAI 分组支持的档位及协议关闭值，
// 校验并标准化分组映射规则。
func NormalizeReasoningEffortMatchType(matchType, model string) (string, error) {
	model = strings.TrimSpace(model)
	matchType = strings.ToLower(strings.TrimSpace(matchType))
	if model == "" {
		return "", nil
	}
	switch matchType {
	case "", ReasoningEffortMatchExact:
		return ReasoningEffortMatchExact, nil
	case ReasoningEffortMatchPrefix:
		return ReasoningEffortMatchPrefix, nil
	case ReasoningEffortMatchSuffix:
		return ReasoningEffortMatchSuffix, nil
	default:
		return "", fmt.Errorf("invalid match_type %q", matchType)
	}
}

func ReasoningEffortMappingDuplicateKey(from, matchType, model string) string {
	return from + "\x00" + matchType + "\x00" + strings.ToLower(strings.TrimSpace(model))
}

func RequestModelMatchesReasoningEffortMapping(requestModel, matchType, mappingModel string) bool {
	scope := strings.ToLower(strings.TrimSpace(mappingModel))
	req := strings.ToLower(strings.TrimSpace(requestModel))
	switch matchType {
	case "":
		return true
	case ReasoningEffortMatchExact:
		return scope != "" && scope == req
	case ReasoningEffortMatchPrefix:
		return scope != "" && req != "" && strings.HasPrefix(req, scope)
	case ReasoningEffortMatchSuffix:
		return scope != "" && req != "" && strings.HasSuffix(req, scope)
	default:
		return false
	}
}

func SelectReasoningEffortMapping(mappings []ReasoningEffortMapping, from, requestModel string) (ReasoningEffortMapping, bool) {
	type candidate struct {
		mapping       ReasoningEffortMapping
		matchStrength int
		patternLen    int
		index         int
	}
	candidates := make([]candidate, 0, len(mappings))
	for i, mapping := range mappings {
		if NormalizeReasoningEffortMappingValue(mapping.From) != from {
			continue
		}
		model := strings.TrimSpace(mapping.Model)
		matchType, err := NormalizeReasoningEffortMatchType(mapping.MatchType, model)
		if err != nil {
			continue
		}
		if !RequestModelMatchesReasoningEffortMapping(requestModel, matchType, model) {
			continue
		}
		strength := 1
		patternLen := 0
		switch matchType {
		case ReasoningEffortMatchExact:
			strength = 3
		case ReasoningEffortMatchPrefix, ReasoningEffortMatchSuffix:
			strength = 2
			patternLen = len(strings.ToLower(model))
		}
		candidates = append(candidates, candidate{
			mapping:       mapping,
			matchStrength: strength,
			patternLen:    patternLen,
			index:         i,
		})
	}
	if len(candidates) == 0 {
		return ReasoningEffortMapping{}, false
	}
	best := candidates[0]
	for _, item := range candidates[1:] {
		if item.matchStrength != best.matchStrength {
			if item.matchStrength > best.matchStrength {
				best = item
			}
			continue
		}
		if item.patternLen != best.patternLen {
			if item.patternLen > best.patternLen {
				best = item
			}
			continue
		}
		if item.index < best.index {
			best = item
		}
	}
	return best.mapping, true
}

func NormalizeReasoningEffortMappings(platform string, raw []ReasoningEffortMapping) ([]ReasoningEffortMapping, error) {
	if len(raw) > MaxReasoningEffortMappings {
		return nil, fmt.Errorf("reasoning effort mappings cannot exceed %d entries", MaxReasoningEffortMappings)
	}

	normalized := make([]ReasoningEffortMapping, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for i, mapping := range raw {
		from := NormalizeReasoningEffortMappingValue(mapping.From)
		to := NormalizeReasoningEffortMappingValue(mapping.To)
		if from == "" || to == "" {
			return nil, fmt.Errorf("reasoning effort mapping %d contains an empty or unknown value", i+1)
		}
		if len(from) > MaxReasoningEffortValueLen || len(to) > MaxReasoningEffortValueLen {
			return nil, fmt.Errorf("reasoning effort mapping %d values cannot exceed %d characters", i+1, MaxReasoningEffortValueLen)
		}
		if _, err := NormalizeReasoningEffortMappingValueForPlatform(platform, from); err != nil {
			return nil, fmt.Errorf("reasoning effort mapping %d source: %w", i+1, err)
		}
		if _, err := NormalizeReasoningEffortMappingValueForPlatform(platform, to); err != nil {
			return nil, fmt.Errorf("reasoning effort mapping %d target: %w", i+1, err)
		}
		model := strings.TrimSpace(mapping.Model)
		if len(model) > MaxReasoningEffortModelLen {
			return nil, fmt.Errorf("reasoning effort mapping %d model cannot exceed %d characters", i+1, MaxReasoningEffortModelLen)
		}
		matchType, err := NormalizeReasoningEffortMatchType(mapping.MatchType, model)
		if err != nil {
			return nil, fmt.Errorf("reasoning effort mapping %d: %w", i+1, err)
		}
		key := ReasoningEffortMappingDuplicateKey(from, matchType, model)
		if _, exists := seen[key]; exists {
			if model == "" {
				return nil, fmt.Errorf("duplicate reasoning effort mapping source %q", from)
			}
			return nil, fmt.Errorf("duplicate reasoning effort mapping source %q for %s model %q", from, matchType, strings.ToLower(model))
		}
		seen[key] = struct{}{}
		normalized = append(normalized, ReasoningEffortMapping{
			From:      from,
			To:        to,
			MatchType: matchType,
			Model:     model,
		})
	}
	return normalized, nil
}

func MapReasoningEffort(raw string, mappings []ReasoningEffortMapping, requestModel string) (string, bool) {
	value := strings.TrimSpace(raw)
	canonical := NormalizeReasoningEffortMappingValue(value)
	if canonical == "" {
		return value, false
	}
	mapping, ok := SelectReasoningEffortMapping(mappings, canonical, requestModel)
	if !ok {
		return value, false
	}
	return strings.TrimSpace(mapping.To), true
}
