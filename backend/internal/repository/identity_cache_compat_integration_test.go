//go:build integration

// 旧集成测试白盒入口只引用唯一指纹实现。
package repository

import native "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic/rediscache"

type identityCache = native.FingerprintStore

const fingerprintKeyPrefix = native.FingerprintKeyPrefix
const fingerprintTTL = native.FingerprintTTL
