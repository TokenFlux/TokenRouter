// 运维模板占位符由通知叶子契约唯一声明。
package ops

import "github.com/TokenFlux/TokenRouter/internal/notification/contract"

var notificationEmailOpsSummaryPlaceholders = contract.SummaryPlaceholders()

func SummaryPlaceholders() []string { return contract.SummaryPlaceholders() }
