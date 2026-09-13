// 本文件维护 routing 的所属能力；兼容入口复用唯一实现。
package routing

import (
	accessview "github.com/TokenFlux/TokenRouter/internal/routing/accessview"
)

// CloneGroup 隔离分组值；账户等读方复用相同的叶子复制实现。
func CloneGroup(g *Group) *Group {
	return (*Group)(accessview.CloneGroupConfig((*accessview.GroupConfig)(g)))
}
