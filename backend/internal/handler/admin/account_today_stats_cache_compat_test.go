//go:build unit

// 本文件维护 admin 的所属能力；兼容入口复用唯一实现。
package admin

import (
	accounthttp "github.com/TokenFlux/TokenRouter/internal/account/httpapi"
)

// 旧 key 入口复用新 HTTP 唯一规则。
func buildAccountTodayStatsBatchCacheKey(ids []int64) string {
	return accounthttp.BuildAccountTodayStatsBatchCacheKey(ids)
}
