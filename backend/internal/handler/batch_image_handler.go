// 批量任务 HTTP 的兼容构造入口，路由调用新 Adapter 的同一实现。
package handler

import (
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	native "github.com/TokenFlux/TokenRouter/internal/batchimage/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
)

type BatchImageHandler = native.BatchImageHandler

func NewBatchImageHandler(s *service.BatchImagePublicService, d *service.BatchImageDownloadService, c *service.BatchImageCleanupService) *BatchImageHandler {
	return native.NewBatchImageHandler(s, d, c, BatchImageAccessPorts())
}

// BatchImageAccessPorts 仅投影旧认证上下文，任务 Adapter 不导入旧聚合服务。
func BatchImageAccessPorts() native.AccessPorts {
	return native.AccessPorts{Key: func(c *gin.Context) (*apikey.APIKey, bool) {
		k, ok := middleware.GetAPIKeyFromContext(c)
		return service.APIKeyView(k), ok
	}, PreferredSubscription: func(c *gin.Context) (*billing.UserSubscription, bool) {
		v, ok := middleware.GetAPIKeyBillingContext(c)
		if !ok || v == nil || v.Mode != service.APIKeyBillingModeSubscription || v.Subscription == nil {
			return nil, false
		}
		return v.Subscription, true
	}, SessionID: service.ExtractClientSessionID}
}

func appendBatchImageAPIKeyModelAliases(models []service.BatchImagePublicModel, mapping map[string]string) []service.BatchImagePublicModel {
	return native.AppendBatchImageAPIKeyModelAliases(models, mapping)
}
