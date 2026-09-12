//go:build unit

// 兼容旧测试的私有入口；生产用例只在所属模块保留唯一实现。
package service

import (
	modelmap "github.com/TokenFlux/TokenRouter/internal/routing/modelmap"
)

// matchWildcardMappingResult 复用纯模型匹配，平台归一化由调用方负责。
func matchWildcardMappingResult(mapping map[string]string, requestedModel string) (string, bool) {
	return modelmap.MatchWildcard(mapping, requestedModel)
}
