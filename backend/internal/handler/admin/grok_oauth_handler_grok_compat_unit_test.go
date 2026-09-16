//go:build unit

// 迁移后的私有测试转接不进入生产构建。
package admin

import (
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

func grokSSOImportCredentials(built map[string]any, reqCredentials map[string]any) map[string]any {
	return accountcore.GrokSSOImportCredentials(built, reqCredentials)
}
func grokSSOImportExpiry(requestExpiresAt *int64, requestAutoPause *bool, tokenInfo *service.GrokTokenInfo) (*int64, *bool) {
	return accountcore.GrokSSOImportExpiry(requestExpiresAt, requestAutoPause, tokenInfo)
}
