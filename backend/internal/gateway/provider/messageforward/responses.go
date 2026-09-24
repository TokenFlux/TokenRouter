package messageforward

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
)

// streamOptions 保留响应窗口观察和逐事件缓存规则读取，不复制流解析状态。
func (r *Runtime) streamOptions(output HTTPBoundary, state *AttemptState, target *provider.ExecutionAccount) anthropic.StreamOptions {
	options := anthropic.StreamOptions{
		AccountID: target.Record.ID,
		ToolNames: state.ToolNames,
		UpdateWindow: func(ctx context.Context, headers http.Header) {
			provider.ObserveExecutionSessionWindow(ctx, r.dependencies.Health, target, headers)
		},
		OverrideCache: func(ctx context.Context) (string, bool) {
			return r.cacheUsageOverride(ctx, target)
		},
		Failover: func(body []byte) error {
			return &forward.UpstreamFailoverError{StatusCode: 502, ResponseBody: body, RetryableOnSameAccount: true}
		},
	}
	if output.RequestPresent() {
		options.UserAgent = output.RequestHeaders().Get("User-Agent")
	}
	options.MaxLineSize = r.options.MaxLineSize
	if r.options.StreamInterval > 0 {
		options.Interval = r.options.StreamInterval
	}
	if r.options.StreamKeepalive > 0 {
		options.Keepalive = r.options.StreamKeepalive
	}
	if output.HasHeaderFilter() {
		options.WriteHeaders = func(dst, src http.Header) { output.WriteHeaders(dst, src, false) }
	}
	if r.dependencies.Health != nil {
		options.OnTimeout = func(ctx context.Context, model string) {
			r.dependencies.Health.Core.HandleStreamTimeout(ctx, provider.ExecutionRecord(target), model)
		}
	}
	return options
}

func (r *Runtime) responseOptions(ctx context.Context, output HTTPBoundary, state *AttemptState, target *provider.ExecutionAccount, model string, passthrough bool) anthropic.ResponseOptions {
	options := anthropic.ResponseOptions{
		StreamOptions: r.streamOptions(output, state, target),
		ReadBody: func(reader io.Reader) ([]byte, error) {
			return output.ReadResponseBody(reader, r.options.ResponseReadLimit, MessagesBody)
		},
		PreserveContentType: r.options.PreserveContentType,
		ForceCacheBilling:   passthrough && requeststate.IsForceCacheBilling(ctx),
	}
	options.InvalidJSON = func(ctx context.Context, response *http.Response, body []byte, err error) error {
		var models []string
		if !passthrough {
			models = []string{model}
		}
		return provider.NonJSONUpstreamFailure(ctx, r.dependencies.Health, response, target, body, err, models...)
	}
	options.WriteHeaders = func(dst, src http.Header) { output.WriteHeaders(dst, src, passthrough) }
	if passthrough && r.dependencies.Health == nil {
		options.UpdateWindow = nil
	}
	return options
}

// readErrorBody 保留错误诊断上限与读取失败，不替代成功响应的独立大小限制。
func (r *Runtime) readErrorBody(response *http.Response) ([]byte, error) {
	if response == nil || response.Body == nil {
		return nil, nil
	}
	limit := int64(512 << 10)
	if r.options.LogErrorBody && r.options.LogErrorBodyMaxBytes > int(limit) {
		limit = int64(r.options.LogErrorBodyMaxBytes)
	}
	return io.ReadAll(io.LimitReader(response.Body, limit))
}

func detachedStreamContext(ctx context.Context, stream bool) (context.Context, context.CancelFunc) {
	if ctx == nil {
		return context.Background(), func() {}
	}
	if !stream {
		return ctx, func() {}
	}
	return context.WithoutCancel(ctx), func() {}
}

// 固定时长仅用于平台已有的重试预算，不引入额外请求循环。
const (
	maxRetryAttempts = 5
	maxRetryElapsed  = 10 * time.Second
)
