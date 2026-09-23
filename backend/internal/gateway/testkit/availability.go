package testkit

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

// AvailabilityStore 只把原测试仓储结果投影给实际诊断读取端口，查询仍调用同一替身。
type AvailabilityStore struct {
	Source provider.ExecutionAccountStore
}

func (s AvailabilityStore) ListModelAvailabilityCandidates(ctx context.Context, group *int64, platforms []string, grouped bool) ([]account.Record, error) {
	values, err := s.Source.ListModelAvailabilityCandidates(ctx, group, platforms, grouped)
	if err != nil {
		return nil, err
	}
	if values == nil {
		return nil, nil
	}
	out := make([]account.Record, len(values))
	for i := range values {
		out[i] = *provider.ExecutionRecord(&values[i])
	}
	return out, nil
}
