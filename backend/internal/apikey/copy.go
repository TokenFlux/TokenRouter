package apikey

import (
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// CopyAPIKey 保持原管理/执行投影的复制边界；认证缓存另按 v40 的不可变快照复制。
func CopyAPIKey(value *APIKey) *APIKey {
	if value == nil {
		return nil
	}
	out := *value
	out.User = identity.CopyUser(value.User)
	out.ActorUser = identity.CopyUser(value.ActorUser)
	out.Group = routing.CloneGroup(value.Group)
	out.CompositeGroups = CopyCompositeGroups(value.CompositeGroups)
	return &out
}

// CopyCompositeGroup 保留绑定顺序和请求级覆盖，分组策略不与输入共享。
func CopyCompositeGroup(value APIKeyCompositeGroup) APIKeyCompositeGroup {
	value.Group = routing.CloneGroup(value.Group)
	return value
}

func CopyCompositeGroups(values []APIKeyCompositeGroup) []APIKeyCompositeGroup {
	if values == nil {
		return nil
	}
	out := make([]APIKeyCompositeGroup, len(values))
	for i, value := range values {
		out[i] = CopyCompositeGroup(value)
	}
	return out
}

func CopyAPIKeys(values []APIKey) []APIKey {
	if values == nil {
		return nil
	}
	out := make([]APIKey, len(values))
	for i := range values {
		out[i] = *CopyAPIKey(&values[i])
	}
	return out
}
