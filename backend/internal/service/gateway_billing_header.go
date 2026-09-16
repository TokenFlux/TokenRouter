// 旧入口只委托平台标记同步规则。
package service

import claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"

func syncBillingHeaderVersion(body []byte, userAgent string) []byte {
	return claude.SyncBillingHeaderVersion(body, userAgent)
}
