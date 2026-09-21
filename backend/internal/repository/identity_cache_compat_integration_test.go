//go:build integration

// 旧集成测试白盒入口只引用唯一指纹实现。
package repository

import "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic/rediscache"

type identityCache = rediscache.FingerprintStore

const fingerprintKeyPrefix = rediscache.FingerprintKeyPrefix
const fingerprintTTL = rediscache.FingerprintTTL
