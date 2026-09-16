//go:build unit

// 原单测保留旧名称，行为只由 HTTP Adapter 实现。
package handler

import gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

func isExpectedGrokRealtimeClose(err error) bool { return gatewayhttp.IsExpectedGrokRealtimeClose(err) }
