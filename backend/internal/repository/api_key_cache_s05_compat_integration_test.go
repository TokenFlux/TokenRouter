//go:build integration

// 兼容旧测试的私有入口；生产用例只在所属模块保留唯一实现。
package repository

import (
	rediscache "github.com/TokenFlux/TokenRouter/internal/apikey/rediscache"
)

const apiKeyRateLimitKeyPrefix = rediscache.ApiKeyRateLimitKeyPrefix

const apiKeyRateLimitDuration = rediscache.ApiKeyRateLimitDuration

type apiKeyCache = rediscache.ApiKeyCache
