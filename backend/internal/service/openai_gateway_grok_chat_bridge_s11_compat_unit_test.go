//go:build unit

// 仅保留既有测试的私有兼容入口；生产实现已迁出。
package service

import (
	nativegrok "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

const grokChatRawEndpoint = nativegrok.GrokChatRawEndpoint
