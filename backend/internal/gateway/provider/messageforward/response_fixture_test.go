package messageforward_test

import (
	"context"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/messageforward"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/gin-gonic/gin"
)

// 响应契约组合真实的 HTTP 写入器、原生参数投影和平台读取器。
func newResponseRuntimeFixture(options *messageforward.Options, deps messageforward.Dependencies, _ *egress.CompiledHeaderFilter) *messageforward.Runtime {
	value := messageforward.Options{ResponseReadLimit: 128 * 1024 * 1024}
	if options != nil {
		value = *options
	}
	return messageforward.NewRuntime(deps, value)
}
func nonStreamResponseFixture(runtime *messageforward.Runtime, ctx context.Context, response *http.Response, c *gin.Context, target *provider.ExecutionAccount, original, mapped string) (*upstream.TokenUsage, error) {
	boundary := gatewayhttp.NewMessageForwardBoundary(c, nil)
	options := messageforward.ResponseOptionsForTest(runtime, ctx, boundary, target, mapped, false)
	return anthropic.NonStreamResponse(ctx, response, upstream.NewOutputContext(boundary.Sink()), options, original, mapped)
}
func passthroughResponseFixture(runtime *messageforward.Runtime, ctx context.Context, response *http.Response, c *gin.Context, target *provider.ExecutionAccount) (*upstream.TokenUsage, error) {
	boundary := gatewayhttp.NewMessageForwardBoundary(c, nil)
	options := messageforward.ResponseOptionsForTest(runtime, ctx, boundary, target, "", true)
	return anthropic.NonStreamResponsePassthrough(ctx, response, upstream.NewOutputContext(boundary.Sink()), options)
}

// 每个用例先确定静态 keepalive 参数，再构造运行时。
func newStreamingRuntimeFixture(keepalive time.Duration) *messageforward.Runtime {
	return messageforward.NewRuntime(messageforward.Dependencies{Health: newPartialHealthFixture()}, messageforward.Options{Configured: true, MaxLineSize: defaultMaxLineSize, StreamKeepalive: keepalive})
}
func streamResponseFixture(runtime *messageforward.Runtime, ctx context.Context, response *http.Response, c *gin.Context, target *provider.ExecutionAccount, started time.Time, original, mapped string, mimic bool) (*anthropic.StreamResult, error) {
	boundary := gatewayhttp.NewMessageForwardBoundary(c, nil)
	options := messageforward.ResponseOptionsForTest(runtime, ctx, boundary, target, mapped, false)
	return anthropic.StreamResponse(ctx, response, upstream.NewOutputContext(boundary.Sink()), options.StreamOptions, started, original, mapped, mimic)
}

func passthroughStreamFixture(runtime *messageforward.Runtime, ctx context.Context, response *http.Response, c *gin.Context, target *provider.ExecutionAccount, started time.Time, model string) (*anthropic.StreamResult, error) {
	boundary := gatewayhttp.NewMessageForwardBoundary(c, nil)
	options := messageforward.ResponseOptionsForTest(runtime, ctx, boundary, target, model, true)
	return anthropic.StreamResponsePassthrough(ctx, response, upstream.NewOutputContext(boundary.Sink()), options.StreamOptions, started, model)
}
