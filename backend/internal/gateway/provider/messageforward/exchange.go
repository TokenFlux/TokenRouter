package messageforward

import (
	"context"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
)

// exchangeOptions 组合既有平台单账号交换，不拥有第二套重试或切号循环。
func (r *Runtime) exchangeOptions(output HTTPBoundary, state *AttemptState, target *provider.ExecutionAccount, token, tokenType, model string, stream, mimic bool, proxy string, profile *tlsfingerprint.Profile, replace func([]byte) error) anthropic.ExchangeOptions {
	options := anthropic.ExchangeOptions{
		AccountID: target.Record.ID, AccountName: target.Record.Name, Platform: target.Record.Platform,
		Stream: stream, MaxAttempts: maxRetryAttempts, MaxElapsed: maxRetryElapsed,
		BudgetTokens: anthropic.BudgetRectifyBudgetTokens, BudgetMaxTokens: anthropic.BudgetRectifyMaxTokens,
		Context: detachedStreamContext, ReadErrorBody: r.readErrorBody, Delay: forward.RetryDelay,
		SafeURL: logredact.SafeUpstreamURL, Sanitize: logredact.SanitizeUpstreamQueries,
		ErrorMessage: upstream.ExtractErrorMessage, IsBudgetError: anthropic.IsThinkingBudgetConstraintError,
		RectifyBudget: anthropic.RectifyThinkingBudget, ReplaceBody: replace,
	}
	options.Build = func(ctx context.Context, body []byte) (*http.Request, []byte, error) {
		return r.buildRequest(ctx, output, state, target, body, token, tokenType, model, stream, mimic)
	}
	options.Do = func(request *http.Request) (*http.Response, error) {
		return r.dependencies.Transport.DoWithTLS(request, proxy, target.Record.ID, target.Record.Concurrency, profile)
	}
	options.ShouldRectify = func(ctx context.Context, body []byte) bool {
		return r.shouldRectify(ctx, target, body, model)
	}
	options.IsSignatureError = func(ctx context.Context, body []byte) bool {
		return r.signaturePattern(ctx, target, body)
	}
	options.BudgetEnabled = func(ctx context.Context) bool { return r.dependencies.Settings.IsBudgetRectifierEnabled(ctx) }
	options.ShouldRetry = func(status int) bool { return forward.ShouldRetry(target.View().IsOAuth(), status) }
	options.FilterThinking = func(body []byte) []byte { return provider.FilterThinkingBlocksForRetry(body, model) }
	options.FilterTools = func(body []byte) []byte { return provider.FilterSignatureSensitiveBlocksForRetry(body, model) }
	options.Observe = func(notice anthropic.ExchangeNotice) {
		output.Observe(forward.Notice{
			Platform: notice.Platform, AccountID: notice.AccountID, AccountName: notice.AccountName,
			UpstreamStatusCode: notice.UpstreamStatusCode, UpstreamRequestID: notice.UpstreamRequestID,
			UpstreamURL: notice.UpstreamURL, Kind: notice.Kind, Message: notice.Message, Detail: notice.Detail,
		})
	}
	options.Detail = func(body []byte) string {
		if r.options.LogErrorBody {
			return logredact.TruncateUTF8(string(body), r.options.LogErrorBodyMaxBytes)
		}
		return ""
	}
	options.TransportError = func(ctx context.Context, err error, address string) error {
		return r.transportError(ctx, output, target, err, forward.Notice{UpstreamURL: logredact.SafeUpstreamURL(address)})
	}
	options.DebugHeaders = target.Record.Platform == capability.PlatformGemini && r.options.GeminiDebugHeaders
	return options
}

func (r *Runtime) passthroughExchange(output HTTPBoundary, state *AttemptState, target *provider.ExecutionAccount, token, proxy string, input *forward.APIKeyInput) anthropic.ExchangeOptions {
	options := r.exchangeOptions(output, state, target, token, "apikey", input.RequestModel, input.RequestStream, false, proxy, nil, func(body []byte) error {
		if input.Parsed != nil {
			if err := input.Parsed.ReplaceBody(body); err != nil {
				return err
			}
			input.Body = input.Parsed.Body.Bytes()
		}
		return nil
	})
	options.SynchronizeBody = input.Parsed != nil
	options.Build = func(ctx context.Context, body []byte) (*http.Request, []byte, error) {
		return r.buildPassthroughRequest(ctx, output, state, target, body, token)
	}
	options.Do = func(request *http.Request) (*http.Response, error) {
		return r.dependencies.Transport.DoWithTLS(request, proxy, target.Record.ID, target.Record.Concurrency, r.requestTLS(target))
	}
	options.TransportError = func(ctx context.Context, err error, address string) error {
		return r.transportError(ctx, output, target, err, forward.Notice{UpstreamURL: logredact.SafeUpstreamURL(address), Passthrough: true})
	}
	options.Observe = func(notice anthropic.ExchangeNotice) {
		output.Observe(forward.Notice{
			Platform: notice.Platform, AccountID: notice.AccountID, AccountName: notice.AccountName,
			UpstreamStatusCode: notice.UpstreamStatusCode, UpstreamRequestID: notice.UpstreamRequestID,
			UpstreamURL: notice.UpstreamURL, Kind: notice.Kind, Message: notice.Message, Detail: notice.Detail, Passthrough: notice.Passthrough,
		})
	}
	return options
}

// 签名匹配和开关读取保持原先的两阶段差异，已经进入重试后不再次裁决开关。
func (r *Runtime) shouldRectify(ctx context.Context, target *provider.ExecutionAccount, body []byte, model string) bool {
	if !modelidentity.ShouldRectifyThinkingSignatureError(model) {
		return false
	}
	if target.Record.Type == capability.AccountTypeAPIKey {
		settings, err := r.dependencies.Settings.GetRectifierSettings(ctx)
		if err != nil || !settings.Enabled || !settings.APIKeySignatureEnabled {
			return false
		}
		return thinkingSignatureError(body) || anthropic.MatchSignaturePatterns(body, settings.APIKeySignaturePatterns)
	}
	return thinkingSignatureError(body) && r.dependencies.Settings.IsSignatureRectifierEnabled(ctx)
}

func (r *Runtime) signaturePattern(ctx context.Context, target *provider.ExecutionAccount, body []byte) bool {
	if thinkingSignatureError(body) {
		return true
	}
	if target.Record.Type == capability.AccountTypeAPIKey {
		settings, err := r.dependencies.Settings.GetRectifierSettings(ctx)
		if err != nil {
			return false
		}
		return anthropic.MatchSignaturePatterns(body, settings.APIKeySignaturePatterns)
	}
	return false
}

func thinkingSignatureError(body []byte) bool {
	matched, diagnostic := anthropic.IsThinkingBlockSignatureError(upstream.ExtractErrorMessage(body))
	if diagnostic != "" {
		logging.LegacyPrintf("service.gateway", "%s", diagnostic)
	}
	return matched
}
