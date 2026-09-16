//go:build unit

// 保留原标签测试的私有委托，生产代码不保留测试专用入口。
package service

import (
	native "github.com/TokenFlux/TokenRouter/internal/moderation"
)

type moderationAPIResponse = native.LegacyModerationAPIResponse

type moderationAPIResult = native.LegacyModerationAPIResult
