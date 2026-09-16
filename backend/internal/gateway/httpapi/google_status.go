// 旧协议 HTTP 入口委托通用 Google 状态映射，避免认证与网关 Adapter 互相依赖。
package httpapi

import "github.com/TokenFlux/TokenRouter/internal/server/httpx"

func HTTPStatusToGoogleStatus(status int) string { return httpx.HTTPStatusToGoogleStatus(status) }
