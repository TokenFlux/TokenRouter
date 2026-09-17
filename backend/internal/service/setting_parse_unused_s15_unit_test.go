//go:build unit

package service

import (
	"github.com/TokenFlux/TokenRouter/internal/identity"
)

// 仅保留已有 unit 断言需要的旧入口，退出 S16。
func parseDefaultSubscriptions(raw string) []DefaultSubscriptionSetting {
	return identity.ParseDefaultSubscriptions(raw)
}
