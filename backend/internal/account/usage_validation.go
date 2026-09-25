// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	"context"
	"errors"
	"reflect"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/upstream/usageview"
)

func SameUpstreamUsageIdentity(expected, current *Record, expectedConfig UpstreamUsageQueryConfig) bool {
	if expected == nil || current == nil || expected.ID != current.ID || current.Type != AccountTypeAPIKey ||
		expected.Platform != current.Platform || !reflect.DeepEqual(expected.Credentials, current.Credentials) ||
		!SameUpstreamUsageOptionalInt64(expected.ProxyID, current.ProxyID) || expected.Concurrency != current.Concurrency ||
		expected.Status != current.Status {
		return false
	}
	if expected.ProxyID != nil && !SameUpstreamUsageProxy(expected.Proxy, current.Proxy, *expected.ProxyID) {
		// 仓储可能暂时没有预加载代理详情；两边都缺失时交给客户端构建阶段
		// 返回请求错误，避免把可诊断的配置缺失误报成身份冲突。
		if expected.Proxy != nil || current.Proxy != nil {
			return false
		}
	}
	for _, key := range []string{"enable_tls_fingerprint", "tls_fingerprint_profile_id", "tls_fingerprint_router_id"} {
		if !reflect.DeepEqual(ExtraValue(expected.Extra, key), ExtraValue(current.Extra, key)) {
			return false
		}
	}
	currentConfig, err := EffectiveUpstreamUsageConfig(current)
	if err != nil || currentConfig != expectedConfig {
		return false
	}
	return true
}

func SameUpstreamUsageOptionalInt64(left, right *int64) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func SameUpstreamUsageProxy(expected, current *egress.Proxy, id int64) bool {
	if expected == nil || current == nil || expected.ID != id || current.ID != id {
		return false
	}
	return expected.Protocol == current.Protocol && expected.Host == current.Host && expected.Port == current.Port &&
		expected.Username == current.Username && expected.Password == current.Password && expected.Status == current.Status
}

func ExtraValue(extra map[string]any, key string) any {
	if extra == nil {
		return nil
	}
	return extra[key]
}

func ValidateNormalizedUsage(usage *UpstreamUsageInfo) error {
	return usageview.ValidateNormalizedUsage(usage)
}

func ValidateUsageAmount(amount *UpstreamUsageAmount) error {
	return usageview.ValidateUsageAmount(amount)
}

func ValidateUsageLimits(limits []UpstreamUsageLimit) error {
	return usageview.ValidateUsageLimits(limits)
}

func ValidNonNegativeNumber(value float64) bool { return usageview.ValidNonNegativeNumber(value) }

func ValidPositiveNumber(value float64) bool { return usageview.ValidPositiveNumber(value) }

func ValidFiniteNumber(value float64) bool { return usageview.ValidFiniteNumber(value) }

func UpstreamUsageContextError(ctx context.Context) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ErrUpstreamUsageTimeout
	}
	return ErrUpstreamUsageRequestFailed
}
