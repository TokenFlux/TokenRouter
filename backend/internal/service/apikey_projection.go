// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
)

func GroupFromAPIKeyView(g *routing.Group) *routing.Group {
	return routing.CloneGroup(apikey.RoutingGroup(g))
}
