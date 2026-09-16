// 原网关输入投影为平台重试参数；账号健康、粘性与 Ops 动作仍按原时点调用。
package service

import (
	"net/http"
	"os"
	"time"

	native "github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
)

func (s *AntigravityGatewayService) antigravityRetryAdapter(p antigravityRetryLoopParams) (*native.RetryAdapter, native.RetryInput) {
	input := native.RetryInput{Ctx: p.ctx, Prefix: p.prefix, AccountID: p.account.ID, AccountName: p.account.Name, Native: p.account.Platform == PlatformAntigravity, OveragesEnabled: p.account.IsOveragesEnabled(), SingleAccount: isSingleAccountRetry(p.ctx), AccessToken: p.accessToken, Action: p.action, Body: p.body, RequestedModel: p.requestedModel, IsStickySession: p.isStickySession, CreditsExhausted: p.account.isCreditsExhausted, ModelLimited: p.account.isModelRateLimitedWithContext, ModelRemaining: p.account.GetRateLimitRemainingTimeWithContext}
	opts := native.RetryOptions{BaseURL: func() string { return resolveAntigravityForwardBaseURL(p.account) }, Do: func(req *http.Request) (*http.Response, error) {
		return p.httpUpstream.Do(req, p.proxyURL, p.account.ID, p.account.Concurrency)
	}, ApplyHeaders: applyAccountTestUserAgent, TruncateString: truncateString, TruncateForLog: truncateForLog, SafeURL: safeUpstreamURL, ReadErrorBody: s.readUpstreamErrorBody,
		Observe: func(o native.RetryObservation) {
			appendOpsUpstreamError(p.c, OpsUpstreamErrorEvent{Platform: p.account.Platform, AccountID: o.AccountID, AccountName: o.AccountName, UpstreamStatusCode: o.UpstreamStatusCode, UpstreamRequestID: o.UpstreamRequestID, UpstreamURL: o.UpstreamURL, Kind: o.Kind, Message: o.Message, Detail: o.Detail})
		}, SetError: func(status int, message, detail string) { setOpsUpstreamError(p.c, status, message, detail) },
		ApplyErrorPolicy: func(status int, header http.Header, body []byte) (bool, int, error) {
			return s.applyErrorPolicy(p, status, header, body)
		}, HandleError: func(status int, header http.Header, body []byte) {
			p.handleError(p.ctx, p.prefix, p.account, status, header, body, p.requestedModel, p.groupID, p.sessionHash, p.isStickySession)
		},
		SetModelLimits: func(model string, status int, until time.Time, force bool) bool {
			return s.setAntigravityModelRateLimits(p.ctx, p.accountRepo, p.account, model, p.prefix, status, until, force)
		}, ClearSticky: func() { s.clearStickySession(p.ctx, p.groupID, p.sessionHash) },
		CreditsModel: func(model string) string {
			return resolveCreditsOveragesModelKey(p.ctx, p.account, model, p.requestedModel)
		}, ClearCredits: func() { s.clearCreditsExhausted(p.ctx, p.account) }, CreditsFailure: func(model string, resp *http.Response, err error) {
			s.handleCreditsRetryFailure(p.ctx, p.prefix, model, p.account, resp, err)
		}, Internal500Exhausted: func() { s.handleInternal500RetryExhausted(p.ctx, p.prefix, p.account) }, ResetInternal500: func() { s.resetInternal500Counter(p.ctx, p.prefix, p.account.ID) }}
	if p.settingService != nil && p.settingService.cfg != nil {
		opts.LogBody = p.settingService.cfg.Gateway.LogUpstreamErrorBody
		opts.LogMaxBytes = p.settingService.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
	}
	return &native.RetryAdapter{Options: opts}, input
}
func resolveAntigravityForwardBaseURL(account *Account) string {
	return native.ResolveAntigravityForwardBaseURL(os.Getenv(antigravityForwardBaseURLEnv), accountHasAntigravityPaidTier(account))
}

type antigravitySmartRetryInfo = native.AntigravitySmartRetryInfo

func parseAntigravitySmartRetryInfo(body []byte) *antigravitySmartRetryInfo {
	return native.ParseAntigravitySmartRetryInfo(body)
}
func shouldTriggerAntigravitySmartRetry(account *Account, respBody []byte) (shouldRetry bool, shouldRateLimitModel bool, waitDuration time.Duration, modelName string, isModelCapacityExhausted bool) {
	return native.ShouldTriggerAntigravitySmartRetry(account.Platform == PlatformAntigravity, respBody)
}

func shouldMarkCreditsExhausted(resp *http.Response, respBody []byte, reqErr error) bool {
	return native.ShouldMarkCreditsExhausted(resp, respBody, reqErr)
}

const antigravityRateLimitThreshold = native.AntigravityRateLimitThreshold

const antigravityDefaultRateLimitDuration = native.AntigravityDefaultRateLimitDuration

type AntigravityAccountSwitchError = native.AntigravityAccountSwitchError

func IsAntigravityAccountSwitchError(err error) (*AntigravityAccountSwitchError, bool) {
	return native.IsAntigravityAccountSwitchError(err)
}
