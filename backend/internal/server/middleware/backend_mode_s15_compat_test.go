package middleware

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	"github.com/gin-gonic/gin"
)

// 旧签名只用于原断言，保留 nil 的原门控语义。
func BackendModeUserGuard(s *admission.BackendMode) gin.HandlerFunc {
	var r identityhttp.BackendModeReader
	if s != nil {
		r = s
	}
	return identityhttp.BackendModeUserGuard(r)
}
func BackendModeAuthGuard(s *admission.BackendMode) gin.HandlerFunc {
	var r identityhttp.BackendModeReader
	if s != nil {
		r = s
	}
	return identityhttp.BackendModeAuthGuard(r)
}
