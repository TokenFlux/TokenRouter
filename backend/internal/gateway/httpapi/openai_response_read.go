package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	"github.com/gin-gonic/gin"
)

// ReadStreamObservation 保留结果与错误并存；完成资格仍由入口用例决定。
func (p *OpenAIResponseOutput) ReadStreamObservation(ctx context.Context, resp *http.Response, c *gin.Context, target *provider.ExecutionAccount, started time.Time, original, mapped, effort string) (*openai.StreamingResult, error) {
	return openai.ReadStreamingResponse(ctx, resp, upstream.NewOutputContext(ResponseSink{Writer: c.Writer}), p.StreamOptions(ctx, c, target, effort), started, original, mapped, effort)
}

// Stream 保留旧消费入口在仅有观测而执行失败时返回空结果的约定。
func (p *OpenAIResponseOutput) Stream(ctx context.Context, resp *http.Response, c *gin.Context, target *provider.ExecutionAccount, started time.Time, original, mapped, effort string) (*openai.StreamingResult, error) {
	result, err := p.ReadStreamObservation(ctx, resp, c, target, started, original, mapped, effort)
	if err != nil && result != nil && result.ObservedOnly {
		return nil, err
	}
	return result, err
}
func (p *OpenAIResponseOutput) NonStream(ctx context.Context, resp *http.Response, c *gin.Context, target *provider.ExecutionAccount, original, mapped string) (*openai.NonStreamingResult, error) {
	return openai.ReadNonStreamingResponse(ctx, resp, upstream.NewOutputContext(ResponseSink{Writer: c.Writer}), p.NonStreamOptions(ctx, c, target), original, mapped)
}
func (p *OpenAIResponseOutput) SSEAsJSON(ctx context.Context, resp *http.Response, c *gin.Context, target *provider.ExecutionAccount, body []byte, original, mapped string) (*openai.NonStreamingResult, error) {
	return openai.ReadSSEAsJSON(ctx, resp, upstream.NewOutputContext(ResponseSink{Writer: c.Writer}), p.NonStreamOptions(ctx, c, target), body, original, mapped)
}
func (p *OpenAIResponseOutput) PassthroughStream(ctx context.Context, resp *http.Response, c *gin.Context, target *provider.ExecutionAccount, started time.Time, original, mapped string) (*openai.StreamingResult, error) {
	return openai.ReadPassthroughStreaming(ctx, resp, upstream.NewOutputContext(ResponseSink{Writer: c.Writer}), p.PassthroughOptions(ctx, c, target), started, original, mapped)
}
func (p *OpenAIResponseOutput) PassthroughNonStream(ctx context.Context, resp *http.Response, c *gin.Context, target *provider.ExecutionAccount, original, mapped string) (*openai.NonStreamingResult, error) {
	return openai.ReadPassthroughNonStreaming(ctx, resp, ResponseSink{Writer: c.Writer}, p.PassthroughOptions(ctx, c, target), original, mapped)
}
func (p *OpenAIResponseOutput) PassthroughSSEAsJSON(resp *http.Response, c *gin.Context, target *provider.ExecutionAccount, body []byte, original, mapped string) (*openai.NonStreamingResult, error) {
	return openai.ReadPassthroughSSEAsJSON(resp, ResponseSink{Writer: c.Writer}, p.PassthroughOptions(c.Request.Context(), c, target), body, original, mapped)
}
