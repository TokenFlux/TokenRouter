//go:build unit

package service

import account "github.com/TokenFlux/TokenRouter/internal/account"

// 旧关键词白盒测试只委托已迁纯规则。
func matchTempUnschedKeyword(body string, keys []string) string {
	return account.MatchTempUnschedKeyword(body, keys)
}
