// 原网关输入投影为平台重试参数；账号健康、粘性与 Ops 动作仍按原时点调用。
package service

import (
	"net/http"
	"os"

	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/ops"

	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
)

func resolveAntigravityForwardBaseURL(account *Account) string {
	return antigravity.ResolveAntigravityForwardBaseURL(os.Getenv(antigravityForwardBaseURLEnv), accountHasAntigravityPaidTier(account))
}

// 剩余旧执行入口只投影当前尝试；重试绑定与平台状态操作由原生拥有者完成。
func (s *AntigravityGatewayService) antigravityRetryAdapter(p antigravityRetryLoopParams) (*antigravity.RetryAdapter, antigravity.RetryInput) {
	value := AccountRecordView(p.account)
	factory := s.antigravityRetryBinding()
	return factory.Bind(accountprovider.AntigravityRetryRequest{Context: p.ctx, Account: value, ModelStore: p.accountRepo, Prefix: p.prefix, ProxyURL: p.proxyURL, AccessToken: p.accessToken, Action: p.action, Body: p.body, RequestedModel: p.requestedModel, Thinking: modelHealthThinking(p.ctx), SingleAccount: isSingleAccountRetry(p.ctx), Sticky: p.isStickySession, UserAgent: p.userAgent, PolicyModelFallback: tempUnschedulableModel(p.ctx, nil),
		Do: func(req *http.Request) (*http.Response, error) {
			// 原传输/测试端口可发布本次请求的新窗口；在下一次签名恢复前同步显式尝试视图。
			resp, err := p.httpUpstream.Do(req, p.proxyURL, p.account.ID, p.account.Concurrency)
			value.Extra = AccountRecordView(p.account).Extra
			return resp, err
		},
		LogConfig: func() (bool, int) {
			if p.settingService == nil || p.settingService.Antigravity == nil {
				return false, 0
			}
			return p.settingService.Antigravity.LogUpstreamErrorBody, p.settingService.Antigravity.LogUpstreamErrorBodyMaxBytes
		},
		Changed: func(record *accountcore.Record) { p.account.Extra = record.Extra },
		Observe: func(o antigravity.RetryObservation) {
			gatewayhttp.AppendOpsUpstreamError(p.c, ops.OpsUpstreamErrorEvent{Platform: p.account.Platform, AccountID: o.AccountID, AccountName: o.AccountName, UpstreamStatusCode: o.UpstreamStatusCode, UpstreamRequestID: o.UpstreamRequestID, UpstreamURL: o.UpstreamURL, Kind: o.Kind, Message: o.Message, Detail: o.Detail})
		},
		SetError: func(status int, message, detail string) {
			gatewayhttp.SetOpsUpstreamError(p.c, status, message, detail)
		},
		HandleError: func(status int, header http.Header, body []byte) {
			p.handleError(p.ctx, p.prefix, p.account, status, header, body, p.requestedModel, p.groupID, p.sessionHash, p.isStickySession)
			value.Extra = p.account.Extra
		},
		ClearSticky: func() { s.clearStickySession(p.ctx, p.groupID, p.sessionHash) },
	})
}

// BindAntigravityRetry 由 app 绑定唯一生产平台重试装配，不启动工作。
func (s *AntigravityGatewayService) BindAntigravityRetry(value *accountprovider.AntigravityRetry) {
	s.nativeRetry = value
}
func (s *AntigravityGatewayService) antigravityRetryBinding() *accountprovider.AntigravityRetry {
	if s.nativeRetry != nil {
		return s.nativeRetry
	}
	var policy *accountcore.HealthService
	if s.rateLimitService != nil {
		policy = s.rateLimitService.HealthCore()
	}
	return &accountprovider.AntigravityRetry{Health: s.antigravityHealth(), Policy: policy, BaseURL: func(v *accountcore.Record) string { return resolveAntigravityForwardBaseURL(AccountFromRecord(v)) }, BodyLimit: s.upstreamErrorBodyReadLimit, TruncateString: logredact.TruncateUTF8, SafeURL: logredact.SafeUpstreamURL}
}
