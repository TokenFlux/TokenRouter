package googleapi

import "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

// HTTPStatusToGoogleStatus 保留旧 HTTP 状态映射入口。
func HTTPStatusToGoogleStatus(status int) string { return httpapi.HTTPStatusToGoogleStatus(status) }
