package messageforward

import (
	"context"
	"net/http"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayadapter "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
)

// exchange.go configures Anthropic exchanges and signature-recovery policy for
// ordinary and passthrough requests.
// Anthropic exchanges rebuild requests with the current attempt state and
// selected provider. Signature and budget recovery are implemented by the
// upstream exchange, while these callbacks supply model rules, bounded retry
// settings, transport routing, and request diagnostics.

// exchangeOptions binds one provider exchange to Messages builders, transport,
// diagnostics, and retry settings. Callback readers borrow target and attempt
// state during execution; replace receives request bodies selected by recovery.
func (r *Runtime) exchangeOptions(output HTTPBoundary, state *AttemptState, target *gatewayadapter.ExecutionProvider, token, tokenType, model string, stream, mimic bool, proxy string, profile *tlsfingerprint.Profile, replace func([]byte) error) anthropic.ExchangeOptions {
	options := anthropic.ExchangeOptions{
		ProviderID: target.Record.ID, ProviderName: target.Record.Name, Platform: target.Record.Platform,
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
		if state != nil && !state.PrepareRecorded && !state.PrepareStartedAt.IsZero() && request != nil {
			state.PrepareRecorded = true
			timing.ObserveProviderPrepare(request.Context(), time.Since(state.PrepareStartedAt))
		}
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
	options.FilterThinking = func(body []byte) []byte { return gatewayadapter.FilterThinkingBlocksForRetry(body, model) }
	options.FilterTools = func(body []byte) []byte { return gatewayadapter.FilterSignatureSensitiveBlocksForRetry(body, model) }
	options.Observe = func(notice anthropic.ExchangeNotice) {
		output.Observe(forward.Notice{
			Platform: notice.Platform, ProviderID: notice.ProviderID, ProviderName: notice.ProviderName,
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

// passthroughExchange replaces ordinary construction with API key passthrough
// building and retains shared exchange settings. Parsed inputs synchronize body
// replacement into Parsed and input.Body, which stay mutable through the
// exchange.
func (r *Runtime) passthroughExchange(output HTTPBoundary, state *AttemptState, target *gatewayadapter.ExecutionProvider, token, proxy string, input *forward.APIKeyInput) anthropic.ExchangeOptions {
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
		if state != nil && !state.PrepareRecorded && !state.PrepareStartedAt.IsZero() && request != nil {
			state.PrepareRecorded = true
			timing.ObserveProviderPrepare(request.Context(), time.Since(state.PrepareStartedAt))
		}
		return r.dependencies.Transport.DoWithTLS(request, proxy, target.Record.ID, target.Record.Concurrency, r.requestTLS(target))
	}
	options.TransportError = func(ctx context.Context, err error, address string) error {
		return r.transportError(ctx, output, target, err, forward.Notice{UpstreamURL: logredact.SafeUpstreamURL(address), Passthrough: true})
	}
	options.Observe = func(notice anthropic.ExchangeNotice) {
		output.Observe(forward.Notice{
			Platform: notice.Platform, ProviderID: notice.ProviderID, ProviderName: notice.ProviderName,
			UpstreamStatusCode: notice.UpstreamStatusCode, UpstreamRequestID: notice.UpstreamRequestID,
			UpstreamURL: notice.UpstreamURL, Kind: notice.Kind, Message: notice.Message, Detail: notice.Detail, Passthrough: notice.Passthrough,
		})
	}
	return options
}

// shouldRectify checks model eligibility and current switches for initial
// signature recovery. API key targets require both rectifier switches and may
// use configured patterns; other targets use the standard signature switch.
func (r *Runtime) shouldRectify(ctx context.Context, target *gatewayadapter.ExecutionProvider, body []byte, model string) bool {
	if !modelidentity.ShouldRectifyThinkingSignatureError(model) {
		return false
	}
	if target.Record.Type == capability.ProviderTypeAPIKey {
		settings, err := r.dependencies.Settings.GetRectifierSettings(ctx)
		// Unavailable settings leave API key recovery disabled here,
		// preventing a custom pattern from initiating recovery without
		// both enabling switches for this provider.
		if err != nil || !settings.Enabled || !settings.APIKeySignatureEnabled {
			return false
		}
		return thinkingSignatureError(body) || anthropic.MatchSignaturePatterns(body, settings.APIKeySignaturePatterns)
	}
	return thinkingSignatureError(body) && r.dependencies.Settings.IsSignatureRectifierEnabled(ctx)
}

// signaturePattern recognizes the standard signature error or API key patterns
// during later recovery. Failed pattern settings reads return false; other
// credential kinds rely on the standard matcher result.
func (r *Runtime) signaturePattern(ctx context.Context, target *gatewayadapter.ExecutionProvider, body []byte) bool {
	if thinkingSignatureError(body) {
		return true
	}
	if target.Record.Type == capability.ProviderTypeAPIKey {
		settings, err := r.dependencies.Settings.GetRectifierSettings(ctx)
		// Unavailable later pattern settings return no custom match,
		// leaving the exchange recovery decision dependent on its
		// already evaluated standard signature checks.
		if err != nil {
			return false
		}
		return anthropic.MatchSignaturePatterns(body, settings.APIKeySignaturePatterns)
	}
	return false
}

// thinkingSignatureError recognizes Anthropic thinking signature messages and
// logs matcher diagnostics.
func thinkingSignatureError(body []byte) bool {
	matched, diagnostic := anthropic.IsThinkingBlockSignatureError(upstream.ExtractErrorMessage(body))
	if diagnostic != "" {
		logging.LegacyPrintf("service.gateway", "%s", diagnostic)
	}
	return matched
}
