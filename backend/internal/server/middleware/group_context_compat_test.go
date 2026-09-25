package middleware

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/gin-gonic/gin"
)

// 旧 context 安装仅供历史测试夹具使用。
func setGroupContext(c *gin.Context, group *routing.Group) {
	if !routing.IsGroupContextValid(group) {
		return
	}
	if existing, ok := requeststate.GroupFromContext(c.Request.Context()); ok && existing != nil && existing.ID == group.ID && routing.IsGroupContextValid(existing) {
		return
	}
	ctx := requeststate.WithGroup(c.Request.Context(), group)
	c.Request = c.Request.WithContext(ctx)
}
