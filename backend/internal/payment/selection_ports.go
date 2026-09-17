// 实例查询仅暴露选择所需的独立值和批量统计，SQL 位于 Adapter。
package payment

import (
	"context"
	"time"
)

type InstanceSource interface {
	EnabledInstances(context.Context, string) ([]*ProviderInstance, error)
	Instance(context.Context, int64) (*ProviderInstance, error)
	DailyUsage(context.Context, []string, time.Time) (map[string]float64, error)
	PaidDailyAmount(context.Context, string, time.Time) (float64, error)
}
type SelectionObserver func(string, string, ...any)
type SelectionRuntime struct {
	Now     func() time.Time
	Observe SelectionObserver
}
