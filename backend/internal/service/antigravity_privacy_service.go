// 旧隐私入口复用账号规则，不再持有供应商请求算法。
package service

import (
	"context"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
)

const AntigravityPrivacySet = accountcore.AntigravityPrivacySet
const AntigravityPrivacyFailed = accountcore.AntigravityPrivacyFailed

func setAntigravityPrivacy(ctx context.Context, token, project, proxy string) string {
	core := accountcore.AntigravityAuthorization{Options: antigravityAuthorizationOptions(nil)}
	return core.SetPrivacy(ctx, token, project, proxy)
}
