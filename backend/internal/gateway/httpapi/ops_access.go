package httpapi

import (
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/gin-gonic/gin"
)

// OpsErrorLogQueue 只接收已冻结的观测值，队列生命周期由 app 管理。
type OpsErrorLogQueue interface {
	Enqueue(*ops.OpsService, *ops.OpsInsertErrorLogInput)
}

// OpsObservationAccess 区分错误日志的只读身份投影和准入拒绝，不执行身份认证。
type OpsObservationAccess struct {
	APIKey   func(*gin.Context) *apikey.APIKey
	Rejected func(*gin.Context) bool
}

func (a OpsObservationAccess) key(c *gin.Context) *apikey.APIKey {
	if a.APIKey == nil {
		return nil
	}
	return a.APIKey(c)
}

func (a OpsObservationAccess) rejected(c *gin.Context) bool {
	return a.Rejected != nil && a.Rejected(c)
}
