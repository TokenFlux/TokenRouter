//go:build unit

// 兼容旧测试的私有入口；生产用例只在所属模块保留唯一实现。
package handler

import (
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
)

type passkeyBeginLoginRequest = identityhttp.PasskeyBeginLoginRequest
