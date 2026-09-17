package middleware

import (
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
)

// 旧签名只用于原断言，保留 nil 的原门控语义。
func BackendModeUserGuard(s *service.SettingService) gin.HandlerFunc {
	var r identityhttp.BackendModeReader
	if s != nil {
		r = s
	}
	return identityhttp.BackendModeUserGuard(r)
}
func BackendModeAuthGuard(s *service.SettingService) gin.HandlerFunc {
	var r identityhttp.BackendModeReader
	if s != nil {
		r = s
	}
	return identityhttp.BackendModeAuthGuard(r)
}
