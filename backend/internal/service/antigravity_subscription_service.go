// 旧账号套餐入口委托 account 的唯一规则。
package service

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
)

type AntigravitySubscriptionResult = account.AntigravitySubscriptionResult

func NormalizeAntigravitySubscription(resp *antigravity.LoadCodeAssistResponse) AntigravitySubscriptionResult {
	return account.NormalizeAntigravitySubscription(resp)
}
