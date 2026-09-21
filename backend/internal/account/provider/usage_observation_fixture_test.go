package provider

import (
	"context"
	"reflect"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
)

// 原交错测试替身按当前行身份判断，拒绝管理员修改后的旧观测。
func (r *qoderObservationIdentityRepo) matches(value account.UsageObservationVersion) bool {
	current := r.current
	return current.ID == value.ID && current.Platform == value.Platform && current.Type == value.Type && current.Status == value.Status && reflect.DeepEqual(current.Credentials, value.Credentials) && reflect.DeepEqual(current.ProxyID, value.ProxyID)
}

func (r *qoderObservationIdentityRepo) UpdateUsageExtraIfUnchanged(ctx context.Context, value account.UsageObservationVersion, updates map[string]any) (bool, error) {
	if !r.matches(value) {
		return false, nil
	}
	return true, r.UpdateExtra(ctx, value.ID, updates)
}

func (r *qoderObservationIdentityRepo) SetUsageRateLimitIfUnchanged(ctx context.Context, value account.UsageObservationVersion, reset time.Time) (bool, error) {
	if !r.matches(value) {
		return false, nil
	}
	return true, r.SetRateLimited(ctx, value.ID, reset)
}

func (r *qoderObservationIdentityRepo) ClearUsageRateLimitIfUnchanged(ctx context.Context, value account.UsageObservationVersion) (bool, error) {
	if !r.matches(value) {
		return false, nil
	}
	return true, r.ClearRateLimit(ctx, value.ID)
}
