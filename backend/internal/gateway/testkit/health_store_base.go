//go:build unit

package testkit

import (
	"context"
	"errors"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

// HealthStoreBase 保留原健康夹具的缺失账号和空写入行为，其余能力未配置即不可调用。
type HealthStoreBase struct{ provider.ExecutionAccountStore }

func (*HealthStoreBase) GetByID(context.Context, int64) (*provider.ExecutionAccount, error) {
	return nil, errors.New("account not found")
}
func (*HealthStoreBase) SetError(context.Context, int64, string) error          { return nil }
func (*HealthStoreBase) SetRateLimited(context.Context, int64, time.Time) error { return nil }
func (*HealthStoreBase) SetModelRateLimit(context.Context, int64, string, time.Time, ...string) error {
	return nil
}
func (*HealthStoreBase) SetOverloaded(context.Context, int64, time.Time) error { return nil }
func (*HealthStoreBase) SetTempUnschedulable(context.Context, int64, time.Time, string) error {
	return nil
}
func (*HealthStoreBase) ClearTempUnschedulable(context.Context, int64) error            { return nil }
func (*HealthStoreBase) ClearRateLimit(context.Context, int64) error                    { return nil }
func (*HealthStoreBase) ClearError(context.Context, int64) error                        { return nil }
func (*HealthStoreBase) UpdateCredentials(context.Context, int64, map[string]any) error { return nil }
func (*HealthStoreBase) UpdateExtra(context.Context, int64, map[string]any) error       { return nil }
func (*HealthStoreBase) UpdateSessionWindow(context.Context, int64, *time.Time, *time.Time, string) error {
	return nil
}
