// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/gateway/modeltrace"
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

// RewriteAPIKeyAdditionalModels 委托唯一网关报文改写。
func RewriteAPIKeyAdditionalModels(body []byte, mapping map[string]string) ([]byte, error) {
	return modeltrace.RewriteAPIKeyAdditionalModels(body, mapping)
}
