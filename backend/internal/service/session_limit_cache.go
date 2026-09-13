// 旧组合接口保留调用形状；调度会话和资金窗口各自只有一份实现。
package service

import (
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

type SessionLimitCache interface {
	scheduler.SessionLimitCache
	billing.WindowCostCache
}
