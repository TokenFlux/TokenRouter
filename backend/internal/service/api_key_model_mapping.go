package service

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	infraerrors "github.com/BrandonVee/TokenRouter/internal/pkg/errors"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	// MaxAPIKeyModelMappingRules 限制鉴权快照与请求热路径中的规则数量。
	MaxAPIKeyModelMappingRules = 100
	// MaxAPIKeyModelNameRunes 与 usage_logs 的模型字段长度保持一致。
	MaxAPIKeyModelNameRunes = 100
)

var ErrInvalidAPIKeyModelMapping = infraerrors.BadRequest(
	"INVALID_API_KEY_MODEL_MAPPING",
	"invalid API key model mapping",
)

// NormalizeAPIKeyModelMapping 校验规则并返回去除首尾空格后的独立副本。
func NormalizeAPIKeyModelMapping(mapping map[string]string) (map[string]string, error) {
	if len(mapping) > MaxAPIKeyModelMappingRules {
		return nil, fmt.Errorf("%w: at most %d rules are allowed", ErrInvalidAPIKeyModelMapping, MaxAPIKeyModelMappingRules)
	}

	normalized := make(map[string]string, len(mapping))
	for rawSource, rawTarget := range mapping {
		source := strings.TrimSpace(rawSource)
		target := strings.TrimSpace(rawTarget)
		if source == "" || target == "" {
			return nil, fmt.Errorf("%w: source and target are required", ErrInvalidAPIKeyModelMapping)
		}
		if utf8.RuneCountInString(source) > MaxAPIKeyModelNameRunes || utf8.RuneCountInString(target) > MaxAPIKeyModelNameRunes {
			return nil, fmt.Errorf("%w: model names must not exceed %d characters", ErrInvalidAPIKeyModelMapping, MaxAPIKeyModelNameRunes)
		}
		if strings.Count(source, "*") > 1 || (strings.Contains(source, "*") && !strings.HasSuffix(source, "*")) {
			return nil, fmt.Errorf("%w: source wildcard is only allowed once at the end", ErrInvalidAPIKeyModelMapping)
		}
		if strings.Contains(target, "*") {
			return nil, fmt.Errorf("%w: target wildcard is not allowed", ErrInvalidAPIKeyModelMapping)
		}
		if source == target {
			return nil, fmt.Errorf("%w: source and target must differ", ErrInvalidAPIKeyModelMapping)
		}
		if _, exists := normalized[source]; exists {
			return nil, fmt.Errorf("%w: duplicate source after trimming", ErrInvalidAPIKeyModelMapping)
		}
		normalized[source] = target
	}
	return normalized, nil
}

// ResolveModelMapping 只执行一次精确或最长尾通配匹配，不递归处理目标模型。
func ResolveModelMapping(mapping map[string]string, requestedModel string) (string, bool) {
	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel == "" || len(mapping) == 0 {
		return requestedModel, false
	}
	return resolveRequestedModelInMapping(mapping, requestedModel)
}

// ResolveModelMapping 返回当前 API Key 的一次重定向结果。
func (k *APIKey) ResolveModelMapping(requestedModel string) (string, bool) {
	if k == nil {
		return strings.TrimSpace(requestedModel), false
	}
	return ResolveModelMapping(k.ModelMapping, requestedModel)
}

// CloneModelMapping 返回可安全写入缓存或响应的独立副本。
func CloneModelMapping(mapping map[string]string) map[string]string {
	cloned := make(map[string]string, len(mapping))
	for source, target := range mapping {
		cloned[source] = target
	}
	return cloned
}

// AppendAPIKeyModelAliases 保留原模型顺序，并按名称排序追加目标当前可请求的精确别名。
func AppendAPIKeyModelAliases(models []string, mapping map[string]string) []string {
	seen := make(map[string]struct{}, len(models)+len(mapping))
	result := make([]string, 0, len(models)+len(mapping))
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if _, exists := seen[model]; exists {
			continue
		}
		seen[model] = struct{}{}
		result = append(result, model)
	}
	result = append(result, AvailableAPIKeyModelAliases(models, mapping)...)
	return result
}

// AvailableAPIKeyModelAliases 返回目标位于当前可请求集合且尚未存在的精确别名。
func AvailableAPIKeyModelAliases(models []string, mapping map[string]string) []string {
	availableTargets := make(map[string]struct{}, len(models))
	seen := make(map[string]struct{}, len(models)+len(mapping))
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		availableTargets[model] = struct{}{}
		seen[model] = struct{}{}
	}
	aliases := make([]string, 0, len(mapping))
	for source, target := range mapping {
		if strings.Contains(source, "*") {
			continue
		}
		if _, available := availableTargets[target]; !available {
			continue
		}
		if _, exists := seen[source]; exists {
			continue
		}
		aliases = append(aliases, source)
	}
	sort.Strings(aliases)
	return aliases
}

// RewriteAPIKeyAdditionalModels 重定向 Responses 工具声明中的附加模型。
func RewriteAPIKeyAdditionalModels(body []byte, mapping map[string]string) ([]byte, error) {
	if len(body) == 0 || len(mapping) == 0 || !gjson.ValidBytes(body) {
		return body, nil
	}
	rewritten := body
	for index, tool := range gjson.GetBytes(body, "tools").Array() {
		model := strings.TrimSpace(tool.Get("model").String())
		mappedModel, matched := ResolveModelMapping(mapping, model)
		if !matched {
			continue
		}
		var err error
		rewritten, err = sjson.SetBytes(rewritten, fmt.Sprintf("tools.%d.model", index), mappedModel)
		if err != nil {
			return body, err
		}
	}
	return rewritten, nil
}
