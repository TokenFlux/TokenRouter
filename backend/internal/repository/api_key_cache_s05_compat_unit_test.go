//go:build unit

// 兼容旧测试的私有入口；生产用例只在所属模块保留唯一实现。
package repository

import (
	rediscache "github.com/TokenFlux/TokenRouter/internal/apikey/rediscache"
)

func apiKeyRateLimitKey(userID int64) string { return rediscache.ApiKeyRateLimitKey(userID) }
