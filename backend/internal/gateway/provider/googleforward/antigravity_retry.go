package googleforward

import (
	"net/http"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
)

// 投影当前尝试，复用账号探测和转发共享的重试实例。
func (s *Antigravity) antigravityRetryAdapter(p antigravityRetryLoopParams) (*antigravity.RetryAdapter, antigravity.RetryInput) {
	value := gatewayprovider.ExecutionRecord(p.account)
	factory := s.Retry
	return factory.Bind(accountprovider.AntigravityRetryRequest{
		Context:             p.ctx,
		Account:             value,
		ModelStore:          p.accountRepo,
		Prefix:              p.prefix,
		ProxyURL:            p.proxyURL,
		AccessToken:         p.accessToken,
		Action:              p.action,
		Body:                p.body,
		RequestedModel:      p.requestedModel,
		Thinking:            requeststate.HealthThinking(p.ctx),
		SingleAccount:       isSingleAccountRetry(p.ctx),
		Sticky:              p.isStickySession,
		UserAgent:           p.userAgent,
		PolicyModelFallback: requeststate.HealthModel(p.ctx, nil),

		Do: func(req *http.Request) (*http.Response, error) {
			// 原传输/测试端口可发布本次请求的新窗口；在下一次签名恢复前同步显式尝试视图。
			resp, err := p.httpUpstream.Do(req, p.proxyURL, p.account.Record.ID, p.account.Record.Concurrency)
			value.Extra = gatewayprovider.ExecutionRecord(p.account).Extra
			return resp, err
		},

		LogConfig: func() (bool, int) {
			if !s.Options.Configured {
				return false, 0
			}
			return s.Options.LogErrorBody, s.Options.LogErrorBodyMaxBytes
		},

		Changed: func(record *accountcore.Record) { p.account.Record.Extra = record.Extra },

		Observe: func(o antigravity.RetryObservation) {
			p.c.Observe(ops.OpsUpstreamErrorEvent{
				Platform:           p.account.Record.Platform,
				AccountID:          o.AccountID,
				AccountName:        o.AccountName,
				UpstreamStatusCode: o.UpstreamStatusCode,
				UpstreamRequestID:  o.UpstreamRequestID,
				UpstreamURL:        o.UpstreamURL,
				Kind:               o.Kind,
				Message:            o.Message,
				Detail:             o.Detail,
			})
		},

		SetError: func(status int, message, detail string) {
			p.c.SetError(status, message, detail)
		},

		HandleError: func(status int, header http.Header, body []byte) {
			p.handleError(p.ctx, p.prefix, p.account, status, header, body, p.requestedModel, p.groupID, p.sessionHash, p.isStickySession)
			value.Extra = p.account.Record.Extra
		},

		ClearSticky: func() { s.clearStickySession(p.ctx, p.groupID, p.sessionHash) },
	})
}
