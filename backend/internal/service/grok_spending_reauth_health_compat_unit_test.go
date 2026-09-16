//go:build unit

// 生产调用已迁出，旧断言继续验证同一实现。
package service

import (
	"time"
)

// 消费限额会在已观测账期结束后恢复。
// 缺少账单快照时使用较短的探测周期，不根据错误到达时间虚构 24 小时边界。
const grokSpendingLimitProbeCooldown = 10 * time.Minute
