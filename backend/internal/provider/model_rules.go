package provider

import (
	"maps"
	"slices"
	"sort"
	"strings"
	"unicode"
)

// ModelRulesSnapshot 保存一次查询使用的模型配置和匹配索引。
type ModelRulesSnapshot struct {
	record          *Record
	rules           ModelPlatformRules
	mapping         map[string]string
	mappingPatterns []string
	exact           map[string]struct{}
	qoder           map[string]struct{}
	prefixes        []string
	configured      []string
}

// PrepareModelRules 读取一次映射、白名单和平台型号，后续匹配复用这些值。
func PrepareModelRules(record *Record, defaults ModelMappingDefaults, rules ModelPlatformRules) *ModelRulesSnapshot {
	result := &ModelRulesSnapshot{record: record, rules: rules, exact: map[string]struct{}{}}
	if record == nil {
		return result
	}
	result.mapping = ResolveModelMapping(record, defaults)
	for pattern := range result.mapping {
		if strings.HasSuffix(pattern, "*") {
			result.mappingPatterns = append(result.mappingPatterns, pattern)
		}
	}
	sort.Slice(result.mappingPatterns, func(i, j int) bool {
		left, right := result.mappingPatterns[i], result.mappingPatterns[j]
		if len(left) != len(right) {
			return len(left) > len(right)
		}
		return left < right
	})
	var platformModels []string
	if defaults.Models != nil {
		platformModels = defaults.Models(record)
		defaults.Models = func(*Record) []string { return platformModels }
	}
	scope := record.effectiveModelScope(defaults, result.mapping)
	for pattern := range scope {
		pattern = strings.TrimSpace(pattern)
		result.exact[modelWhitelistKey(pattern)] = struct{}{}
		if prefix, wildcard := strings.CutSuffix(pattern, "*"); wildcard {
			result.prefixes = append(result.prefixes, strings.ToLower(prefix))
		}
		if record.Platform == PlatformQoder && rules.NormalizeQoder != nil {
			if result.qoder == nil {
				result.qoder = map[string]struct{}{}
			}
			result.qoder[rules.NormalizeQoder(pattern)] = struct{}{}
		}
	}
	result.configured = configuredRequestModels(result.mapping, scope, platformModels, result.whitelistMember)
	return result
}

// Mapping 返回可由调用者持有的映射副本。
func (s *ModelRulesSnapshot) Mapping() map[string]string { return maps.Clone(s.mapping) }

// ConfiguredModels 返回配置及平台执行规则提供的具体名称。
func (s *ModelRulesSnapshot) ConfiguredModels() []string { return slices.Clone(s.configured) }

// ResolveMappedModel 对本次查询捕获的映射执行一跳改写。
func (s *ModelRulesSnapshot) ResolveMappedModel(model string) (string, bool) {
	if mapped, found := s.resolve(model); found {
		return mapped, true
	}
	if trimmed := strings.TrimSpace(model); trimmed != model {
		if mapped, found := s.resolve(trimmed); found {
			return mapped, true
		}
	}
	return model, false
}

func (s *ModelRulesSnapshot) resolve(model string) (string, bool) {
	if model == "" {
		return "", false
	}
	if mapped, found := s.mapping[model]; found {
		return mapped, true
	}
	for _, pattern := range s.mappingPatterns {
		if strings.HasPrefix(model, strings.TrimSuffix(pattern, "*")) {
			return s.mapping[pattern], true
		}
	}
	return model, false
}

// ForwardMappedModel 将未命中或空目标还原为请求名称。
func (s *ModelRulesSnapshot) ForwardMappedModel(model string) string {
	mapped, matched := s.ResolveMappedModel(model)
	if !matched || strings.TrimSpace(mapped) == "" {
		return model
	}
	return strings.TrimSpace(mapped)
}

// Supports 检查映射后的白名单和平台资格。
func (s *ModelRulesSnapshot) Supports(model string) bool {
	if s == nil || s.record == nil {
		return false
	}
	mapped, _ := s.ResolveMappedModel(model)
	return s.whitelisted(mapped) && s.record.modelPlatformAllows(mapped, s.rules)
}

// FinalModelWhitelisted 检查已完成平台规范化的最终型号。
func (s *ModelRulesSnapshot) FinalModelWhitelisted(model string) bool {
	if s == nil || s.record == nil {
		return false
	}
	if s.record.Platform == PlatformQoder && s.rules.QoderCompatible != nil && !s.rules.QoderCompatible(model) {
		return false
	}
	return s.whitelisted(model)
}

func (s *ModelRulesSnapshot) whitelisted(model string) bool {
	if s.whitelistMember(model) {
		return true
	}
	if s.qoder != nil {
		_, found := s.qoder[s.rules.NormalizeQoder(strings.TrimSpace(model))]
		return found
	}
	return false
}

func (s *ModelRulesSnapshot) whitelistMember(model string) bool {
	model = strings.TrimSpace(model)
	if _, found := s.exact[modelWhitelistKey(model)]; found {
		return true
	}
	lower := strings.ToLower(model)
	for _, prefix := range s.prefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// modelWhitelistKey 为 EqualFold 的 Unicode 等价字符生成相同的索引键。
func modelWhitelistKey(value string) string {
	return strings.Map(func(r rune) rune {
		if r < unicode.MaxASCII {
			if r >= 'A' && r <= 'Z' {
				return r + ('a' - 'A')
			}
			return r
		}
		minimum := r
		for next := unicode.SimpleFold(r); next != r; next = unicode.SimpleFold(next) {
			if next < minimum {
				minimum = next
			}
		}
		if minimum >= 'A' && minimum <= 'Z' {
			return minimum + ('a' - 'A')
		}
		return minimum
	}, value)
}

// configuredRequestModels 合并具体白名单、平台型号和映射两端的名称。
func configuredRequestModels(mapping map[string]string, scope map[string]struct{}, platformModels []string, allows func(string) bool) []string {
	models := make(map[string]struct{})
	for model := range scope {
		if !strings.Contains(model, "*") {
			models[model] = struct{}{}
		}
	}
	for _, model := range platformModels {
		if allows(model) {
			models[model] = struct{}{}
		}
	}
	for source, target := range mapping {
		if target != "" && !strings.Contains(target, "*") && allows(target) {
			models[target] = struct{}{}
		}
		if !strings.Contains(source, "*") && allows(target) {
			models[source] = struct{}{}
		}
	}
	out := make([]string, 0, len(models))
	for model := range models {
		out = append(out, model)
	}
	sort.Strings(out)
	return out
}
