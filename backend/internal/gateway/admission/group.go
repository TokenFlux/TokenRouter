package admission

import (
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
)

// GroupAllowed 保留已绑定 Key 的专属分组权限检查，缺失投影仍按原入口处理。
func GroupAllowed(key *apikey.APIKey) bool {
	if key == nil || key.GroupID == nil || key.User == nil || key.Group == nil {
		return true
	}
	return key.User.CanBindGroup(key.Group.ID, key.Group.IsExclusive)
}

// GroupAvailable 区分删除与停用，公开错误 reason 保持不变。
func GroupAvailable(key *apikey.APIKey) (string, string, bool) {
	if key == nil || key.GroupID == nil {
		return "", "", true
	}
	group := key.Group
	if group == nil || strings.EqualFold(group.Status, "deleted") {
		return "GROUP_DELETED", "API Key 所属分组已删除", false
	}
	if !group.IsActive() {
		return "GROUP_DISABLED", "API Key 所属分组已停用", false
	}
	return "", "", true
}
