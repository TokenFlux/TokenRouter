// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"

	routing "github.com/TokenFlux/TokenRouter/internal/routing"
)

// legacyAvailabilityReader 只读取原持久候选并把平台判断封装为本轮窄端口。
func legacyAvailabilityReader(repo AccountRepository, supports func(context.Context, *Account, string) bool) routing.AvailabilityReader {
	if repo == nil {
		return nil
	}
	return func(ctx context.Context, id *int64, platforms []string, includeGrouped bool) ([]routing.AvailabilityAccount, error) {
		values, err := repo.ListModelAvailabilityCandidates(ctx, id, platforms, includeGrouped)
		if err != nil {
			return nil, err
		}
		out := make([]routing.AvailabilityAccount, len(values))
		for i := range values {
			v := &values[i]
			out[i] = routing.AvailabilityAccount{Platform: v.Platform, MixedScheduling: v.IsMixedSchedulingEnabled(), Supports: func(ctx context.Context, model string) bool { return supports(ctx, v, model) }}
		}
		return out, nil
	}
}
