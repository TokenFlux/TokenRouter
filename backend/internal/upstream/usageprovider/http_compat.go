// 过渡期只暴露固定技术 helper，算法在共享读取器中唯一维护。
package usageprovider

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/upstream/internal/usageclient"
)

func UpstreamUsageEndpoint(base, path string, build func(string, string) string) (string, error) {
	return usageclient.UpstreamUsageEndpoint(base, path, build)
}
func UpstreamUsageStatusEndpoint(base string) (string, error) {
	return usageclient.UpstreamUsageStatusEndpoint(base)
}
func UpstreamUsageTokenEndpoint(base string) (string, error) {
	return usageclient.UpstreamUsageTokenEndpoint(base)
}
func UpstreamUsageWalletEndpoint(base string) (string, error) {
	return usageclient.UpstreamUsageWalletEndpoint(base)
}
func UpstreamUsageUserSelfEndpoint(base string) (string, error) {
	return usageclient.UpstreamUsageUserSelfEndpoint(base)
}
func UpstreamUsageRootEndpoint(base, path string) (string, error) {
	return usageclient.UpstreamUsageRootEndpoint(base, path)
}
func UpstreamUsageHTTPError(status int, unsupported bool) error {
	return usageclient.UpstreamUsageHTTPError(status, unsupported)
}
func UpstreamUsageOperationError(ctx context.Context, err error) error {
	return usageclient.UpstreamUsageOperationError(ctx, err)
}

func CnParseF64(raw any) (float64, bool) { return usageclient.CnParseF64(raw) }

func CnNormalizeResetTime(raw any) string { return usageclient.CnNormalizeResetTime(raw) }

func CnMillisToRFC3339(n int64) string { return usageclient.CnMillisToRFC3339(n) }
