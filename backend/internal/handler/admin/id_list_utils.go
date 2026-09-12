// 本文件维护 admin 的所属能力；兼容入口复用唯一实现。
package admin

import (
	httpx "github.com/TokenFlux/TokenRouter/internal/server/httpx"
)

func normalizeInt64IDList(ids []int64) []int64 { return httpx.NormalizeInt64IDList(ids) }
