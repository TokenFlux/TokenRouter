//go:build unit

// 这些旧测试入口只委托新实现；生产已无消费者。
package admin

import (
	httpx "github.com/TokenFlux/TokenRouter/internal/server/httpx"
)

func normalizeInt64IDList(ids []int64) []int64 { return httpx.NormalizeInt64IDList(ids) }
