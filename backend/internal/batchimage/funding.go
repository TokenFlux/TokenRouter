// 任务模块拥有资金动作身份，历史前缀保持不变。
package batchimage

import (
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/billing"
)

const FundingScope billing.TaskScope = "batchimage"

func FundingReference(id string) billing.TaskReference {
	id = strings.TrimSpace(id)
	return billing.TaskReference{Scope: FundingScope, ID: id, ReserveRequestID: "batch_image_hold:" + id}
}
