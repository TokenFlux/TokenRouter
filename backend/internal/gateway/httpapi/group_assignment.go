package httpapi

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
)

// UngroupedKeySettings 只提供最终未分组 Key 的部署策略读取。
type UngroupedKeySettings interface{ IsUngroupedKeySchedulingAllowed(context.Context) bool }

// GroupAssignmentAccess 是 HTTP 门禁的只读资格投影，不包含凭据或余额。
type GroupAssignmentAccess struct{ Loaded, Assigned, CompositeNoGroup bool }
type GroupAssignmentOptions struct {
	Access     func(*gin.Context) GroupAssignmentAccess
	WriteError func(*gin.Context, int, string)
	Rejected   func(*gin.Context)
}

// RequireGroupAssignment 保留未分组裁决与错误顺序，普通 Key 不读取请求体。
func RequireGroupAssignment(settings UngroupedKeySettings, options GroupAssignmentOptions) gin.HandlerFunc {
	return func(c *gin.Context) {
		access := options.Access(c)
		if !access.Loaded || access.Assigned || access.CompositeNoGroup {
			c.Next()
			return
		}
		if settings.IsUngroupedKeySchedulingAllowed(c.Request.Context()) {
			c.Next()
			return
		}
		if options.Rejected != nil {
			options.Rejected(c)
		}
		options.WriteError(c, http.StatusForbidden, "API Key is not assigned to any group and cannot be used. Please contact the administrator to assign it to a group.")
		c.Abort()
	}
}

// EffectiveGroupAssignment 读取已完成 Key 认证和复合选组后的原生请求快照。
func EffectiveGroupAssignment(c *gin.Context) GroupAssignmentAccess {
	key, ok := EffectiveAPIKey(c)
	if !ok || key == nil {
		return GroupAssignmentAccess{}
	}
	_, noGroup := c.Get(CompositeKeyNoGroupContextKey)
	return GroupAssignmentAccess{Loaded: true, Assigned: key.GroupID != nil, CompositeNoGroup: key.IsComposite && noGroup}
}
