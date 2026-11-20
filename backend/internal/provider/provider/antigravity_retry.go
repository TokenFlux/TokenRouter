package provider

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
)

// antigravity_retry.go binds Antigravity retry decisions to model windows,
// credit exhaustion, and provider health callbacks. Antigravity retries
// consult provider model windows using the final forwarded identity and, for
// Gemini names, its shared platform window. The remaining wait is the longest
// positive duration across those keys, so a short exact-model window cannot
// hide a longer shared window. Credits overages use their own exhaustion
// marker and can select a different recovery path.
//
// The adapter binds attempt-local callbacks to application health services.
// Custom policy handling preserves a recognized long model limit before
// consuming or rewriting the error, and successful model-window handling
// releases the sticky binding. Health callbacks can publish or mutate the
// borrowed record and notify Changed even when a write reports failure. The
// platform retry loop owns request/response sequencing; this adapter supplies
// its policy, transport selection and bounded diagnostic reads.

// AntigravityRetryRequest contains one attempt and its synchronous observation
// and health callbacks.
type AntigravityRetryRequest struct {
	// ModelStore receives retry model-window writes. Nil makes the health helper
	// report failure; Changed can still run after that attempt through Bind.
	ModelStore provider.AntigravityHealthStore
	// Do optionally overrides this attempt transport. Nil selects
	// AntigravityRetry.Do
	// with ProxyURL and the borrowed record ID/concurrency at callback time.
	Do func(*http.Request) (*http.Response, error)
	// LogConfig overrides the retry service logger selection when nonnil. Bind
	// reads
	// the selected callback once, capturing body-logging enablement and its
	// diagnostic byte limit.
	LogConfig func() (bool, int)
	// PolicyModelFallback supplies custom policy model matching when final
	// admission
	// returns an empty or whitespace-only model. It is used exactly as supplied.
	PolicyModelFallback string
	// Context is retained for synchronous health callbacks and passed to platform
	// retry work. Callers retain a usable attempt context through the loop.
	Context context.Context
	// Provider is required by Bind and remains borrowed by every callback. Callers
	// serialize its model-window, Extra and health mutations through the
	// complete attempt.
	Provider *provider.Record
	// Prefix: diagnostic message prefix; ProxyURL: selected proxy endpoint address.
	// AccessToken: OAuth bearer request credential; Action: upstream API
	// action name.
	// RequestedModel: pre-mapping request model spelling.
	Prefix, ProxyURL, AccessToken, Action, RequestedModel string
	// Body retains the supplied request bytes for platform retries. Callers keep
	// that storage stable while the returned input and its callbacks are used.
	Body []byte
	// Thinking selects the final Sonnet variant for window and policy keys when
	// supplied. Nil retains the mapped suffix for those admission checks.
	Thinking *bool
	// SingleProvider selects in-place platform recovery instead of switching for an
	// existing model window. Sticky labels resulting provider-switch errors so the
	// caller can distinguish the session binding used by this failed attempt.
	// SingleProvider: single-provider retry mode; Sticky: current
	// sticky-session flag.
	SingleProvider, Sticky bool
	// UserAgent overrides platform request headers when nonblank after trimming.
	// The selected value is applied after the platform creates each retry request.
	UserAgent string
	// Observe optionally receives synchronous upstream attempt observations. Bind
	// provides a callable wrapper even when this callback was omitted by the
	// caller.
	Observe func(antigravity.RetryObservation)
	// SetError optionally receives classified request failure presentation fields.
	// Its wrapper ignores the notification when absent, leaving platform retry
	// results available.
	SetError func(int, string, string)
	// HandleError is passed directly to platform handling and is required on
	// reached
	// error paths, including custom policy matches that were not consumed by
	// model handling.
	HandleError func(int, http.Header, []byte)
	// ClearSticky optionally releases this attempt binding after selected
	// model handling.
	// Bind installs a nil-safe wrapper for platform calls and preserves caller
	// ownership.
	ClearSticky func()
	// Changed optionally observes the borrowed record after model, credits or
	// internal-error
	// health callbacks. It can run after failed writes, so it does not confirm
	// persistence.
	Changed func(*provider.Record)
}

// AntigravityRetry binds the platform retry loop to provider health services.
type AntigravityRetry struct {
	// Health supplies model/credits windows and INTERNAL failure counters.
	// Configure it
	// and reached dependencies before using any health callback from the bound
	// retry loop.
	Health *provider.AntigravityHealth
	// Policy optionally classifies explicit provider errors. A nil value leaves
	// custom policy handling unconsumed so the platform loop continues its own
	// recovery.
	Policy *provider.HealthService
	// Do supplies shared transport when an attempt has no override. Its callbacks
	// receive the selected proxy and record ID/concurrency from the bound attempt.
	Do func(*http.Request, string, int64, int) (*http.Response, error)
	// BaseURL supplies the single platform endpoint when BaseURLs is nil.
	BaseURL func(*provider.Record) string
	// BaseURLs optionally supplies ordered forwarding endpoint candidates (such
	// as [daily, prod] for paid accounts in auto mode); nil falls back to BaseURL.
	BaseURLs func(*provider.Record) []string
	// BodyLimit is required for response-error reads and selected-provider probes.
	// The value is passed to LimitReader exactly; callers configure its
	// intended positive byte limit.
	BodyLimit func() int64
	// LogConfig supplies body logging when the attempt did not override it. Bind
	// reads it once; absent callbacks leave logging fields at their zero values.
	LogConfig func() (bool, int)
	// TruncateString truncates platform diagnostic strings when that logging
	// path is
	// reached. Configure it together with enabled body logging and its
	// selected byte limit.
	TruncateString func(string, int) string
	// SafeURL supplies a safe upstream URL diagnostic for platform observations.
	// Configure it before the retry loop reaches its URL logging or
	// observation path.
	SafeURL func(string) string
	// Capacity tracks shared MODEL_CAPACITY_EXHAUSTED cooldown deadlines across
	// attempts bound by this service; nil allocates a lazy instance-local tracker.
	Capacity        *antigravity.CapacityTracker
	capacityOnce    sync.Once                    // guards lazy defaultCapacity initialization
	defaultCapacity *antigravity.CapacityTracker // fallback tracker when Capacity is nil
}

// WithDo returns a shallow copy of s configured with do as its default
// transport callback while sharing s's initialized CapacityTracker without
// copying sync.Once state.
func (s *AntigravityRetry) WithDo(do func(*http.Request, string, int64, int) (*http.Response, error)) *AntigravityRetry {
	if s == nil {
		return &AntigravityRetry{Do: do}
	}
	return &AntigravityRetry{
		Health:         s.Health,
		Policy:         s.Policy,
		Do:             do,
		BaseURL:        s.BaseURL,
		BaseURLs:       s.BaseURLs,
		BodyLimit:      s.BodyLimit,
		LogConfig:      s.LogConfig,
		TruncateString: s.TruncateString,
		SafeURL:        s.SafeURL,
		Capacity:       s.capacityTracker(),
	}
}

// capacityTracker returns the configured Capacity tracker or initializes a
// service-scoped fallback tracker once.
func (s *AntigravityRetry) capacityTracker() *antigravity.CapacityTracker {
	if s.Capacity != nil {
		return s.Capacity
	}
	s.capacityOnce.Do(func() {
		s.defaultCapacity = antigravity.NewCapacityTracker()
	})
	return s.defaultCapacity
}

// Bind constructs platform input and callbacks for one Antigravity attempt.
// The provider record and body remain borrowed, and reached service dependencies
// must be configured. Attempt transport and logging can override service defaults;
// Changed, Observe, SetError and ClearSticky receive nil-safe notification
// wrappers.
//
// Each window callback applies one mapping, thinking choice and final admission,
// then consult exact and shared Gemini keys. Remaining time selects the longest
// positive window, while a separate credits marker controls overages eligibility.
// Health callbacks can mutate/publish the record before Changed runs; that
// notification occurs even after a failed model write, so it represents an
// attempted update rather than a persistence acknowledgement.
func (s *AntigravityRetry) Bind(p AntigravityRetryRequest) (*antigravity.RetryAdapter, antigravity.RetryInput) {
	value := p.Provider
	changed := func() {
		if p.Changed != nil {
			p.Changed(value)
		}
	}
	clearSticky := func() {
		if p.ClearSticky != nil {
			p.ClearSticky()
		}
	}
	input := antigravity.RetryInput{
		Ctx: p.Context, Prefix: p.Prefix, ProviderID: value.ID, ProviderName: value.Name,
		Native: value.Platform == provider.PlatformAntigravity, OveragesEnabled: value.IsOveragesEnabled(), SingleProvider: p.SingleProvider,
		AccessToken: p.AccessToken, Action: p.Action, Body: p.Body, RequestedModel: p.RequestedModel, IsStickySession: p.Sticky,
		CreditsExhausted: func() bool {
			reset := value.ModelRateLimitResetAt(provider.CreditsExhaustedKey)
			return reset != nil && time.Now().Before(*reset)
		},
		ModelLimited: func(_ context.Context, model string) bool {
			for _, key := range antigravityRequestLimitKeys(value, model, p.Thinking) {
				if reset := value.ModelRateLimitResetAt(key); reset != nil && time.Now().Before(*reset) {
					return true
				}
			}
			return false
		},
		// Exact and shared Gemini deadlines can overlap. Use the longest positive
		// remainder so the caller cannot retry while a different applicable
		// window is active.
		ModelRemaining: func(_ context.Context, model string) time.Duration {
			var remaining time.Duration
			for _, key := range antigravityRequestLimitKeys(value, model, p.Thinking) {
				if reset := value.ModelRateLimitResetAt(key); reset != nil {
					if left := time.Until(*reset); left > remaining {
						remaining = left
					}
				}
			}
			return remaining
		},
	}
	var baseURLs func() []string
	if s.BaseURLs != nil {
		baseURLs = func() []string { return s.BaseURLs(value) }
	}
	var baseURL func() string
	if s.BaseURL != nil {
		baseURL = func() string { return s.BaseURL(value) }
	}
	options := antigravity.RetryOptions{
		BaseURL:  baseURL,
		BaseURLs: baseURLs,
		Do: func(req *http.Request) (*http.Response, error) {
			if p.Do != nil {
				return p.Do(req)
			}
			return s.Do(req, p.ProxyURL, value.ID, value.Concurrency)
		},
		ApplyHeaders: func(req *http.Request) {
			if agent := strings.TrimSpace(p.UserAgent); req != nil && agent != "" {
				req.Header.Set("User-Agent", agent)
			}
		},
		TruncateString: s.TruncateString, TruncateForLog: logredact.TruncateLine, SafeURL: s.SafeURL,
		ReadErrorBody: func(resp *http.Response) []byte {
			if resp == nil || resp.Body == nil {
				return nil
			}
			// Retry classification can use bytes received before a read error.
			// Bound that
			// diagnostic read and retain its partial result; the platform owns
			// response closing.
			body, _ := io.ReadAll(io.LimitReader(resp.Body, s.BodyLimit()))
			return body
		},
		Observe: func(observation antigravity.RetryObservation) {
			if p.Observe != nil {
				p.Observe(observation)
			}
		},
		SetError: func(status int, message, detail string) {
			if p.SetError != nil {
				p.SetError(status, message, detail)
			}
		},
		HandleError: p.HandleError,
		ApplyErrorPolicy: func(status int, headers http.Header, body []byte) (bool, int, error) {
			return s.applyPolicy(p, status, headers, body)
		},
		SetModelLimits: func(model string, status int, reset time.Time, force bool) bool {
			result := s.Health.SetAntigravityModelRateLimits(p.Context, p.ModelStore, value, model, p.Prefix, status, reset, force)
			changed()
			return result
		},
		ClearSticky: clearSticky,
		CreditsModel: func(model string) string {
			return AntigravityCreditsModelKey(value, model, p.RequestedModel, p.Thinking)
		},
		ClearCredits:         func() { s.Health.ClearCreditsExhausted(p.Context, value); changed() },
		CreditsFailure:       func(model string, resp *http.Response, err error) { s.creditsFailure(p, model, resp, err); changed() },
		Internal500Exhausted: func() { s.Health.HandleInternal500RetryExhausted(p.Context, p.Prefix, value); changed() },
		ResetInternal500:     func() { s.Health.ResetInternal500Counter(p.Context, p.Prefix, value.ID) },
		Capacity:             s.capacityTracker(),
	}
	if p.LogConfig != nil {
		options.LogBody, options.LogMaxBytes = p.LogConfig()
	} else if s.LogConfig != nil {
		options.LogBody, options.LogMaxBytes = s.LogConfig()
	}
	return &antigravity.RetryAdapter{Options: options}, input
}

// applyPolicy reports whether configured provider policy consumed a platform
// failure.
// Nil Policy leaves the status unchanged and returns unhandled. It resolves the
// final model for policy matching, then uses PolicyModelFallback when that result
// is blank. Temporary exclusion returns a provider-switch error carrying the
// request model and sticky marker.
//
// Custom skipped and matched policies first preserve a recognized long model
// window. A successful model write returns the original status and bypasses the
// generic callback. Otherwise skipped policy supplies status 500, while a matched
// policy calls HandleError and returns the supplied status. Callers therefore
// distinguish consumed policy from the platform's built-in retry handling.
func (s *AntigravityRetry) applyPolicy(p AntigravityRetryRequest, status int, headers http.Header, body []byte) (bool, int, error) {
	if s.Policy == nil {
		return false, status, nil
	}
	model := FinalAntigravityModel(p.Provider, p.RequestedModel, p.Thinking)
	// Final admission can leave no policy model after whitelist rejection. Use
	// the caller-supplied matching name so configured error rules still receive
	// the model context for this attempt.
	if strings.TrimSpace(model) == "" {
		model = p.PolicyModelFallback
	}
	switch s.Policy.CheckErrorPolicy(p.Context, p.Provider, status, body, model, false) {
	case provider.ErrorPolicyCustomSkipped:
		if s.modelLimitBeforePolicy(p, status, body) {
			return true, status, nil
		}
		return true, http.StatusInternalServerError, nil
	case provider.ErrorPolicyCustomMatched:
		if s.modelLimitBeforePolicy(p, status, body) {
			return true, status, nil
		}
		p.HandleError(status, headers, body)
		return true, status, nil
	case provider.ErrorPolicyTempUnscheduled:
		s.Health.Info("temp_unschedulable_matched", "prefix", p.Prefix, "status_code", status, "provider_id", p.Provider.ID)
		return true, status, &antigravity.AntigravityProviderSwitchError{OriginalProviderID: p.Provider.ID, RateLimitedModel: p.RequestedModel, IsStickySession: p.Sticky}
	case provider.ErrorPolicyPoolBypassed:
		return false, status, nil
	}
	return false, status, nil
}

// modelLimitBeforePolicy attempts a native model window before custom policy
// consumes an error.
// It accepts native Antigravity 429/503 responses with a parsed long limit and
// nonblank model, excluding shared capacity. A successful health helper result
// notifies Changed and clears an available sticky binding; failed writes return
// false so custom policy handling can continue.
//
// An accepted window retains the parsed model identity when custom response
// handling would otherwise consume this failure before ordinary smart retry.
// The result requires at least one applicable key write, not every shared key.
func (s *AntigravityRetry) modelLimitBeforePolicy(p AntigravityRetryRequest, status int, body []byte) bool {
	if (status != http.StatusTooManyRequests && status != http.StatusServiceUnavailable) || p.Provider == nil || p.Provider.Platform != provider.PlatformAntigravity {
		return false
	}
	decision := antigravity.ClassifyAntigravitySmartRetry(true, body)
	if decision.Kind != antigravity.SmartRetryModelLimit || strings.TrimSpace(decision.ModelName) == "" {
		return false
	}
	// ClassifyAntigravitySmartRetry supplies a positive delay for long
	// model limits. Its current result cannot reach this defensive default
	// before the parsed model window is written.
	duration := decision.WaitDuration
	if duration <= 0 {
		duration = antigravity.AntigravityDefaultRateLimitDuration
	}
	reset := time.Now().Add(duration)
	// Preserve model scope before custom policy can consume this response. Notify
	// and release the binding only when at least one applicable model window
	// was written.
	if !s.Health.SetAntigravityModelRateLimits(p.Context, p.ModelStore, p.Provider, decision.ModelName, p.Prefix, status, reset, false) {
		return false
	}
	if p.Changed != nil {
		p.Changed(p.Provider)
	}
	if p.ClearSticky != nil {
		p.ClearSticky()
	}
	logging.LegacyPrintf("service.antigravity_gateway", "%s status=%d model_rate_limited_before_error_policy model=%s provider=%d reset_in=%v", p.Prefix, status, decision.ModelName, p.Provider.ID, duration)
	return true
}

// creditsFailure consumes a failed credits response and observes exhaustion
// evidence.
// It reads at most 64 KiB, retains partial bytes on read failure, and closes
// an available response body. Only the platform's exhaustion classifier can
// install the credits marker on a nonnil record; remaining failures are logged
// with a bounded redacted diagnostic.
func (s *AntigravityRetry) creditsFailure(p AntigravityRetryRequest, model string, resp *http.Response, err error) {
	var body []byte
	status := 0
	if resp != nil {
		status = resp.StatusCode
		if resp.Body != nil {
			// Exhaustion classification needs bounded diagnostic bytes from
			// this credits
			// attempt. Retain partial reads and close here because a failed
			// credits response
			// is consumed by this callback rather than returned to the request
			// caller.
			body, _ = io.ReadAll(io.LimitReader(resp.Body, 64<<10))
			_ = resp.Body.Close()
		}
	}
	if antigravity.ShouldMarkCreditsExhausted(resp, body, err) && p.Provider != nil {
		s.Health.SetCreditsExhausted(p.Context, p.Provider)
		logging.LegacyPrintf("service.antigravity_gateway", "%s credit_overages_failed model=%s provider=%d marked_exhausted=true status=%d body=%s", p.Prefix, model, p.Provider.ID, status, logredact.TruncateLine(body, 200))
		return
	}
	if p.Provider != nil {
		logging.LegacyPrintf("service.antigravity_gateway", "%s credit_overages_failed model=%s provider=%d marked_exhausted=false status=%d err=%v body=%s", p.Prefix, model, p.Provider.ID, status, err, logredact.TruncateLine(body, 200))
	}
}

// antigravityRequestLimitKeys resolves one final admitted identity before
// expanding window keys.
// An empty result after mapping, thinking and whitelist admission produces nil
// keys. The helper therefore consults the same admitted target used for the
// request's exact/shared model windows.
func antigravityRequestLimitKeys(value *provider.Record, model string, thinking *bool) []string {
	return AntigravityModelLimitKeys(FinalAntigravityModel(value, model, thinking))
}

// AntigravityModelLimitKeys returns the trimmed exact identity and any shared
// Gemini key.
// Blank input returns nil. Normalized Gemini-family names add antigravity:gemini
// after the original exact spelling; other names retain a single key. Callers
// use the complete set when deciding model-window eligibility or remaining wait.
func AntigravityModelLimitKeys(model string) []string {
	model = strings.TrimSpace(model)
	if model == "" {
		return nil
	}
	keys := []string{model}
	if strings.HasPrefix(antigravity.NormalizeAntigravityModelName(model), "gemini-") && model != "antigravity:gemini" {
		keys = append(keys, "antigravity:gemini")
	}
	return keys
}

// AntigravityPaidTier reports whether stored plan_type is a Pro or Ultra string.
// Comparison trims whitespace and ignores case. Nil records, missing credentials,
// nonstrings and other plan names return false for the caller's endpoint tier
// choice.
func AntigravityPaidTier(value *provider.Record) bool {
	if value == nil || value.Credentials == nil {
		return false
	}
	tier, ok := value.Credentials["plan_type"].(string)
	if !ok {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(tier)) {
	case "pro", "ultra":
		return true
	default:
		return false
	}
}

// AntigravityCreditsModelKey selects the identity used for a credits observation.
// A nonblank trimmed upstreamModel wins even without a record. Otherwise nil
// records return empty; available records try the final admitted request target,
// then the platform-normalized request spelling when final resolution is blank.
func AntigravityCreditsModelKey(value *provider.Record, upstreamModel, requestedModel string, thinking *bool) string {
	if key := strings.TrimSpace(upstreamModel); key != "" {
		return key
	}
	if value == nil {
		return ""
	}
	if key := FinalAntigravityModel(value, requestedModel, thinking); strings.TrimSpace(key) != "" {
		return key
	}
	// Credits observations still need a marker when final admission supplies
	// no key.
	// Use normalized request spelling for that observation, leaving admission
	// decisions with its caller.
	return antigravity.NormalizeAntigravityModelName(requestedModel)
}
