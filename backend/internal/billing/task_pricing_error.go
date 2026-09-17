// 任务报价缺失保持历史 HTTP 类别和 reason；任务模块复用同一错误身份。
package billing

import "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"

var ErrImageTaskPricingMissing = apperror.New(apperror.CategoryBadRequest, "BATCH_IMAGE_SETTLEMENT_PRICING_MISSING", "batch image settlement pricing is missing")
