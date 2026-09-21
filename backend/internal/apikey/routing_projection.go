package apikey

import "github.com/TokenFlux/TokenRouter/internal/routing"

// GroupFromRouting 保留认证与管理输入之间的副本边界。
func GroupFromRouting(group *routing.Group) *routing.Group {
	return routing.CloneGroup(group)
}

// RoutingGroup 返回请求独立副本，调用方不能修改认证缓存。
func RoutingGroup(group *routing.Group) *routing.Group {
	return routing.CloneGroup(group)
}
