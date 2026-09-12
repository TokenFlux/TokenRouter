// 本文件维护 apikey 的所属能力；兼容入口复用唯一实现。
package apikey

import (
	billing "github.com/TokenFlux/TokenRouter/internal/billing"
)

// ErrAPIKeyNotFound 保持原认证错误的类别和 reason。
var ErrAPIKeyNotFound = billing.ErrAPIKeyNotFound
