package scheduler

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
)

// AccountRPMInput 是账号配置的显式调度投影，不读取完整账号或凭据。
type AccountRPMInput struct {
	ID           int64
	Enabled      bool
	Base, Buffer int
	Strategy     string
}
type rpmPrefetchKey struct{}

func PrefetchedRPM(ctx context.Context, id int64) (int, bool) {
	if v, ok := ctx.Value(rpmPrefetchKey{}).(map[int64]int); ok {
		count, found := v[id]
		return count, found
	}
	return 0, false
}
func PrefetchRPM(ctx context.Context, cache RPMCache, ids []int64) context.Context {
	if cache == nil || len(ids) == 0 {
		return ctx
	}
	counts, err := cache.GetRPMBatch(ctx, ids)
	if err != nil {
		return ctx
	}
	return context.WithValue(ctx, rpmPrefetchKey{}, counts)
}

// AllowAccountRPM 保留软计数、失败放行与粘性三区规则。
func AllowAccountRPM(ctx context.Context, cache RPMCache, input AccountRPMInput, sticky bool) bool {
	if !input.Enabled || input.Base <= 0 {
		return true
	}
	current, found := PrefetchedRPM(ctx, input.ID)
	if !found && cache != nil {
		if count, err := cache.GetRPM(ctx, input.ID); err == nil {
			current = count
		}
	}
	switch policy.CheckRPM(current, input.Base, input.Buffer, input.Strategy) {
	case policy.RPMAllowed:
		return true
	case policy.RPMStickyOnly:
		return sticky
	case policy.RPMBlocked:
		return false
	}
	return true
}
func IncrementAccountRPM(ctx context.Context, cache RPMCache, id int64) error {
	if cache == nil {
		return nil
	}
	_, err := cache.IncrementRPM(ctx, id)
	return err
}
