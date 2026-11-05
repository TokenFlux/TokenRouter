package antigravity

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	mathrand "math/rand"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/tidwall/gjson"

	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	googlewire "github.com/TokenFlux/TokenRouter/internal/protocol/google"
)

// retry_loop.go executes Antigravity retries and selects model waits, credit
// recovery, or provider switching.
// Antigravity forwarding retries one provider against its configured base URL,
// combining ordinary transport/status retries with structured model delays and
// enabled-credit recovery. The selected URL list currently contains one entry,
// so endpoint-fallback branches do not introduce account-management
// alternatives during forwarding.
//
// Model limits can request provider switching unless enabled credits or
// single-provider mode allow dispatch. Shared capacity uses a fixed retry
// budget and writes a process-local deadline only after exhaustion; concurrent
// sequences already started can each retry. Captured HTTP failures are closed
// and rebuilt for callers, while smart-retry termination distinguishes 429/503
// from other statuses. Requested waits have their own budgets, with transport
// time additional; provider callbacks own health, affinity and credit state.

// Smart retry actions tell the outer loop to use ordinary handling, return
// selected results, or advance its supplied endpoint list.
const (
	SmartRetryActionContinue      SmartRetryAction = iota // continue ordinary retry handling
	SmartRetryActionBreakWithResp                         // return selected response or failure
	SmartRetryActionContinueURL                           // advance supplied endpoint selection
)

// Smart retry kinds distinguish structured local waits, provider-model limits,
// and shared capacity so each caller can apply its existing recovery policy.
//
//nolint:decorder // Separate const blocks keep each iota sequence starting at zero.
const (
	SmartRetryNone       SmartRetryKind = iota // no recognized structured retry decision
	SmartRetryShort                            // short wait on the current provider
	SmartRetryModelLimit                       // long provider-model quota window
	SmartRetryCapacity                         // shared capacity with fixed retry wait
)

// RetryInput supplies one provider request and model-limit observations used
// by local retry policy.
type RetryInput struct {
	// Ctx supplies request cancellation for waits and HTTP requests; callers
	// provide a nonnil context and callbacks compatible with its lifetime.
	Ctx          context.Context
	Prefix       string // caller diagnostic log prefix
	ProviderID   int64  // selected retry provider identifier
	ProviderName string // selected provider diagnostic name
	// Native enables structured platform retry selection; OveragesEnabled permits
	// AI credit use; SingleProvider selects bounded in-place handling when
	// provider switching is unavailable.
	Native, OveragesEnabled, SingleProvider bool
	// AccessToken supplies bearer authentication for repeated requests; Action
	// selects the v1internal operation reused by each attempt without altering
	// its request body.
	AccessToken, Action string
	// Body supplies wrapped request bytes borrowed by repeated request readers;
	// callers keep its storage stable through all dispatches and enabled-credit
	// preparation.
	Body []byte
	// RequestedModel supplies local-window lookup identity; an empty value skips
	// the precheck while upstream RPC parsing can still select a model.
	RequestedModel  string
	IsStickySession bool // observed sticky-session affinity flag
	// CreditsExhausted reads enabled-credit eligibility for native overages
	// paths; required when that policy can be considered for the request.
	CreditsExhausted func() bool
	// ModelLimited reports the existing free-quota window before native credit
	// injection; required when requested model and enabled overages select that
	// precheck.
	ModelLimited func(context.Context, string) bool
	// ModelRemaining reads remaining local-window time for nonempty
	// RequestedModel; credit injection or single-provider mode can bypass the
	// observed delay.
	ModelRemaining func(context.Context, string) time.Duration
}

// RetryObservation carries provider and upstream diagnostic identity for the
// owning error observer.
type RetryObservation struct {
	ProviderID         int64  // observed upstream provider identifier
	ProviderName       string // observed provider diagnostic name
	UpstreamStatusCode int    // observed HTTP status or zero
	// UpstreamRequestID carries response correlation identity; UpstreamURL
	// carries sanitized destination; Kind names the retry event; Message carries
	// sanitized error text; Detail carries permitted bounded body text.
	UpstreamRequestID, UpstreamURL, Kind, Message, Detail string
}

// RetryOptions supplies forwarding dispatch, diagnostic formatting, and
// provider-state callbacks for the retry adapter.
type RetryOptions struct {
	// BaseURL supplies the single forwarding destination at loop entry; an empty
	// value rejects execution before any HTTP request is built.
	BaseURL func() string
	// Do executes each prepared request; nil successful responses are rejected by
	// the main loop, and callbacks own cleanup of responses returned with errors.
	Do func(*http.Request) (*http.Response, error)
	// ApplyHeaders adjusts every normal, smart, and credit retry request after
	// construction and before dispatch; required on each selected request path.
	ApplyHeaders func(*http.Request)
	// LogBody permits captured upstream detail in retry observations; formatted
	// body logging elsewhere follows its own supplied truncation callback.
	LogBody bool
	// LogMaxBytes overrides the positive observation-detail byte allowance; zero
	// or negative values use the loop two-KiB default when detail logging
	// participates.
	LogMaxBytes int
	// TruncateString bounds observation detail when LogBody is true, using the
	// selected byte allowance for captured upstream text.
	TruncateString func(string, int) string
	// TruncateForLog formats bounded diagnostic response prefixes on selected
	// retry paths; callers provide the sanitizer used by their logging policy.
	TruncateForLog func([]byte, int) string
	// SafeURL removes sensitive destination detail before request-error
	// observations receive the URL; required when normal transport failure is
	// observed.
	SafeURL func(string) string
	// Observe synchronously records selected transport and retry diagnostics;
	// provider effects are owned by the callback and do not replace response
	// ownership.
	Observe func(RetryObservation)
	// SetError records exhausted normal transport failure after retries; required
	// on that terminal path before the wrapped request error returns.
	SetError func(int, string, string)
	// ReadErrorBody captures normal HTTP failure bytes; the main loop closes that
	// body afterward and rebuilds retained bytes for later readers.
	ReadErrorBody func(*http.Response) []byte
	// ApplyErrorPolicy precedes platform smart retry; handled returns select its
	// output status or propagate its error before further local retry decisions.
	ApplyErrorPolicy func(int, http.Header, []byte) (bool, int, error)
	// HandleError applies provider failure policy when platform limits remain
	// unhandled or model-limit mapping fails, using captured body data after
	// closing.
	HandleError func(int, http.Header, []byte)
	// SetModelLimits records a normalized model deadline and force flag; a false
	// result during long-delay handling permits broader provider error handling.
	SetModelLimits func(string, int, time.Time, bool) bool
	// ClearSticky removes affinity on smart-retry switch paths; local-window
	// prechecks return a switch signal separately, retaining the original sticky
	// observation for gateway accounting.
	ClearSticky func()
	// CreditsModel maps upstream or blank model observations to the provider
	// credit key used by enabled-credit callbacks for this request.
	CreditsModel func(string) string
	// ClearCredits resets exhausted-credit state after a successful credit-backed
	// response, before its open response body returns to the caller.
	ClearCredits func()
	// CreditsFailure owns unsuccessful credit responses for bounded reading and
	// closing, then decides provider exhausted-credit state from those
	// observations.
	CreditsFailure func(string, *http.Response, error)
	// Internal500Exhausted updates provider penalty state when the final matching
	// HTTP failure retains the tracked internal-500 flag for this normal
	// sequence.
	Internal500Exhausted func()
	// ResetInternal500 clears provider internal-500 penalty state after any
	// selected response below 400, including successful smart or credit retries.
	ResetInternal500 func()
}

// RetryAdapter performs provider-local request retries using Options for
// transport dispatch and provider policy callbacks.
type RetryAdapter struct{ Options RetryOptions } // retry dispatch and policy callbacks

// AntigravityRetryLoopResult holds the selected response for the outer
// exchange owner to consume.
type AntigravityRetryLoopResult struct {
	// Resp carries the selected response body for caller reading and closing;
	// failure bodies can be reconstructed from captured prefix bytes.
	Resp *http.Response
}

// SmartRetryAction names the next outer-loop step selected by platform retry
// handling.
type SmartRetryAction int

// SmartRetryResult combines an outer-loop action with a response or terminal
// retry/switch failure.
type SmartRetryResult struct {
	// Action controls result consumption. For BreakWithResp, the outer loop
	// checks Err first, then SwitchError, then uses Resp with caller body
	// ownership.
	Action      SmartRetryAction                // selected outer retry-loop step
	Resp        *http.Response                  // selected response for caller consumption
	Err         error                           // selected wait or dispatch failure
	SwitchError *AntigravityProviderSwitchError // selected provider-switch model-limit signal
}

// SmartRetryKind identifies one mutually exclusive policy selected from RPC details.
type SmartRetryKind uint8

// SmartRetryDecision carries the classification, effective wait, and normalized
// model selected for a native Antigravity failure. Its zero value selects no
// structured retry handling.
type SmartRetryDecision struct {
	// Kind selects how callers interpret WaitDuration and ModelName. None leaves
	// both fields zero; every recognized kind supplies a positive wait and a
	// nonempty normalized model.
	Kind         SmartRetryKind // selected structured retry policy
	WaitDuration time.Duration  // effective retry or cooldown duration
	ModelName    string         // normalized upstream model identity
}

// AntigravitySmartRetryInfo combines normalized model identity, parsed wait,
// and accumulated capacity-reason observations.
type AntigravitySmartRetryInfo struct {
	// RetryDelay retains the last parseable RPC delay, defaulting when final
	// delay is nonpositive; capacity policy can replace this wait.
	RetryDelay time.Duration
	// ModelName retains the last supplied normalized model metadata; a blank
	// final value prevents the parser from returning retry observations.
	ModelName string
	// IsModelCapacityExhausted accumulates a capacity reason from any ErrorInfo
	// detail and can coexist with separate rate-limit reason observations.
	IsModelCapacityExhausted bool
}

// String formats the selected provider identifier for retry diagnostics
// without credentials.
func (p RetryInput) String() string {
	return fmt.Sprintf("antigravity retry provider=%d", p.ProviderID)
}

// ResolveAntigravityForwardBaseURL selects a configured endpoint by normalized
// mode and paid status. Daily or sandbox modes select index one when
// available; an empty mode also selects that index for paid accounts.
//
// Other modes and free defaults select index zero, and an empty endpoint list
// returns empty. Index selection uses BaseURLs as configured rather than
// resolving endpoint availability or token audience. Callers serialize package
// endpoint replacement with readers, and the retry loop uses the selected
// single URL for forwarding.
func ResolveAntigravityForwardBaseURL(mode string, paid bool) string {
	baseURLs := BaseURLs
	if len(baseURLs) == 0 {
		return ""
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	if (mode == "daily" || mode == "sandbox") && len(baseURLs) > 1 {
		return baseURLs[1]
	}
	if (mode == "" || mode == "auto") && paid && len(baseURLs) > 1 {
		return baseURLs[1]
	}
	return baseURLs[0]
}

// HandleSmartRetry selects credits, local waits, capacity recovery, or
// provider switching for a captured 429/503. Callers supply a nonnil response
// with retained failure bytes and applicable callbacks; selected response
// bodies return for caller reading and closing. The caller has already
// consumed and closed the initial response body before entering this recovery.
//
// Explicit quota wording permits an enabled-credit attempt before structured
// limit handling. Long model delays normally mark limits and clear affinity,
// while single-provider 503 uses bounded in-place waits. Capacity checks a
// stored cooldown before its separate sequence, then writes a deadline after
// exhaustion without reserving active work. A retry status other than 429/503
// terminates that sequence even if another HTTP failure. Exhausted responses
// combine initial status/headers with latest captured error bytes.
func (s *RetryAdapter) HandleSmartRetry(p RetryInput, resp *http.Response, respBody []byte, baseURL string, urlIdx int, availableURLs []string) *SmartRetryResult {
	// Consider endpoint advancement before model handling when a supplied list
	// has another URL, because generic exhaustion can select a different
	// configured destination.
	if resp.StatusCode == http.StatusTooManyRequests && IsURLLevelRateLimit(respBody) && urlIdx < len(availableURLs)-1 {
		logger.LegacyPrintf("service.antigravity_gateway", "%s URL fallback (429): %s -> %s", p.Prefix, baseURL, availableURLs[urlIdx+1])
		return &SmartRetryResult{Action: SmartRetryActionContinueURL}
	}

	category := Antigravity429Unknown
	if resp.StatusCode == http.StatusTooManyRequests {
		category = ClassifyAntigravity429(respBody)
	}

	decision := ClassifyAntigravitySmartRetry(p.Native, respBody)
	waitDuration := decision.WaitDuration

	// Attempt enabled credits only after explicit quota classification and
	// current credit eligibility, so an ordinary model wait does not alone spend
	// the overage path.
	if resp.StatusCode == http.StatusTooManyRequests &&
		category == Antigravity429QuotaExhausted &&
		p.OveragesEnabled &&
		!p.CreditsExhausted() {
		result := s.AttemptCreditsOveragesRetry(p, baseURL, decision.ModelName, waitDuration, resp.StatusCode, respBody)
		if result.Handled && result.Resp != nil {
			return &SmartRetryResult{
				Action: SmartRetryActionBreakWithResp,
				Resp:   result.Resp,
			}
		}
	}

	// Handle long model delays before short-retry dispatch; the single-provider
	// 503 exception bounds local waiting while other paths mark a deadline and
	// clear affinity.
	if decision.Kind == SmartRetryModelLimit {
		// A single-provider 503 waits in place so local throttling does not add another delay.
		if resp.StatusCode == http.StatusServiceUnavailable && p.SingleProvider {
			return s.HandleSingleProviderRetryInPlace(p, resp, respBody, baseURL, waitDuration, decision.ModelName)
		}

		rateLimitDuration := waitDuration
		if rateLimitDuration <= 0 {
			// Retain a positive defensive deadline default; current structured selection
			// already supplies a positive wait before this model-limit branch runs.
			rateLimitDuration = AntigravityDefaultRateLimitDuration
		}
		logger.LegacyPrintf("service.antigravity_gateway", "%s status=%d oauth_long_delay model=%s provider=%d upstream_retry_delay=%v body=%s (model rate limit, switch provider)",
			p.Prefix, resp.StatusCode, decision.ModelName, p.ProviderID, rateLimitDuration, s.Options.TruncateForLog(respBody, 200))

		resetAt := time.Now().Add(rateLimitDuration)
		if !s.Options.SetModelLimits(decision.ModelName, resp.StatusCode, resetAt, false) {
			s.Options.HandleError(resp.StatusCode, resp.Header, respBody)
			logger.LegacyPrintf("service.antigravity_gateway", "%s status=%d rate_limited provider=%d (no model mapping)", p.Prefix, resp.StatusCode, p.ProviderID)
		}
		s.Options.ClearSticky()

		return &SmartRetryResult{
			Action: SmartRetryActionBreakWithResp,
			SwitchError: &AntigravityProviderSwitchError{
				OriginalProviderID: p.ProviderID,
				RateLimitedModel:   decision.ModelName,
				IsStickySession:    p.IsStickySession,
			},
		}
	}

	// Select same-provider retry for short structured waits or capacity, keeping
	// capacity on its independent attempt budget instead of consuming the
	// ordinary retry count.
	if decision.Kind == SmartRetryShort || decision.Kind == SmartRetryCapacity {
		var lastRetryResp *http.Response
		var lastRetryBody []byte

		// Override both attempts and wait for accumulated capacity, since its fixed
		// local retry policy disregards the upstream long-delay observation.
		maxAttempts := AntigravitySmartRetryMaxAttempts
		if decision.Kind == SmartRetryCapacity {
			maxAttempts = AntigravityModelCapacityRetryMaxAttempts
			waitDuration = AntigravityModelCapacityRetryWait

			// Read the cooldown before this sequence starts; the lookup reserves no
			// active retry work, so concurrently admitted sequences may each continue.
			if decision.ModelName != "" {
				modelCapacityExhaustedMu.RLock()
				cooldownUntil, exists := modelCapacityExhaustedUntil[decision.ModelName]
				modelCapacityExhaustedMu.RUnlock()
				if exists && time.Now().Before(cooldownUntil) {
					log.Printf("%s status=%d model_capacity_exhausted_dedup model=%s provider=%d cooldown_until=%v (skip retry)",
						p.Prefix, resp.StatusCode, decision.ModelName, p.ProviderID, cooldownUntil.Format("15:04:05"))
					return &SmartRetryResult{
						Action: SmartRetryActionBreakWithResp,
						Resp: &http.Response{
							StatusCode: resp.StatusCode,
							Header:     resp.Header.Clone(),
							Body:       io.NopCloser(bytes.NewReader(respBody)),
						},
					}
				}
			}
		}

		for attempt := 1; attempt <= maxAttempts; attempt++ {
			log.Printf("%s status=%d oauth_smart_retry attempt=%d/%d delay=%v model=%s provider=%d",
				p.Prefix, resp.StatusCode, attempt, maxAttempts, waitDuration, decision.ModelName, p.ProviderID)

			// Wait before dispatching again and let caller cancellation stop this
			// sequence; the initial failure bytes remain available for terminal handling.
			timer := time.NewTimer(waitDuration)
			select {
			case <-p.Ctx.Done():
				timer.Stop()
				log.Printf("%s status=context_canceled_during_smart_retry", p.Prefix)
				return &SmartRetryResult{Action: SmartRetryActionBreakWithResp, Err: p.Ctx.Err()}
			case <-timer.C:
			}

			retryReq, err := NewAPIRequestWithURL(p.Ctx, baseURL, p.Action, p.AccessToken, p.Body)
			if err != nil {
				logger.LegacyPrintf("service.antigravity_gateway", "%s status=smart_retry_request_build_failed error=%v", p.Prefix, err)
				s.Options.HandleError(resp.StatusCode, resp.Header, respBody)
				return &SmartRetryResult{
					Action: SmartRetryActionBreakWithResp,
					Resp: &http.Response{
						StatusCode: resp.StatusCode,
						Header:     resp.Header.Clone(),
						Body:       io.NopCloser(bytes.NewReader(respBody)),
					},
				}
			}
			s.Options.ApplyHeaders(retryReq)

			retryResp, retryErr := s.Options.Do(retryReq)
			if retryErr == nil && retryResp != nil && retryResp.StatusCode != http.StatusTooManyRequests && retryResp.StatusCode != http.StatusServiceUnavailable {
				log.Printf("%s status=%d smart_retry_success attempt=%d/%d", p.Prefix, retryResp.StatusCode, attempt, maxAttempts)
				// Clear capacity state when a retry leaves 429/503, including a different
				// HTTP failure, because this sequence terminates on that broader response
				// classification.
				if decision.Kind == SmartRetryCapacity && decision.ModelName != "" {
					modelCapacityExhaustedMu.Lock()
					delete(modelCapacityExhaustedUntil, decision.ModelName)
					modelCapacityExhaustedMu.Unlock()
				}
				return &SmartRetryResult{Action: SmartRetryActionBreakWithResp, Resp: retryResp}
			}

			// Consume a transport-failed attempt without replacing captured HTTP bytes,
			// allowing a later dispatch within the remaining smart-retry budget.
			if retryErr != nil || retryResp == nil {
				log.Printf("%s status=smart_retry_network_error attempt=%d/%d error=%v", p.Prefix, attempt, maxAttempts, retryErr)
				continue
			}

			// Release the previously retained retry before replacing it; failure
			// capture already closed that body, so this cleanup can call Close again.
			if lastRetryResp != nil {
				_ = lastRetryResp.Body.Close()
			}
			lastRetryResp = retryResp
			if retryResp != nil {
				// Capture eight KiB at most and close the failed retry body; ignored read
				// errors retain partial bytes used when rebuilding the final failure.
				lastRetryBody, _ = io.ReadAll(io.LimitReader(retryResp.Body, 8<<10))
				_ = retryResp.Body.Close()
			}

			// Refresh noncapacity waits from later short or capacity decisions only.
			// Later model limits leave this sequence unchanged, while an initial
			// capacity decision retains its fixed interval and separate retry budget.
			if decision.Kind != SmartRetryCapacity && attempt < maxAttempts && lastRetryBody != nil {
				newDecision := ClassifyAntigravitySmartRetry(p.Native, lastRetryBody)
				if (newDecision.Kind == SmartRetryShort || newDecision.Kind == SmartRetryCapacity) && newDecision.WaitDuration > 0 {
					waitDuration = newDecision.WaitDuration
				}
			}
		}

		rateLimitDuration := waitDuration
		if rateLimitDuration <= 0 {
			// Retain a positive defensive deadline default; current structured selection
			// already supplies a positive wait before this model-limit branch runs.
			rateLimitDuration = AntigravityDefaultRateLimitDuration
		}
		retryBody := lastRetryBody
		if retryBody == nil {
			retryBody = respBody
		}

		// Return captured capacity failure from this provider after its dedicated
		// sequence, recording a later admission deadline while already running
		// sequences remain independent.
		if decision.Kind == SmartRetryCapacity {
			// Write the deadline after exhaustion so later sequences can skip capacity
			// retries; this update does not interrupt or reserve already admitted work.
			if decision.ModelName != "" {
				modelCapacityExhaustedMu.Lock()
				modelCapacityExhaustedUntil[decision.ModelName] = time.Now().Add(AntigravityModelCapacityCooldown)
				modelCapacityExhaustedMu.Unlock()
			}
			log.Printf("%s status=%d smart_retry_exhausted_model_capacity attempts=%d model=%s provider=%d body=%s (model capacity exhausted, not switching provider)",
				p.Prefix, resp.StatusCode, maxAttempts, decision.ModelName, p.ProviderID, s.Options.TruncateForLog(retryBody, 200))
			return &SmartRetryResult{
				Action: SmartRetryActionBreakWithResp,
				Resp: &http.Response{
					StatusCode: resp.StatusCode,
					Header:     resp.Header.Clone(),
					Body:       io.NopCloser(bytes.NewReader(retryBody)),
				},
			}
		}

		// In single-provider mode, return the 503 to the caller for final handling.
		// Leave model cooldown decisions to the outer orchestration.
		if resp.StatusCode == http.StatusServiceUnavailable && p.SingleProvider {
			logger.LegacyPrintf("service.antigravity_gateway", "%s status=%d smart_retry_exhausted_single_provider attempts=%d model=%s provider=%d body=%s (return 503 directly)",
				p.Prefix, resp.StatusCode, AntigravitySmartRetryMaxAttempts, decision.ModelName, p.ProviderID, s.Options.TruncateForLog(retryBody, 200))
			return &SmartRetryResult{
				Action: SmartRetryActionBreakWithResp,
				Resp: &http.Response{
					StatusCode: resp.StatusCode,
					Header:     resp.Header.Clone(),
					Body:       io.NopCloser(bytes.NewReader(retryBody)),
				},
			}
		}

		log.Printf("%s status=%d smart_retry_exhausted attempts=%d model=%s provider=%d upstream_retry_delay=%v body=%s (switch provider)",
			p.Prefix, resp.StatusCode, maxAttempts, decision.ModelName, p.ProviderID, rateLimitDuration, s.Options.TruncateForLog(retryBody, 200))

		resetAt := time.Now().Add(rateLimitDuration)
		s.Options.SetModelLimits(decision.ModelName, resp.StatusCode, resetAt, true)

		// Clear affinity after recording model limiting so the outer switch signal
		// can select another provider while retaining the original sticky-session
		// observation.
		s.Options.ClearSticky()

		return &SmartRetryResult{
			Action: SmartRetryActionBreakWithResp,
			SwitchError: &AntigravityProviderSwitchError{
				OriginalProviderID: p.ProviderID,
				RateLimitedModel:   decision.ModelName,
				IsStickySession:    p.IsStickySession,
			},
		}
	}

	return &SmartRetryResult{Action: SmartRetryActionContinue}
}

// HandleSingleProviderRetryInPlace retries a captured long-delay failure on
// the same provider and base URL. It requires a nonnil response, request
// context, and dispatch/logging callbacks; returned selected bodies belong to
// the caller. The caller has already consumed and closed the initial body.
//
// Requested waits are clamped between one and fifteen seconds, shortened to
// fit thirty seconds total, and limited to three attempts. Transport duration
// is additional, and a transport failure consumes an attempt. A status other
// than 429/503 terminates with its open response; otherwise captured prefixes
// replace the retained body while exhaustion returns initial status/headers
// and the latest available bytes. Cancellation during waiting returns its
// context error.
func (s *RetryAdapter) HandleSingleProviderRetryInPlace(
	p RetryInput,
	resp *http.Response,
	respBody []byte,
	baseURL string,
	waitDuration time.Duration,
	modelName string,
) *SmartRetryResult {
	// Clamp the initial wait to local per-attempt limits before accounting for
	// the total budget, so a long supplier delay remains a bounded retry wait.
	if waitDuration > AntigravitySingleProviderSmartRetryMaxWait {
		waitDuration = AntigravitySingleProviderSmartRetryMaxWait
	}
	if waitDuration < AntigravitySmartRetryMinWait {
		waitDuration = AntigravitySmartRetryMinWait
	}

	logger.LegacyPrintf("service.antigravity_gateway", "%s status=%d single_provider_503_retry_in_place model=%s provider=%d upstream_retry_delay=%v (retrying in-place instead of rate-limiting)",
		p.Prefix, resp.StatusCode, modelName, p.ProviderID, waitDuration)

	var lastRetryResp *http.Response
	var lastRetryBody []byte
	totalWaited := time.Duration(0)

	for attempt := 1; attempt <= AntigravitySingleProviderSmartRetryMaxAttempts; attempt++ {
		// Shorten a requested wait to the remaining total delay budget before timer
		// creation, keeping accumulated waits bounded independently from dispatch
		// duration.
		if totalWaited+waitDuration > AntigravitySingleProviderSmartRetryTotalMaxWait {
			remaining := AntigravitySingleProviderSmartRetryTotalMaxWait - totalWaited
			if remaining <= 0 {
				logger.LegacyPrintf("service.antigravity_gateway", "%s single_provider_503_retry: total_wait_exceeded total=%v max=%v, giving up",
					p.Prefix, totalWaited, AntigravitySingleProviderSmartRetryTotalMaxWait)
				break
			}
			waitDuration = remaining
		}

		logger.LegacyPrintf("service.antigravity_gateway", "%s status=%d single_provider_503_retry attempt=%d/%d delay=%v total_waited=%v model=%s provider=%d",
			p.Prefix, resp.StatusCode, attempt, AntigravitySingleProviderSmartRetryMaxAttempts, waitDuration, totalWaited, modelName, p.ProviderID)

		// Observe cancellation while waiting before the next dispatch, stopping the
		// timer so cancelled callers do not begin another in-place request.
		timer := time.NewTimer(waitDuration)
		select {
		case <-p.Ctx.Done():
			timer.Stop()
			logger.LegacyPrintf("service.antigravity_gateway", "%s status=context_canceled_during_single_provider_retry", p.Prefix)
			return &SmartRetryResult{Action: SmartRetryActionBreakWithResp, Err: p.Ctx.Err()}
		case <-timer.C:
		}
		totalWaited += waitDuration

		retryReq, err := NewAPIRequestWithURL(p.Ctx, baseURL, p.Action, p.AccessToken, p.Body)
		if err != nil {
			logger.LegacyPrintf("service.antigravity_gateway", "%s single_provider_503_retry: request_build_failed error=%v", p.Prefix, err)
			break
		}
		s.Options.ApplyHeaders(retryReq)

		retryResp, retryErr := s.Options.Do(retryReq)
		if retryErr == nil && retryResp != nil && retryResp.StatusCode != http.StatusTooManyRequests && retryResp.StatusCode != http.StatusServiceUnavailable {
			logger.LegacyPrintf("service.antigravity_gateway", "%s status=%d single_provider_503_retry_success attempt=%d/%d total_waited=%v",
				p.Prefix, retryResp.StatusCode, attempt, AntigravitySingleProviderSmartRetryMaxAttempts, totalWaited)
			// Release a retained failed response before transferring the accepted body
			// to the caller; earlier failure capture can make this a repeated close.
			if lastRetryResp != nil {
				_ = lastRetryResp.Body.Close()
			}
			return &SmartRetryResult{Action: SmartRetryActionBreakWithResp, Resp: retryResp}
		}

		// Keep the latest captured HTTP bytes across transport failures, consuming
		// an attempt while allowing a later dispatch within the bounded in-place
		// budget.
		if retryErr != nil || retryResp == nil {
			logger.LegacyPrintf("service.antigravity_gateway", "%s single_provider_503_retry: network_error attempt=%d/%d error=%v",
				p.Prefix, attempt, AntigravitySingleProviderSmartRetryMaxAttempts, retryErr)
			continue
		}

		// Release the previously retained retry before replacing it; failure
		// capture has already closed that body, so repeated closing can occur here.
		if lastRetryResp != nil {
			_ = lastRetryResp.Body.Close()
		}
		lastRetryResp = retryResp
		// Capture at most eight KiB before closing the failed response, retaining
		// any prefix even when reading fails for final failure reconstruction.
		lastRetryBody, _ = io.ReadAll(io.LimitReader(retryResp.Body, 8<<10))
		_ = retryResp.Body.Close()

		// Accept every recognized later delay without changing the initial retry
		// path or model. Clamp before the next timer so even a model-limit decision
		// stays within the single-provider per-wait budget.
		if attempt < AntigravitySingleProviderSmartRetryMaxAttempts && lastRetryBody != nil {
			newDecision := ClassifyAntigravitySmartRetry(p.Native, lastRetryBody)
			if newDecision.WaitDuration > 0 {
				waitDuration = max(min(newDecision.WaitDuration, AntigravitySingleProviderSmartRetryMaxWait), AntigravitySmartRetryMinWait)
			}
		}
	}

	// Return the original status and the latest available error body when retries stop.
	// The caller handles any remaining retry or failure policy.
	retryBody := lastRetryBody
	if retryBody == nil {
		retryBody = respBody
	}
	logger.LegacyPrintf("service.antigravity_gateway", "%s status=%d single_provider_503_retry_exhausted attempts=%d total_waited=%v model=%s provider=%d body=%s (return 503 directly)",
		p.Prefix, resp.StatusCode, AntigravitySingleProviderSmartRetryMaxAttempts, totalWaited, modelName, p.ProviderID, s.Options.TruncateForLog(retryBody, 200))

	return &SmartRetryResult{
		Action: SmartRetryActionBreakWithResp,
		Resp: &http.Response{
			StatusCode: resp.StatusCode,
			Header:     resp.Header.Clone(),
			Body:       io.NopCloser(bytes.NewReader(retryBody)),
		},
	}
}

// AntigravityRetryLoop performs local forwarding attempts using one configured
// base URL and required RetryOptions callbacks. Callers supply stable wrapped
// bytes and a nonnil context; successful selected bodies return open, while
// captured HTTP failure bodies are closed and reconstructed.
//
// Native enabled-credit eligibility can bypass an existing local model window,
// and single-provider mode also dispatches despite that window. Custom error
// policy runs before smart retry, followed by ordinary bounded
// status/transport retries. Provider-switch signals retain sticky identity.
// The internal-500 flag is cleared only by a nonmatching retried ordinary HTTP
// error, so intervening transport or smart-limit paths do not necessarily
// clear it. Any selected status below 400 records URL success and resets the
// provider internal-500 counter.
func (s *RetryAdapter) AntigravityRetryLoop(p RetryInput) (*AntigravityRetryLoopResult, error) {
	// Try enabled-credit injection before local-window rejection when the model
	// is limited and credits remain eligible, so a successfully rewritten body
	// can bypass that free-quota wait.
	overagesInjected := false
	if p.RequestedModel != "" && p.Native &&
		p.OveragesEnabled && !p.CreditsExhausted() &&
		p.ModelLimited(p.Ctx, p.RequestedModel) {
		if creditsBody := InjectEnabledCreditTypes(p.Body); creditsBody != nil {
			p.Body = creditsBody
			overagesInjected = true
			logger.LegacyPrintf("service.antigravity_gateway", "%s pre_check: model_rate_limited_credits_inject model=%s provider=%d (injecting enabledCreditTypes)",
				p.Prefix, p.RequestedModel, p.ProviderID)
		}
	}

	// Combine local remaining time with the rewritten-credit observation and
	// single-provider flag before dispatch, because each can change whether the
	// model window blocks execution.
	if p.RequestedModel != "" {
		if remaining := p.ModelRemaining(p.Ctx, p.RequestedModel); remaining > 0 {
			// A successfully injected credit body can proceed through a local model
			// window; failed injection leaves ordinary provider selection policy
			// responsible for that wait.
			if overagesInjected {
				logger.LegacyPrintf("service.antigravity_gateway", "%s pre_check: credits_injected_ignore_rate_limit remaining=%v model=%s provider=%d",
					p.Prefix, remaining.Truncate(time.Millisecond), p.RequestedModel, p.ProviderID)
			} else if p.SingleProvider {
				// Single-provider mode sends the request and handles a 503 with in-place retries.
				// Waiting for the local limit here would add another delay before that retry loop.
				logger.LegacyPrintf("service.antigravity_gateway", "%s pre_check: single_provider_retry skipping rate_limit remaining=%v model=%s provider=%d (will retry in-place if 503)",
					p.Prefix, remaining.Truncate(time.Millisecond), p.RequestedModel, p.ProviderID)
			} else {
				logger.LegacyPrintf("service.antigravity_gateway", "%s pre_check: rate_limit_switch remaining=%v model=%s provider=%d",
					p.Prefix, remaining.Truncate(time.Millisecond), p.RequestedModel, p.ProviderID)
				return nil, &AntigravityProviderSwitchError{
					OriginalProviderID: p.ProviderID,
					RateLimitedModel:   p.RequestedModel,
					IsStickySession:    p.IsStickySession,
				}
			}
		}
	}

	baseURL := s.Options.BaseURL()
	if baseURL == "" {
		return nil, errors.New("no antigravity forward base url configured")
	}
	// Use only the configured forwarding URL, so generic endpoint-advancement
	// branches remain inactive in this loop unless its candidate construction
	// changes.
	availableURLs := []string{baseURL}

	var resp *http.Response
	var usedBaseURL string
	logBody := s.Options.LogBody
	// Two KiB bounds default observation detail when body logging is enabled.
	maxBytes := 2048
	if s.Options.LogMaxBytes > 0 {
		maxBytes = s.Options.LogMaxBytes
	}
	getUpstreamDetail := func(body []byte) string {
		if !logBody {
			return ""
		}
		return s.Options.TruncateString(string(body), maxBytes)
	}

urlFallbackLoop:
	for urlIdx, baseURL := range availableURLs {
		usedBaseURL = baseURL
		allAttemptsInternal500 := true // cleared by a nonmatching retried HTTP error
		for attempt := 1; attempt <= AntigravityMaxRetries; attempt++ {
			select {
			case <-p.Ctx.Done():
				logger.LegacyPrintf("service.antigravity_gateway", "%s status=context_canceled error=%v", p.Prefix, p.Ctx.Err())
				return nil, p.Ctx.Err()
			default:
			}

			upstreamReq, err := NewAPIRequestWithURL(p.Ctx, baseURL, p.Action, p.AccessToken, p.Body)
			if err != nil {
				return nil, err
			}
			s.Options.ApplyHeaders(upstreamReq)

			resp, err = s.Options.Do(upstreamReq)
			if err == nil && resp == nil {
				err = errors.New("upstream returned nil response")
			}
			if err != nil {
				safeErr := logredact.SanitizeUpstreamQueries(err.Error())
				s.Options.Observe(RetryObservation{
					ProviderID:         p.ProviderID,
					ProviderName:       p.ProviderName,
					UpstreamStatusCode: 0,
					UpstreamURL:        s.Options.SafeURL(upstreamReq.URL.String()),
					Kind:               "request_error",
					Message:            safeErr,
				})
				// Connection-shaped errors can advance a supplied endpoint list before
				// normal retry delay; the current one-entry list leaves this branch
				// inactive.
				if ShouldAntigravityFallbackToNextURL(err, 0) && urlIdx < len(availableURLs)-1 {
					logger.LegacyPrintf("service.antigravity_gateway", "%s URL fallback (connection error): %s -> %s", p.Prefix, baseURL, availableURLs[urlIdx+1])
					continue urlFallbackLoop
				}
				// Transport failures consume normal attempts while backoff observes
				// cancellation, preventing an immediate loop against the same configured
				// endpoint.
				if attempt < AntigravityMaxRetries {
					logger.LegacyPrintf("service.antigravity_gateway", "%s status=request_failed retry=%d/%d error=%v", p.Prefix, attempt, AntigravityMaxRetries, err)
					if !SleepAntigravityBackoffWithContext(p.Ctx, attempt) {
						logger.LegacyPrintf("service.antigravity_gateway", "%s status=context_canceled_during_backoff", p.Prefix)
						return nil, p.Ctx.Err()
					}
					continue
				}
				logger.LegacyPrintf("service.antigravity_gateway", "%s status=request_failed retries_exhausted error=%v", p.Prefix, err)
				s.Options.SetError(0, safeErr, "")
				return nil, fmt.Errorf("upstream request failed after retries: %w", err)
			}

			if resp.StatusCode >= 400 {
				respBody := s.Options.ReadErrorBody(resp)
				_ = resp.Body.Close()

				// A precheck-injected credit request can fail with exhaustion wording.
				// Give its callback a fresh readable failure body because the network
				// stream is already closed.
				if overagesInjected && ShouldMarkCreditsExhausted(resp, respBody, nil) {
					modelKey := s.Options.CreditsModel("")
					s.Options.CreditsFailure(modelKey, &http.Response{
						StatusCode: resp.StatusCode,
						Header:     resp.Header.Clone(),
						Body:       io.NopCloser(bytes.NewReader(respBody)),
					}, nil)
				}

				// Apply caller error policy to captured/closed HTTP failures before
				// platform retries so a handled status or terminal policy error wins over
				// smart retry.
				if handled, outStatus, policyErr := s.Options.ApplyErrorPolicy(resp.StatusCode, resp.Header, respBody); handled {
					if policyErr != nil {
						return nil, policyErr
					}
					resp = &http.Response{
						StatusCode: outStatus,
						Header:     resp.Header.Clone(),
						Body:       io.NopCloser(bytes.NewReader(respBody)),
					}
					break urlFallbackLoop
				}

				// Route 429/503 through platform classification after caller policy,
				// allowing quota credits and structured limits to select responses,
				// switching, or further ordinary attempts.
				if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
					smartResult := s.HandleSmartRetry(p, resp, respBody, baseURL, urlIdx, availableURLs)
					switch smartResult.Action {
					case SmartRetryActionContinueURL:
						continue urlFallbackLoop
					case SmartRetryActionBreakWithResp:
						if smartResult.Err != nil {
							return nil, smartResult.Err
						}
						if smartResult.SwitchError != nil {
							return nil, smartResult.SwitchError
						}
						resp = smartResult.Resp
						break urlFallbackLoop
					}

					// Use ordinary backoff for unrecognized 429/503 bodies when attempts
					// remain, because no structured model wait or capacity policy selected a
					// terminal result.
					if attempt < AntigravityMaxRetries {
						upstreamMsg := strings.TrimSpace(googlewire.ExtractPlatformMessage(respBody))
						upstreamMsg = logredact.SanitizeUpstreamQueries(upstreamMsg)
						s.Options.Observe(RetryObservation{
							ProviderID:         p.ProviderID,
							ProviderName:       p.ProviderName,
							UpstreamStatusCode: resp.StatusCode,
							UpstreamRequestID:  resp.Header.Get("x-request-id"),
							UpstreamURL:        s.Options.SafeURL(upstreamReq.URL.String()),
							Kind:               "retry",
							Message:            upstreamMsg,
							Detail:             getUpstreamDetail(respBody),
						})
						logger.LegacyPrintf("service.antigravity_gateway", "%s status=%d retry=%d/%d body=%s", p.Prefix, resp.StatusCode, attempt, AntigravityMaxRetries, s.Options.TruncateForLog(respBody, 200))
						if !SleepAntigravityBackoffWithContext(p.Ctx, attempt) {
							logger.LegacyPrintf("service.antigravity_gateway", "%s status=context_canceled_during_backoff", p.Prefix)
							return nil, p.Ctx.Err()
						}
						continue
					}

					// Apply provider error handling after unrecognized limit attempts
					// exhaust, then rebuild the captured body because the network failure
					// stream has already been closed.
					s.Options.HandleError(resp.StatusCode, resp.Header, respBody)
					logger.LegacyPrintf("service.antigravity_gateway", "%s status=%d rate_limited base_url=%s body=%s", p.Prefix, resp.StatusCode, baseURL, s.Options.TruncateForLog(respBody, 200))
					resp = &http.Response{
						StatusCode: resp.StatusCode,
						Header:     resp.Header.Clone(),
						Body:       io.NopCloser(bytes.NewReader(respBody)),
					}
					break urlFallbackLoop
				}

				// Ordinary transient statuses use the remaining normal attempt budget after
				// smart-limit selection has finished, retaining captured failure bytes for
				// terminal handling.
				if ShouldRetryAntigravityError(resp.StatusCode) {
					if attempt < AntigravityMaxRetries {
						upstreamMsg := strings.TrimSpace(googlewire.ExtractPlatformMessage(respBody))
						upstreamMsg = logredact.SanitizeUpstreamQueries(upstreamMsg)
						s.Options.Observe(RetryObservation{
							ProviderID:         p.ProviderID,
							ProviderName:       p.ProviderName,
							UpstreamStatusCode: resp.StatusCode,
							UpstreamRequestID:  resp.Header.Get("x-request-id"),
							UpstreamURL:        s.Options.SafeURL(upstreamReq.URL.String()),
							Kind:               "retry",
							Message:            upstreamMsg,
							Detail:             getUpstreamDetail(respBody),
						})
						logger.LegacyPrintf("service.antigravity_gateway", "%s status=%d retry=%d/%d body=%s", p.Prefix, resp.StatusCode, attempt, AntigravityMaxRetries, s.Options.TruncateForLog(respBody, 500))
						if !SleepAntigravityBackoffWithContext(p.Ctx, attempt) {
							logger.LegacyPrintf("service.antigravity_gateway", "%s status=context_canceled_during_backoff", p.Prefix)
							return nil, p.Ctx.Err()
						}
						// Clear the tracked internal-500 flag only after a different ordinary
						// retried HTTP error; transport and earlier smart-limit paths do not
						// reach this update.
						if !IsAntigravityInternalServerError(resp.StatusCode, respBody) {
							allAttemptsInternal500 = false
						}
						continue
					}
				}

				// Escalate the provider counter when the final response matches
				// internal-500 and the tracked flag remains set, including paths that did
				// not clear it earlier.
				if allAttemptsInternal500 && IsAntigravityInternalServerError(resp.StatusCode, respBody) {
					s.Options.Internal500Exhausted()
				}

				// Rebuild the selected failure after retry handling stops, letting later
				// gateway error readers consume captured bytes instead of the closed
				// network body.
				resp = &http.Response{
					StatusCode: resp.StatusCode,
					Header:     resp.Header.Clone(),
					Body:       io.NopCloser(bytes.NewReader(respBody)),
				}
				break urlFallbackLoop
			}

			break urlFallbackLoop
		}
	}

	// Record the configured endpoint after an accepted final status so account
	// callers sharing the availability tracker can prioritize its latest success.
	if resp != nil && resp.StatusCode < 400 && usedBaseURL != "" {
		DefaultURLAvailability.MarkSuccess(usedBaseURL)
	}

	// Reset the provider penalty after an accepted response, including smart and
	// credit retries, so earlier internal failures do not carry into that success.
	if resp != nil && resp.StatusCode < 400 {
		s.Options.ResetInternal500()
	}

	return &AntigravityRetryLoopResult{Resp: resp}, nil
}

// ShouldRetryAntigravityError reports whether the status belongs to the
// configured transient-failure retry set.
func ShouldRetryAntigravityError(statusCode int) bool {
	switch statusCode {
	case 429, 500, 502, 503, 504, 529:
		return true
	default:
		return false
	}
}

// IsURLLevelRateLimit recognizes exact case-sensitive generic exhaustion
// wording while excluding model-capacity wording. It supplies endpoint
// classification only; the caller determines status and whether a further
// configured URL exists.
func IsURLLevelRateLimit(body []byte) bool {
	bodyStr := string(body)
	return strings.Contains(bodyStr, "Resource has been exhausted") &&
		!strings.Contains(bodyStr, "capacity on this model")
}

// IsAntigravityConnectionError reports a wrapped net timeout or net operation
// error. Nil and other error shapes return false; the caller can use this
// result to choose retry or endpoint handling.
func IsAntigravityConnectionError(err error) bool {
	if err == nil {
		return false
	}

	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	var opErr *net.OpError
	return errors.As(err, &opErr)
}

// ShouldAntigravityFallbackToNextURL permits endpoint advancement for
// recognized connection errors or status 429. A true result still requires
// another caller-supplied endpoint; current forwarding constructs a one-entry
// list.
func ShouldAntigravityFallbackToNextURL(err error, statusCode int) bool {
	if IsAntigravityConnectionError(err) {
		return true
	}
	return statusCode == http.StatusTooManyRequests
}

// SleepAntigravityBackoffWithContext waits for a one-based attempt delay with
// time-seeded twenty-percent jitter. The exponential base is capped before
// jitter, and cancellation selected during waiting stops the timer and returns
// false. Callers provide a nonnil context and a positive bounded attempt;
// simultaneous timer readiness and cancellation follow the select outcome.
func SleepAntigravityBackoffWithContext(ctx context.Context, attempt int) bool {
	delay := min(AntigravityRetryBaseDelay*time.Duration(1<<uint(attempt-1)), AntigravityRetryMaxDelay)

	// Add twenty-percent jitter after capping the exponential base delay; the
	// final sleep can exceed that base cap, and cancellation still selects the
	// timer stop path.
	r := mathrand.New(mathrand.NewSource(time.Now().UnixNano()))
	jitter := time.Duration(float64(delay) * 0.2 * (r.Float64()*2 - 1))
	sleepFor := max(delay+jitter, 0)

	timer := time.NewTimer(sleepFor)
	select {
	case <-ctx.Done():
		timer.Stop()
		return false
	case <-timer.C:
		return true
	}
}

// ParseAntigravitySmartRetryInfo combines matching Google RPC status/reason
// details with a normalized model and delay. Invalid/incomplete input returns
// nil; the numeric error code is not required for this classification.
//
// Reason flags accumulate across ErrorInfo entries, while the last supplied
// model and last parseable RetryInfo delay win. RESOURCE_EXHAUSTED requires a
// rate-limit reason and UNAVAILABLE requires capacity, but an accumulated
// capacity flag can come from another detail. A blank final model rejects the
// result. A nonpositive final delay selects the configured default before the
// caller separates capacity waits from long model cooldowns. Reason and model
// observations need not come from the same detail, so this parser does not
// establish their original vendor association.
func ParseAntigravitySmartRetryInfo(body []byte) *AntigravitySmartRetryInfo {
	// Malformed envelopes supply no complete structured observation. Return nil
	// so the outer loop can select its ordinary error or retry handling.
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil
	}

	errObj, ok := parsed["error"].(map[string]any)
	if !ok {
		return nil
	}

	status, _ := errObj["status"].(string)
	isResourceExhausted := status == GoogleRPCStatusResourceExhausted
	isUnavailable := status == GoogleRPCStatusUnavailable

	if !isResourceExhausted && !isUnavailable {
		return nil
	}

	details, ok := errObj["details"].([]any)
	if !ok {
		return nil
	}

	var retryDelay time.Duration
	var modelName string
	var hasRateLimitExceeded bool      // required for RESOURCE_EXHAUSTED
	var hasModelCapacityExhausted bool // required for UNAVAILABLE

	for _, d := range details {
		dm, ok := d.(map[string]any)
		if !ok {
			continue
		}

		atType, _ := dm["@type"].(string)

		if atType == GoogleRPCTypeErrorInfo {
			if meta, ok := dm["metadata"].(map[string]any); ok {
				if model, ok := meta["model"].(string); ok {
					modelName = NormalizeAntigravityModelName(model)
				}
			}
			if reason, ok := dm["reason"].(string); ok {
				if reason == GoogleRPCReasonModelCapacityExhausted {
					hasModelCapacityExhausted = true
				}
				if reason == GoogleRPCReasonRateLimitExceeded {
					hasRateLimitExceeded = true
				}
			}
			continue
		}

		if atType == GoogleRPCTypeRetryInfo {
			delay, ok := dm["retryDelay"].(string)
			if !ok || delay == "" {
				continue
			}
			// Accept Go duration syntax and keep the last successfully parsed value,
			// even when nonpositive; final validation supplies the default after all
			// details are inspected.
			dur, err := time.ParseDuration(delay)
			if err != nil {
				logger.LegacyPrintf("service.antigravity_gateway", "[Antigravity] failed to parse retryDelay: %s error=%v", delay, err)
				continue
			}
			retryDelay = dur
		}
	}

	// Require the corresponding accumulated reason for the outer RPC status,
	// while retaining capacity observations gathered from other details for
	// later policy selection.
	if isResourceExhausted && !hasRateLimitExceeded {
		return nil
	}
	if isUnavailable && !hasModelCapacityExhausted {
		return nil
	}

	// Reject a blank final model observation before publishing retry data so
	// callers can attach wait and cooldown policy to a concrete model identity.
	if modelName == "" {
		return nil
	}

	// Substitute the default after all details when the final parsed delay is
	// nonpositive, including a later zero delay replacing an earlier positive
	// value.
	if retryDelay <= 0 {
		retryDelay = AntigravityDefaultRateLimitDuration
	}

	return &AntigravitySmartRetryInfo{
		RetryDelay:               retryDelay,
		ModelName:                modelName,
		IsModelCapacityExhausted: hasModelCapacityExhausted,
	}
}

// ClassifyAntigravitySmartRetry selects retry and cooldown observations
// for native requests. A false native flag or unrecognized body returns all
// zero fields, and model identity comes from normalized RPC metadata.
//
// Accumulated capacity wins over the delay threshold and uses the fixed
// capacity wait. Otherwise a delay at or above seven seconds selects model
// limiting; a shorter positive delay becomes a local retry raised to the
// one-second minimum. Missing or nonpositive parsed delays already use the
// thirty-second default, so incomplete delay data can select long-delay
// handling rather than a short retry.
func ClassifyAntigravitySmartRetry(native bool, respBody []byte) SmartRetryDecision {
	if !native {
		return SmartRetryDecision{}
	}

	info := ParseAntigravitySmartRetryInfo(respBody)
	if info == nil {
		return SmartRetryDecision{}
	}

	// Capacity wins over other accumulated reasons and parsed delay because its
	// recovery uses the independent fixed wait on the current provider.
	if info.IsModelCapacityExhausted {
		return SmartRetryDecision{
			Kind:         SmartRetryCapacity,
			WaitDuration: AntigravityModelCapacityRetryWait,
			ModelName:    info.ModelName,
		}
	}

	// Long provider-model limits request a cooldown and provider switch.
	// Missing upstream delays have already received the default duration.
	if info.RetryDelay >= AntigravityRateLimitThreshold {
		return SmartRetryDecision{
			Kind:         SmartRetryModelLimit,
			WaitDuration: info.RetryDelay,
			ModelName:    info.ModelName,
		}
	}

	// Raise short parsed delays to the local minimum before provider dispatch
	// so fractional supplier hints cannot form an immediate repeated request.
	waitDuration := max(info.RetryDelay, AntigravitySmartRetryMinWait)

	return SmartRetryDecision{
		Kind:         SmartRetryShort,
		WaitDuration: waitDuration,
		ModelName:    info.ModelName,
	}
}

// NormalizeAntigravityModelName trims, lowercases, and removes supported
// resource prefixes. Last Google publisher paths take precedence over
// Anthropic and generic model paths; otherwise the known leading prefixes are
// stripped in sequence.
func NormalizeAntigravityModelName(model string) string {
	normalized := strings.ToLower(strings.TrimSpace(model))
	if idx := strings.LastIndex(normalized, "/publishers/google/models/"); idx != -1 {
		normalized = normalized[idx+len("/publishers/google/models/"):]
	} else if idx := strings.LastIndex(normalized, "/publishers/anthropic/models/"); idx != -1 {
		normalized = normalized[idx+len("/publishers/anthropic/models/"):]
	} else if idx := strings.LastIndex(normalized, "/models/"); idx != -1 {
		normalized = normalized[idx+len("/models/"):]
	} else {
		normalized = strings.TrimPrefix(normalized, "publishers/google/models/")
		normalized = strings.TrimPrefix(normalized, "publishers/anthropic/models/")
		normalized = strings.TrimPrefix(normalized, "models/")
	}
	return normalized
}

// IsAntigravityInternalServerError reports the exact internal-500
// message/status combination when HTTP status is 500. gjson integer conversion
// reads error.code, so callers should not infer strict JSON numeric-type
// validation from this classifier.
func IsAntigravityInternalServerError(statusCode int, body []byte) bool {
	if statusCode != http.StatusInternalServerError {
		return false
	}
	return gjson.GetBytes(body, "error.code").Int() == 500 &&
		gjson.GetBytes(body, "error.message").String() == "Internal error encountered." &&
		gjson.GetBytes(body, "error.status").String() == "INTERNAL"
}
