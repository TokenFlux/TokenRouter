// 模型报文缓存只在单请求内复用替换结果，不修改原始客户端载荷。
package requeststate

import (
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// ModelBodyReplacer 由报文编码 Adapter 注入。
type ModelBodyReplacer func([]byte, string) []byte

// GroupMappedModel 返回分组映射模型 G；分组映射没有有效结果时保留客户端模型 R。
func GroupMappedModel(requestedModel string, mapping routing.GroupMappingResult) string {
	routingModel := strings.TrimSpace(mapping.MappedModel)
	if routingModel == "" {
		return strings.TrimSpace(requestedModel)
	}
	return routingModel
}

// ModelMappedBody 在存在分组映射时返回替换模型后的请求体。
func ModelMappedBody(body []byte, mapped bool, mappedModel string, replace ModelBodyReplacer) []byte {
	if !mapped || replace == nil {
		return body
	}
	return replace(body, mappedModel)
}

// NewModelMappedBodyCache 缓存同一入口请求体的模型替换结果，避免账号切换重试时重复解析 JSON。
func NewModelMappedBodyCache(body []byte, replace ModelBodyReplacer) func(bool, string) []byte {
	replacedBodies := make(map[string][]byte)
	return func(mapped bool, mappedModel string) []byte {
		if !mapped {
			return body
		}
		if cachedBody, ok := replacedBodies[mappedModel]; ok {
			return cachedBody
		}
		replacedBody := ModelMappedBody(body, true, mappedModel, replace)
		replacedBodies[mappedModel] = replacedBody
		return replacedBody
	}
}
