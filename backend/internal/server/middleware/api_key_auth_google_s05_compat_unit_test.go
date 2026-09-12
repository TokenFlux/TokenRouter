//go:build unit

// 兼容旧测试的私有入口；生产用例只在所属模块保留唯一实现。
package middleware

import (
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
)

func googleTeamAPIKeyError(err error) (int, string, bool) { return keyhttp.GoogleTeamError(err) }
