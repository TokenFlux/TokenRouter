// 本文件为阶段迁移兼容入口；剩余消费者和退出阶段见 refactor/S01-foundation.md。
package servertiming

import (
	context "context"
	http "net/http"

	foundation "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
)

// WithDependencyModule 兼容旧入口；仅转发到目标实现。
func WithDependencyModule(ctx context.Context, module string) context.Context {
	return foundation.WithDependencyModule(ctx, module)
}

// WrapRoundTripper 兼容旧入口；仅转发到目标实现。
func WrapRoundTripper(base http.RoundTripper) http.RoundTripper {
	return foundation.WrapRoundTripper(base)
}

// InstrumentClient 兼容旧入口；仅转发到目标实现。
func InstrumentClient(client *http.Client) *http.Client {
	return foundation.InstrumentClient(client)
}

// Do 兼容旧入口；仅转发到目标实现。
func Do(client *http.Client, req *http.Request) (*http.Response, error) {
	return foundation.Do(client, req)
}
