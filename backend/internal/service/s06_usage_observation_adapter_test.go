package service

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/account"
	"reflect"
	"time"
)

func (r *accountUsageCodexProbeRepo) UpdateUsageExtraIfUnchanged(ctx context.Context, v account.UsageObservationVersion, updates map[string]any) (bool, error) {
	return true, r.UpdateExtra(ctx, v.ID, updates)
}
func (r *accountUsageCodexProbeRepo) SetUsageRateLimitIfUnchanged(ctx context.Context, v account.UsageObservationVersion, reset time.Time) (bool, error) {
	return true, r.SetRateLimited(ctx, v.ID, reset)
}
func (r *accountUsageCodexProbeRepo) ClearUsageRateLimitIfUnchanged(ctx context.Context, v account.UsageObservationVersion) (bool, error) {
	return true, r.ClearRateLimit(ctx, v.ID)
}

// 管理员交错夹具拒绝旧身份，真实同连接和 JSONB 比较由 PostgreSQL 测试验证。
func (r *qoderObservationIdentityRepo) matches(v account.UsageObservationVersion) bool {
	c := r.current
	return c.ID == v.ID && c.Platform == v.Platform && c.Type == v.Type && c.Status == v.Status && reflect.DeepEqual(c.Credentials, v.Credentials) && reflect.DeepEqual(c.ProxyID, v.ProxyID)
}
func (r *qoderObservationIdentityRepo) UpdateUsageExtraIfUnchanged(ctx context.Context, v account.UsageObservationVersion, updates map[string]any) (bool, error) {
	if !r.matches(v) {
		return false, nil
	}
	return true, r.UpdateExtra(ctx, v.ID, updates)
}
func (r *qoderObservationIdentityRepo) SetUsageRateLimitIfUnchanged(ctx context.Context, v account.UsageObservationVersion, reset time.Time) (bool, error) {
	if !r.matches(v) {
		return false, nil
	}
	return true, r.SetRateLimited(ctx, v.ID, reset)
}
func (r *qoderObservationIdentityRepo) ClearUsageRateLimitIfUnchanged(ctx context.Context, v account.UsageObservationVersion) (bool, error) {
	if !r.matches(v) {
		return false, nil
	}
	return true, r.ClearRateLimit(ctx, v.ID)
}

// 影子报文测试验证写入仍定位影子行，供应商调用已由原断言覆盖。
func (r *sparkShadowUsageTestRepo) UpdateUsageExtraIfUnchanged(ctx context.Context, v account.UsageObservationVersion, updates map[string]any) (bool, error) {
	current := r.accounts[v.ID]
	if current == nil {
		return false, nil
	}
	actual := account.ObserveUsageVersion(AccountRecordView(current))
	if !reflect.DeepEqual(actual, v) {
		return false, nil
	}
	return true, r.UpdateExtra(ctx, v.ID, updates)
}
