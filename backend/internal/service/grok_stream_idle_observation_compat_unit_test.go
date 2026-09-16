//go:build unit

// 只供原 unit 断言的兼容别名。
package service

import (
	nativegrok "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// gateway.stream_data_interval_timeout 为 0 时使用该 Grok 流空闲默认值，
// 既容纳慢推理模型，又能及时释放挂起连接。
const defaultGrokStreamIdleTimeout = nativegrok.DefaultStreamIdleTimeout
