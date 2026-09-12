//go:build unit

// 兼容旧测试的私有入口；生产用例只在所属模块保留唯一实现。
package service

import (
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
)

func mergePlatformQuotaDefaults(dst, src *DefaultPlatformQuotaSetting) {
	identity.MergePlatformQuotaDefaults(dst, src)
}
