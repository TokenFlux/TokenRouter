// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	"maps"
)

// ModelRoutingSnapshot 只持有本次模型匹配所需规则，不携带凭据或管理对象。
// 在原匹配时机创建，避免提前读取动态默认模型或引入第二份别名缓存。
type ModelRoutingSnapshot struct {
	platform string
	mapping  map[string]string
}

func NewModelRoutingSnapshot(platform string, mapping map[string]string) ModelRoutingSnapshot {
	return ModelRoutingSnapshot{platform: platform, mapping: maps.Clone(mapping)}
}
func (s ModelRoutingSnapshot) Resolve(requested string) (string, bool) {
	return ResolveMappedModel(s.platform, s.mapping, requested)
}
