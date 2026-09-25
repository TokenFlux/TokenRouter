// 本文件维护 routing 的所属能力；兼容入口复用唯一实现。
package routing

import (
	"github.com/TokenFlux/TokenRouter/internal/routing/accessview"
)

// CloneGroup 隔离分组值；账户等读方复用相同的叶子复制实现。
func CloneGroup(g *Group) *Group {
	return (*Group)(accessview.CloneGroupConfig((*accessview.GroupConfig)(g)))
}

// CloneGroups 保留 nil/空集合并逐项隔离可变分组配置。
func CloneGroups(values []Group) []Group {
	if values == nil {
		return nil
	}
	out := make([]Group, len(values))
	for i := range values {
		out[i] = *CloneGroup(&values[i])
	}
	return out
}
