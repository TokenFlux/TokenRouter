package httpapi

import "github.com/gin-gonic/gin"

// RegisterUnsubscribeRoute 保留公开设置组内的退订路径与客户端 IP 限流。
func RegisterUnsubscribeRoute(group *gin.RouterGroup, endpoint *Handler) {
	group.GET("/email-unsubscribe", endpoint.UnsubscribeNotificationEmail)
}
