package service

import gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

// 测试通过别名确认生产路径确实安装同一 HTTP 包装器。
type openAICompactKeepaliveWriter = gatewayhttp.CompactKeepaliveWriter
