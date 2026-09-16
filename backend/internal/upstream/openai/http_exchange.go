// 单次 HTTP 交换拥有首响应头预算和响应关闭后的 context 释放；不决定账号重试。
package openai

import (
	"context"
	"io"
	"net/http"
	"sync"
	"time"
)

type HTTPExchangeOptions struct {
	StartedAt          time.Time
	FirstOutputTimeout time.Duration
	RequestContext     func(context.Context) (context.Context, context.CancelFunc)
	Build              func(context.Context, []byte) (*http.Request, error)
	ApplyHeaders       func(http.Header)
	Do                 func(*http.Request) (*http.Response, error)
	Latency            func(time.Duration)
	HeaderTimeout      func() error
	TransportError     func(error) error
}

func ExchangeHTTP(ctx context.Context, body []byte, options HTTPExchangeOptions) (*http.Response, error) {
	upstreamCtx, release := options.RequestContext(ctx)
	var guard *openAIFirstOutputHeaderGuard
	if options.FirstOutputTimeout > 0 {
		upstreamCtx, guard = newOpenAIFirstOutputHeaderGuard(upstreamCtx, release, options.StartedAt.Add(options.FirstOutputTimeout))
	}
	request, err := options.Build(upstreamCtx, body)
	if guard == nil {
		release()
	}
	if err != nil {
		if guard != nil {
			guard.close()
		}
		return nil, err
	}
	options.ApplyHeaders(request.Header)
	started := time.Now()
	response, err := options.Do(request)
	options.Latency(time.Since(started))
	if guard != nil && guard.stopHeaderWait() {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		guard.close()
		return nil, options.HeaderTimeout()
	}
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		if guard != nil {
			guard.close()
		}
		return nil, options.TransportError(err)
	}
	if guard != nil {
		response.Body = &openAIRequestContextReadCloser{ReadCloser: response.Body, cleanup: guard.close}
	}
	return response, nil
}

type openAIFirstOutputHeaderGuard struct {
	cancel  context.CancelFunc
	release context.CancelFunc
	timer   *time.Timer
	fired   chan struct{}
	once    sync.Once
}

func newOpenAIFirstOutputHeaderGuard(
	ctx context.Context,
	release context.CancelFunc,
	deadline time.Time,
) (context.Context, *openAIFirstOutputHeaderGuard) {
	guardedCtx, cancel := context.WithCancel(ctx)
	guard := &openAIFirstOutputHeaderGuard{cancel: cancel, release: release, fired: make(chan struct{})}
	remaining := time.Until(deadline)
	if remaining <= 0 {
		remaining = time.Nanosecond
	}
	guard.timer = time.AfterFunc(remaining, func() {
		close(guard.fired)
		cancel()
	})
	return guardedCtx, guard
}

func (g *openAIFirstOutputHeaderGuard) stopHeaderWait() bool {
	if g.timer.Stop() {
		return false
	}
	<-g.fired
	return true
}

func (g *openAIFirstOutputHeaderGuard) close() {
	g.once.Do(func() {
		g.timer.Stop()
		g.cancel()
		g.release()
	})
}

type openAIRequestContextReadCloser struct {
	io.ReadCloser
	cleanup func()
	once    sync.Once
	err     error
}

func (r *openAIRequestContextReadCloser) Close() error {
	r.once.Do(func() {
		r.cleanup()
		r.err = r.ReadCloser.Close()
	})
	return r.err
}
