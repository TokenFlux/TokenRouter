// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package billing

import (
	apperror "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

var ErrRedeemCodeExists = apperror.Conflict("REDEEM_CODE_EXISTS", "redeem code already exists")

var ErrRedeemCodeNotFound = apperror.NotFound("REDEEM_CODE_NOT_FOUND", "redeem code not found")

var ErrRedeemCodeUsed = apperror.Conflict("REDEEM_CODE_USED", "redeem code already used")

var ErrSubscriptionAlreadyExists = apperror.Conflict("SUBSCRIPTION_ALREADY_EXISTS", "subscription already exists")

var ErrSubscriptionNilInput = apperror.BadRequest("SUBSCRIPTION_NIL_INPUT", "subscription input cannot be nil")
