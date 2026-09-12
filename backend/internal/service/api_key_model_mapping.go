// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	fmt "fmt"
	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	gjson "github.com/tidwall/gjson"
	sjson "github.com/tidwall/sjson"
	strings "strings"
)

const MaxAPIKeyModelMappingRules = apikey.MaxAPIKeyModelMappingRules

const MaxAPIKeyModelNameRunes = apikey.MaxAPIKeyModelNameRunes

var ErrInvalidAPIKeyModelMapping = apikey.ErrInvalidAPIKeyModelMapping

// NormalizeAPIKeyModelMapping 委托 Key 模块的唯一实现。
func NormalizeAPIKeyModelMapping(mapping map[string]string) (map[string]string, error) {
	return apikey.NormalizeAPIKeyModelMapping(mapping)
}

// ResolveModelMapping 委托 Key 模块的唯一实现。
func ResolveModelMapping(mapping map[string]string, requestedModel string) (string, bool) {
	return apikey.ResolveModelMapping(mapping, requestedModel)
}

// ResolveModelMapping 委托 Key 模块的唯一实现。
func (k *APIKey) ResolveModelMapping(requestedModel string) (string, bool) {
	coreKey := APIKeyView(k)
	kView := APIKeyView(k)
	result0, result1 := coreKey.ResolveModelMapping(requestedModel)
	ApplyAPIKeyView(k, coreKey)
	ApplyAPIKeyView(k, kView)
	return result0, result1
}

// CloneModelMapping 委托 Key 模块的唯一实现。
func CloneModelMapping(mapping map[string]string) map[string]string {
	return apikey.CloneModelMapping(mapping)
}

// AppendAPIKeyModelAliases 委托 Key 模块的唯一实现。
func AppendAPIKeyModelAliases(models []string, mapping map[string]string) []string {
	return apikey.AppendAPIKeyModelAliases(models, mapping)
}

// AvailableAPIKeyModelAliases 委托 Key 模块的唯一实现。
func AvailableAPIKeyModelAliases(models []string, mapping map[string]string) []string {
	return apikey.AvailableAPIKeyModelAliases(models, mapping)
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
