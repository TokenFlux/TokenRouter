package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
)

// openai_input_token_attempt.go adapts input-token provider selection and
// forwarding to the dedicated retry loop.
// Responses preflight passes selected targets into a count-specific retry loop.
// Each forward releases any selection resource before returning, and failures
// carry platform retry classification back to that loop; terminal client output
// uses OpenAI error rules and the last classified upstream failure.

// inputTokensAttemptBridge retains the current Responses count target and
// request routing.
type inputTokensAttemptBridge struct {
	ports OpenAITokenPorts // shared token operation bindings
	c     *gin.Context     // current counting HTTP context
	call  InputTokensCall  // authenticated preflight request inputs
	key   *apikey.APIKey   // copied effective API key
	// selection owns the current target and its optional release callback.
	// Forward retires that callback before the text loop waits or selects
	// again.
	selection InputTokensSelection
}

// InputTokensExecution binds HTTP preflight input to the count retry loop. It
// copies the effective key and retains request bytes and diagnostics. Callers
// provide funding-approved input; the returned adapter handles
// selected-resource cleanup.
func (p OpenAITokenPorts) InputTokensExecution(c *gin.Context, call InputTokensCall) textflow.InputTokensPorts {
	return &inputTokensAttemptBridge{ports: p, c: c, call: call, key: apikey.CopyAPIKey(call.Key)}
}

// Context returns the HTTP request context used for counting and retry
// cancellation.
func (p *inputTokensAttemptBridge) Context() context.Context { return p.c.Request.Context() }

// Select captures a usable target and exposes its provider and same-provider
// retry limit. It records routing latency even on errors. Failed or absent
// targets return unavailable; a successful target replaces the adapter's
// current selection.
func (p *inputTokensAttemptBridge) Select(excluded map[int64]struct{}) (textflow.Selection, bool, error) {
	start := time.Now()
	selected, err := p.ports.Execution.SelectInputTokens(p.Context(), p.key.GroupID, p.call.SessionHash, p.call.Model, p.call.RoutingModel, excluded, p.call.Platform)
	SetOpsLatencyMs(p.c, OpsRoutingLatencyMsKey, time.Since(start).Milliseconds())
	if err != nil {
		return textflow.Selection{}, false, err
	}
	if selected.Target == nil {
		return textflow.Selection{}, false, nil
	}
	p.selection = selected
	provider := selected.Target.Snapshot()
	SetOpsSelectedProvider(p.c, provider)
	return textflow.Selection{Provider: provider, RetryLimit: selected.Target.RetryLimit()}, true, nil
}

// SelectionFailed writes a final preflight error after target selection fails.
// A previous classified attempt takes precedence through Exhausted. Initial
// empty selection writes unavailable output; selection business errors and
// model diagnostics format the remaining cases.
func (p *inputTokensAttemptBridge) SelectionFailed(err error, last *textflow.AttemptFailure, _ bool) {
	if err != nil {
		p.call.Log.Warn("openai_responses_input_tokens.provider_select_failed", zap.Error(err))
	}
	// Retain the last classified upstream error when subsequent selection
	// fails, giving the client the exhausted attempt's response policy
	// instead of fresh capacity output.
	if last != nil {
		p.Exhausted(last)
		return
	}
	if err == nil {
		MarkOpsRoutingCapacityLimited(p.c)
		writeOpenAITokenError(p.c, http.StatusServiceUnavailable, "api_error", "No available providers")
		return
	}
	if WriteGroupSelectionBusinessError(p.c, err, false, keyhttp.GetAPIKeyFromContext, gatewayprovider.ModelDisplayCatalogue{}, func(status int, kind, message string, _ bool) { writeOpenAITokenError(p.c, status, kind, message) }) {
		return
	}
	cls := tokenSelectionError(p.c, p.ports.ResolvedDiagnoser, p.key, p.call.RoutingModel, p.call.Model)
	if !cls.ModelNotFound {
		MarkOpsRoutingCapacityLimitedIfNoAvailable(p.c, err)
	}
	writeOpenAITokenError(p.c, cls.Status, cls.ErrType, cls.Message)
}

// Forward counts the current captured target and retires its returned resource.
// Callers invoke it after Select; the selection argument is observation data,
// while the target comes from p.selection. Nil failure means counting
// completed, and classified upstream errors expose retry policy to the loop.
func (p *inputTokensAttemptBridge) Forward(_ textflow.Selection) *textflow.AttemptFailure {
	start := time.Now()
	// Release the captured provider resource on every forwarding return,
	// including errors, so a retry wait cannot retain capacity from the
	// completed count.
	err := func() error {
		if p.selection.Release != nil {
			defer p.selection.Release()
		}
		return p.selection.Target.ForwardInputTokens(p.Context(), p.c, p.call.Body)
	}()
	SetOpsLatencyMs(p.c, OpsResponseLatencyMsKey, time.Since(start).Milliseconds())
	if err == nil {
		return nil
	}
	// Preserve upstream retry classification when returning to the counting
	// loop; ordinary errors remain policy-free and stop that loop through
	// its terminal observer.
	if original, ok := errors.AsType[*forwardcore.UpstreamFailoverError](err); ok {
		return &textflow.AttemptFailure{Cause: err, Policy: original.RetryFailure()}
	}
	return &textflow.AttemptFailure{Cause: err}
}

// ForwardFailed logs an unclassified count failure with its selected provider
// ID.
func (p *inputTokensAttemptBridge) ForwardFailed(selected textflow.Selection, err error) {
	p.call.Log.Error("openai_responses_input_tokens.forward_failed", zap.Int64("provider_id", selected.Provider.ID), zap.Error(err))
}

// Exhausted writes terminal OpenAI output using the classified attempt's
// upstream failure. Callers supply a nonnil failure. Error-rule matching and
// Ops hooks operate on that projection after forwarding has already released
// selected resources.
func (p *inputTokensAttemptBridge) Exhausted(failure *textflow.AttemptFailure) {
	var original *forwardcore.UpstreamFailoverError
	errors.As(failure.Cause, &original)
	WriteOpenAIFailoverExhausted(p.c, ProjectOpenAIFailoverError(original), false, p.ports.Rules, FailoverErrorHooks{Upstream: func(c *gin.Context, status int, message string) { SetOpsUpstreamError(c, status, message, "") }, SkipMonitoring: MarkOpsSkipMonitoring}, func(c *gin.Context, status int, kind, message string, _ bool) {
		writeOpenAITokenError(c, status, kind, message)
	})
}
