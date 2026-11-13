package gemini

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"

	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
)

// exchange_messages.go executes Gemini requests and coordinates signature
// repair with provider retry policy. Messages compatibility can first discard
// thinking history and then tool history after signature-related failures.
// Google retries share a total attempt budget with these repairs. Providers
// using an external retry budget retain the two repair stages inside their
// current attempt, while ordinary HTTP failures return to the caller.
//
// External provider policy runs before signature repair, allowing configured
// error rules to stop an attempt. Each discarded response body is captured and
// closed before replacement or another dispatch. Selected responses retain a
// readable body and their request-ID header name for final client conversion.
// Backoff waits stop when the request context is cancelled, and cancellation
// errors propagate without creating a client error response or another request.

// ExchangeNotice identifies an upstream exchange observation and its selected
// diagnostic fields.
type ExchangeNotice struct {
	// Platform identifies the selected platform; ProviderName identifies its
	// display name.
	Platform, ProviderName string
	ProviderID             int64 // selected provider audit identifier
	UpstreamStatusCode     int   // upstream HTTP status; zero for transport failure
	// UpstreamRequestID identifies the server request; Kind classifies
	// observation; Message supplies public-safe text; Detail supplies diagnostic
	// text.
	UpstreamRequestID, Kind, Message, Detail string
}

// ExchangeOptions supplies synchronous construction, retry policy,
// diagnostics, and body-recovery callbacks.
type ExchangeOptions struct {
	ProviderID int64 // selected provider audit identifier
	// ProviderName labels provider; Platform labels platform; RequestIDHeader
	// selects the initial request-ID header.
	ProviderName, Platform, RequestIDHeader string
	// MaxRetries is the local attempt count when ExternalRetry is false. Zero
	// performs no exchange and returns a result with no response.
	MaxRetries int // total request attempt budget
	// ExternalRetry selects one ordinary attempt and leaves retryable HTTP or
	// transport failures to the caller. Messages retains two signature repairs.
	ExternalRetry bool // caller-owned ordinary retry policy
	// CountFallback enables an estimate after native retry exhaustion.
	// EstimateCount is required when this flag is used, and the estimate remains
	// separate from usage.
	CountFallback bool // native token estimate selection
	// Build and Do are required on every attempt. Other callback fields are
	// called synchronously on their applicable error, policy, throttle, or
	// recovery path and callers supply those used by their selected exchange.
	// request and ID-header construction
	Build func(context.Context) (*http.Request, string, error)
	// Do returns a nonnil response and body on success. It owns cleanup for any
	// response returned together with an error because these retry branches do
	// not close that pair.
	// upstream HTTP exchange callback
	Do            func(*http.Request) (*http.Response, error)
	BuildError    func(error) error  // request construction error mapping
	FinalError    func(string) error // terminal transport error mapping
	EstimateCount func() int         // native fallback token estimation
	// error response body consumption
	ReadError func(*http.Response) []byte
	// CheckPolicy returns the readable response used for subsequent status
	// handling, including a nonnil replacement when matched. It owns any original
	// stream consumed or replaced during evaluation.
	// error-policy evaluation and response replacement
	CheckPolicy func(context.Context, *http.Response) (bool, *http.Response)
	ShouldRetry func(int) bool // HTTP status retry eligibility
	// early throttle observation callback
	OnStatus func(context.Context, int, http.Header, []byte)
	// exchange diagnostic observation callback
	Observe  func(ExchangeNotice)
	SetError func(int, string, string) // terminal error state callback
	// Message extracts error text; Detail extracts diagnostic body text.
	Message, Detail func([]byte) string
	Sanitize        func(string) string // diagnostic text redaction callback
	// FilterThinking and FilterTools supply the two Messages recovery stages in
	// that order. ReplaceBody publishes their successfully converted result for
	// the next Build call; callers serialize this evolving request state.
	// FilterThinking downgrades thinking history; FilterTools downgrades thinking
	// and tool history.
	FilterThinking, FilterTools func() []byte
	// ConvertRetry optionally overrides Messages recovery conversion. Nil selects
	// the native Claude-to-Gemini bridge with the dummy thought-signature option
	// for the downgraded request.
	// downgraded request conversion callback
	ConvertRetry func([]byte) ([]byte, error)
	ReplaceBody  func([]byte) // subsequent request body replacement
	// RepairToolReplay replaces OpenAI-compatible history with its legacy
	// representation after a signature rejection. True permits one repair.
	RepairToolReplay func() bool
}

// ExchangeResult carries a selected response or a separate native token
// estimate.
type ExchangeResult struct {
	CandidateCount int // final request candidate count

	// Response and EstimatedTokens represent alternative outcomes. A nonnil
	// response body belongs to the caller; a native estimate outcome has no
	// response body to close.
	Response        *http.Response // caller-owned selected HTTP response
	RequestIDHeader string         // selected upstream ID-header name
	EstimatedTokens *int           // independent native token estimate
}

// ExchangeMessages returns the selected response and request-ID header after
// permitted retry and signature repair. Callers supply the callbacks used by
// their selected paths and close the returned body.
//
// Google signature repairs consume its local attempt budget. ExternalRetry
// keeps two bounded repairs inside the current provider attempt and evaluates
// provider policy before either repair. Each repair closes its captured error
// body before rebuilding the payload. Cancellation ends backoff and request
// execution immediately; exhausted repairs retain readable error evidence for
// the caller's final policy and protocol output.
func ExchangeMessages(ctx context.Context, options ExchangeOptions) (ExchangeResult, error) {
	maxAttempts := options.MaxRetries
	// External retry ownership limits this exchange to one ordinary attempt.
	// Protocol repairs remain bounded separately so they cannot expand provider retries.
	if options.ExternalRetry {
		maxAttempts = 1
	}
	requestIDHeader := options.RequestIDHeader
	var resp *http.Response
	candidateCount := 1
	signatureRetryStage := 0
	// Rebuild the request and its ID-header selection for every attempt,
	// including after caller body replacement. Captured HTTP error bytes survive
	// stream closing before another request or final error mapping.
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return ExchangeResult{}, err
		}
		upstreamReq, idHeader, err := options.Build(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ExchangeResult{}, ctx.Err()
			}
			return ExchangeResult{}, options.BuildError(err)
		}
		requestIDHeader = idHeader
		candidateCount = requestCandidateCount(upstreamReq)

		resp, err = options.Do(upstreamReq)
		if err != nil {
			// Caller cancellation ends the exchange. Transport-local deadlines leave
			// the caller alive and must reach retry or final-error classification.
			if ctx.Err() != nil {
				return ExchangeResult{}, ctx.Err()
			}
			safeErr := options.Sanitize(err.Error())
			options.Observe(ExchangeNotice{
				Platform:           options.Platform,
				ProviderID:         options.ProviderID,
				ProviderName:       options.ProviderName,
				UpstreamStatusCode: 0,
				Kind:               "request_error",
				Message:            safeErr,
			})
			// Transport failures share the total attempt budget with HTTP retries;
			// wait before rebuilding so repeated failures cannot form an immediate
			// dispatch loop.
			if attempt < maxAttempts {
				logger.LegacyPrintf("service.gemini_messages_compat", "Gemini provider %d: upstream request failed, retry %d/%d: %v", options.ProviderID, attempt, maxAttempts, err)
				if !SleepGeminiBackoff(ctx, attempt) {
					return ExchangeResult{}, ctx.Err()
				}
				continue
			}
			options.SetError(0, safeErr, "")
			return ExchangeResult{}, options.FinalError("Upstream request failed after retries: " + safeErr)
		}

		// External provider policies take precedence over protocol repair. A custom
		// status or pool rule can therefore return the original failure immediately.
		if options.ExternalRetry {
			matched, rebuilt := options.CheckPolicy(ctx, resp)
			resp = rebuilt
			if matched {
				break
			}
		}

		// Google signature repair precedes general policy; external provider policy
		// has already run. Eligible failures rebuild progressively downgraded Claude
		// history within the selected retry budget.
		if resp.StatusCode == http.StatusBadRequest && signatureRetryStage < 2 && (options.ExternalRetry || attempt < maxAttempts) {
			respBody := options.ReadError(resp)
			_ = resp.Body.Close()

			if IsGeminiSignatureRelatedError(respBody) {
				upstreamReqID := resp.Header.Get(requestIDHeader)
				if upstreamReqID == "" {
					upstreamReqID = resp.Header.Get("x-goog-request-id")
				}
				upstreamMsg := strings.TrimSpace(options.Message(respBody))
				upstreamMsg = options.Sanitize(upstreamMsg)
				upstreamDetail := options.Detail(respBody)
				options.Observe(ExchangeNotice{
					Platform:           options.Platform,
					ProviderID:         options.ProviderID,
					ProviderName:       options.ProviderName,
					UpstreamStatusCode: resp.StatusCode,
					UpstreamRequestID:  upstreamReqID,
					Kind:               "signature_error",
					Message:            upstreamMsg,
					Detail:             upstreamDetail,
				})

				var strippedClaudeBody []byte
				stageName := ""
				switch signatureRetryStage {
				case 0:
					// Start with thinking-only downgrade so tool calls retain their
					// structured representation unless a later signature response requires
					// the broader recovery stage.
					strippedClaudeBody = options.FilterThinking()
					stageName = "thinking-only"
					signatureRetryStage = 1
				default:
					// A subsequent signature failure selects the broader tool-history
					// downgrade from caller state, trading structured tool replay for a
					// request the recovery converter can send.
					strippedClaudeBody = options.FilterTools()
					stageName = "thinking+tools"
					signatureRetryStage = 2
				}
				convert := options.ConvertRetry
				if convert == nil {
					convert = func(body []byte) ([]byte, error) {
						return bridge.NativeConvertClaudeMessagesToGeminiGenerateContent(bridge.NativeGeminiOptions{DummyThoughtSignature: "skip_thought_signature_validator"}, body)
					}
				}
				retryGeminiReq, txErr := convert(strippedClaudeBody)
				if txErr == nil {
					logger.LegacyPrintf("service.gemini_messages_compat", "Gemini provider %d: detected signature-related 400, retrying with downgraded Claude blocks (%s)", options.ProviderID, stageName)
					options.ReplaceBody(retryGeminiReq)
					// External budgets count ordinary attempts, while the two-stage signature
					// counter bounds protocol repair even when this attempt counter is reused.
					if options.ExternalRetry {
						attempt--
					}
					// Publish the converted body for the next Build call and consume this
					// attempt slot for Google. External provider attempts keep signature
					// repair bounded by its own stage counter before returning ordinary failures.
					if !SleepGeminiBackoff(ctx, 1) {
						return ExchangeResult{}, ctx.Err()
					}
					continue
				}
			}

			// Rebuild the consumed 400 body when recovery is ineligible or conversion
			// fails so caller error mapping can inspect the captured response text.
			resp = &http.Response{
				StatusCode: http.StatusBadRequest,
				Header:     resp.Header.Clone(),
				Body:       io.NopCloser(bytes.NewReader(respBody)),
			}
			break
		}

		// Use the policy-selected response before ordinary status retries so matched
		// administrator rules stop this exchange with the response shape they
		// produced.
		matched, rebuilt := options.CheckPolicy(ctx, resp)
		resp = rebuilt
		if matched {
			break
		}

		if resp.StatusCode >= 400 && !options.ExternalRetry && options.ShouldRetry(resp.StatusCode) {
			respBody := options.ReadError(resp)
			_ = resp.Body.Close()
			// A scope-rejected 403 requires authorization changes. Rebuild its captured
			// body and return it instead of spending another delay on the same
			// credential scope.
			if resp.StatusCode == http.StatusForbidden && IsGeminiInsufficientScope(resp.Header, respBody) {
				resp = &http.Response{
					StatusCode: resp.StatusCode,
					Header:     resp.Header.Clone(),
					Body:       io.NopCloser(bytes.NewReader(respBody)),
				}
				break
			}
			if resp.StatusCode == http.StatusTooManyRequests {
				// Report throttling before sleeping so the caller can publish the cooldown
				// observation while this attempt is still waiting to retry.
				options.OnStatus(ctx, resp.StatusCode, resp.Header, respBody)
			}
			// Retry this classified HTTP failure while attempt slots remain; its body
			// is already captured and closed before waiting and constructing another
			// request.
			if attempt < maxAttempts {
				upstreamReqID := resp.Header.Get(requestIDHeader)
				if upstreamReqID == "" {
					upstreamReqID = resp.Header.Get("x-goog-request-id")
				}
				upstreamMsg := strings.TrimSpace(options.Message(respBody))
				upstreamMsg = options.Sanitize(upstreamMsg)
				upstreamDetail := options.Detail(respBody)
				options.Observe(ExchangeNotice{
					Platform:           options.Platform,
					ProviderID:         options.ProviderID,
					ProviderName:       options.ProviderName,
					UpstreamStatusCode: resp.StatusCode,
					UpstreamRequestID:  upstreamReqID,
					Kind:               "retry",
					Message:            upstreamMsg,
					Detail:             upstreamDetail,
				})

				logger.LegacyPrintf("service.gemini_messages_compat", "Gemini provider %d: upstream status %d, retry %d/%d", options.ProviderID, resp.StatusCode, attempt, maxAttempts)
				if !SleepGeminiBackoff(ctx, attempt) {
					return ExchangeResult{}, ctx.Err()
				}
				continue
			}
			// Return a readable copy after exhausting retries because the original
			// error response body was consumed and closed during retry classification.
			resp = &http.Response{
				StatusCode: resp.StatusCode,
				Header:     resp.Header.Clone(),
				Body:       io.NopCloser(bytes.NewReader(respBody)),
			}
			break
		}

		break
	}

	return ExchangeResult{Response: resp, RequestIDHeader: requestIDHeader, CandidateCount: candidateCount}, nil
}
