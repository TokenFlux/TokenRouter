package messageforward

import (
	"context"
	"errors"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/searchtools"
)

// 搜索资格由同一 Emulator 判断，执行结果保持 Messages 原有用量形状。
func (r *Runtime) shouldEmulate(ctx context.Context, target *provider.ExecutionAccount, group *int64, body []byte) bool {
	return r.dependencies.Search.ShouldEmulate(ctx, searchtools.PolicyInput{
		Body:     body,
		Mode:     provider.SearchAccountMode(&searchtools.AccountPolicy{ID: target.Record.ID, Platform: target.Record.Platform, Type: target.Record.Type, Extra: target.Record.Extra}),
		Platform: target.Record.Platform,
		GroupID:  group,
	})
}

func (r *Runtime) emulate(ctx context.Context, output HTTPBoundary, target *provider.ExecutionAccount, parsed *requeststate.ParsedRequest) (*forward.Result, error) {
	proxyURL := ""
	if target.Record.ProxyID != nil && target.Record.Proxy != nil {
		proxyURL = target.Record.Proxy.URL()
	}
	result, err := r.dependencies.Search.Execute(ctx, searchtools.Request{
		Body: parsed.Body.Bytes(), Model: parsed.Model, Stream: parsed.Stream,
		AccountID: target.Record.ID, AccountName: target.Record.Name,
		ProxyURL: proxyURL, OnAccepted: parsed.OnUpstreamAccepted,
	}, output.SearchOutput())
	if err != nil {
		var proxy *searchtools.ProxyFailure
		if errors.As(err, &proxy) {
			return nil, &forward.UpstreamFailoverError{StatusCode: http.StatusBadGateway, ResponseBody: []byte(err.Error())}
		}
		return nil, err
	}
	return &forward.Result{Model: result.Model, Duration: result.Duration, Usage: result.Usage}, nil
}
