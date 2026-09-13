// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/account"
)

type ScheduledTestRunnerService = account.ScheduledTestRunnerService

// LegacyScheduledRecovery 只投影原恢复结果，健康决策仍由原所属能力提供。
func LegacyScheduledRecovery(limits *RateLimitService) func(context.Context, int64) (*account.SuccessfulTestRecovery, error) {
	if limits == nil {
		return nil
	}
	return func(ctx context.Context, id int64) (*account.SuccessfulTestRecovery, error) {
		v, err := limits.RecoverAccountAfterSuccessfulTest(ctx, id)
		if v == nil {
			return nil, err
		}
		return &account.SuccessfulTestRecovery{ClearedError: v.ClearedError, ClearedRateLimit: v.ClearedRateLimit}, err
	}
}
