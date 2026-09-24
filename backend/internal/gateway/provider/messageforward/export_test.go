package messageforward

import (
	"context"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
)

// 这些入口只参与 go test 构建，让外部契约测试接入真实 HTTP Adapter。
// 测试仍调用本包私有实现，生产 API 不增加白盒测试入口。
func ErrorForTest(runtime *Runtime, ctx context.Context, output HTTPBoundary, target *provider.ExecutionAccount, response *http.Response, retry bool, models ...string) (*forward.Result, error) {
	return runtime.handleError(ctx, output, &AttemptState{}, target, response, retry, models...)
}

func ResponseOptionsForTest(runtime *Runtime, ctx context.Context, output HTTPBoundary, target *provider.ExecutionAccount, model string, passthrough bool) anthropic.ResponseOptions {
	return runtime.responseOptions(ctx, output, &AttemptState{}, target, model, passthrough)
}
