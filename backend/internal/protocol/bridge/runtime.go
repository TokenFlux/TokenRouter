package bridge

import "time"

// Runtime 显式提供转换需要的时刻及随机字节；调用方保留来源、格式和失败语义。
// 转换器不读取环境，不安装全局时钟或随机源。
// @project-doc docs/architecture/gateway_request_lifecycle.md#protocol_conversion_boundary
type Runtime struct {
	Now        func() time.Time
	ReadRandom func([]byte) (int, error)
}
