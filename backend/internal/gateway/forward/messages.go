package forward

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// messages.go prepares and executes a Messages attempt and returns usage or
// classified failure evidence. A Messages attempt retains its client's logical
// model while preparing a provider-mapped, platform-normalized request. Every
// selected ordinary rewrite reparses shared request state before the next
// preparation step. Alternate emulation, API-key passthrough, and Bedrock paths
// return before this sequence; OAuth mimicry is gated by client-format evidence
// and controls system/cache and tool-name handling independently of those
// alternate paths.
//
// HTTP handling finishes before acceptance and response reading. Later stream
// errors combine body evidence, health policy, byte progress, and writer
// commitment: unchanged byte count can permit persisted observations, while an
// uncommitted writer is required for replacement output or failover. Ordinary
// reader failure may retain observed usage for completion when no failover
// signal was selected; classified stream events use their own handling path.

// Messages executes one selected-provider request and returns complete or
// eligible partial observations. A nonnil parsed request and configured attempt
// callbacks are required; alternate delegates return before ordinary Begin and
// preparation, and successful Begin supplies the deferred release callback.
//
// Ordinary rewrites update parsed request state in sequence while retaining the
// entry model for the result. The executor supplies serial hooks and owns body
// closure. HTTP errors stop before acceptance; classified stream errors use
// health plus writer commitment, and other stream failures retain positive usage
// only when the error does not carry a failover signal.
func Messages(ctx context.Context, p MessagePorts, in MessageInput, parsed *requeststate.ParsedRequest) (*Result, error) {
	startTime := time.Now()
	if parsed == nil {
		return nil, fmt.Errorf("parse request: empty request")
	}

	if in.ProviderPresent && p.ShouldEmulate(ctx, parsed.GroupID, parsed.Body.Bytes()) {
		return p.Emulate(ctx, parsed)
	}

	if in.ProviderPresent && in.Passthrough {
		passthroughBody := parsed.Body.Bytes()
		passthroughModel := parsed.Model
		if passthroughModel != "" {
			if mappedModel := p.MappedModel(passthroughModel); mappedModel != passthroughModel {
				passthroughBody = p.ReplaceModel(passthroughBody, mappedModel)
				p.Log(fmt.Sprintf("Passthrough model mapping: %s -> %s (provider: %s)", parsed.Model, mappedModel, in.ProviderName))
				passthroughModel = mappedModel
			}
		}
		// The passthrough delegate receives the selected local body together with
		// original parsed metadata. Its own preparation decides when filtered bytes
		// are republished, keeping this early mapping separate from ordinary rewrites.
		return p.Passthrough(ctx, PassthroughInput{Body: passthroughBody, Parsed: parsed, RequestModel: passthroughModel, OriginalModel: parsed.Model, Stream: parsed.Stream, StartedAt: startTime})
	}

	if in.ProviderPresent && in.Bedrock {
		return p.Bedrock(ctx, parsed, startTime)
	}

	done, err := p.Begin()
	if err != nil {
		return nil, err
	}
	// Keep the attempt registered through preparation, error policy, and
	// response reading. This deferred release covers every return after Begin
	// succeeds, including failures before any upstream exchange.
	defer done()
	if in.Platform == "anthropic" && in.HTTPPresent {
		if err := p.Beta(ctx, parsed.Model); err != nil {
			return nil, err
		}
	}

	// Reparse each selected body before following preparation steps use cached
	// request fields. ParsedRequest may already hold replacement bytes on parse
	// failure, so the error ends this attempt rather than implying rollback.
	body := parsed.Body.Bytes()
	replaceBody := func(next []byte) error {
		if err := parsed.ReplaceBody(next); err != nil {
			return fmt.Errorf("rewrite request body: %w", err)
		}
		body = parsed.Body.Bytes()
		return nil
	}
	reqModel := parsed.Model
	reqStream := parsed.Stream
	originalModel := reqModel

	if in.HTTPPresent {
		p.DebugOriginal(body, reqModel, reqStream)
	}

	// Provider mapping establishes the model used by mimic and cache preparation.
	// Platform normalization follows those steps and retains the entry model for
	// returned client/billing metadata.
	providerMappedModel := p.ProviderMappedModel(reqModel)
	if providerMappedModel != reqModel {
		if err := replaceBody(p.ReplaceModel(body, providerMappedModel)); err != nil {
			return nil, err
		}
		reqModel = providerMappedModel
		parsed.Model = providerMappedModel
		p.Log(fmt.Sprintf("Model mapping applied: %s -> %s (provider: %s, source=provider)", originalModel, providerMappedModel, in.ProviderName))
	}

	// Client-format evidence disables OAuth mimicry, retaining that client's
	// system/cache/tool preparation through this phase. Later shared cache limits
	// and model-specific compatibility filters still apply to the selected body.
	isClaudeCode := p.IsClaudeCode(ctx, body, parsed.MetadataUserID)

	shouldMimicClaudeCode := in.OAuth && !isClaudeCode

	if shouldMimicClaudeCode {
		systemRewritten := false
		systemPromptInjectionEnabled, systemPrompt, systemPromptBlocks := p.SystemSettings(ctx)
		// The enabled branch records an invoked rewrite even when the callback
		// returns unchanged bytes. This phase flag determines whether OAuth
		// normalization strips system markers in the subsequent cache handling.
		if systemPromptInjectionEnabled {
			if err := replaceBody(p.RewriteSystem(body, parsed, systemPrompt, systemPromptBlocks)); err != nil {
				return nil, err
			}
			systemRewritten = true
		}

		// Retain system markers after the enabled rewrite path; otherwise remove
		// incoming system markers during OAuth normalization. CacheLimit later
		// constrains the combined markers independently of this phase flag.
		normalizeOpts := NormalizeOptions{StripSystemCacheControl: !systemRewritten}
		if metadata := p.Metadata(ctx, parsed); metadata != "" {
			normalizeOpts.InjectMetadata = true
			normalizeOpts.MetadataUserID = metadata
		}

		normalizedBody := p.NormalizeOAuth(body, normalizeOpts)
		if err := replaceBody(normalizedBody); err != nil {
			return nil, err
		}

		if err := replaceBody(p.RewriteCache(ctx, body)); err != nil {
			return nil, err
		}
		// Publish adapted tool names before binding their response restoration
		// state. When no rewrite is found, the alternate tool-side breakpoint
		// operates on the normalized body without binding a new name mapping.
		if next, found := p.RewriteTools(body); found {
			if err := replaceBody(next); err != nil {
				return nil, err
			}
			p.BindTools()
		} else if err := replaceBody(p.ToolsLast(body)); err != nil {
			return nil, err
		}
	}

	// Dateline normalization is optional under provider/settings policy. Publish
	// only its reported change so unchanged requests continue with the existing
	// parsed body before total cache-marker enforcement.
	if next, ok := p.NormalizeDateline(ctx, body); ok {
		if err := replaceBody(next); err != nil {
			return nil, err
		}
	}

	if err := replaceBody(p.CacheLimit(body)); err != nil {
		return nil, err
	}

	// Resolve the platform wire name after provider-specific preparation. The
	// parsed model and outgoing JSON move together while originalModel remains
	// available for the result's logical client identity.
	mappedModel := p.PlatformModel(reqModel)
	if mappedModel != reqModel {
		if err := replaceBody(p.ReplaceModel(body, mappedModel)); err != nil {
			return nil, err
		}
		reqModel = mappedModel
		parsed.Model = mappedModel
		p.Log(fmt.Sprintf("Platform model normalization applied: %s -> %s (provider: %s)", providerMappedModel, mappedModel, in.ProviderName))
	}

	// TTL eligibility is resolved for this provider/request after model selection.
	// Its rewrite follows total marker enforcement and is republished before
	// credentials and exchange preparation read the selected body.
	if p.InjectTTL(ctx, reqModel) {
		if err := replaceBody(p.CacheTTL(body)); err != nil {
			return nil, err
		}
	}

	if err := p.Credential(ctx); err != nil {
		return nil, err
	}
	p.Transport()

	// Apply model-compatible empty-text, search-history, and thinking filters
	// before exchange. Reparse each selected body so later filters and optional
	// passback-mode normalization use the preceding transformation's request state.
	if err := replaceBody(protocolanthropic.StripEmptyTextBlocks(body)); err != nil {
		return nil, err
	}

	if err := replaceBody(p.FilterSearchHistory(body, reqModel)); err != nil {
		return nil, err
	}

	if err := replaceBody(p.FilterThinking(body, reqModel)); err != nil {
		return nil, err
	}
	if p.PassbackThinking(reqModel) {
		// Passback-required models can need a different thinking mode in request
		// JSON. Publish and log only a reported normalization before the executor
		// uses those bytes, retaining the filtered body on the unapplied path.
		if rewritten, applied := p.NormalizeThinking(body, reqModel); applied {
			if err := replaceBody(rewritten); err != nil {
				return nil, err
			}
			p.Log(fmt.Sprintf("Provider %d: rewrote thinking.type for %s", in.ProviderID, reqModel))
		}
	}

	var resp *ExchangeResponse
	lastWireBody := body
	var earlyResult *Result
	stopped := false
	var streamResult *StreamOutcome
	writerSizeBeforeStream := 0
	before := func(ctx context.Context, response *ExchangeResponse, wire []byte) (bool, error) {
		resp = response
		lastWireBody = wire
		early := func(result *Result, err error) (bool, error) { earlyResult = result; return true, err }
		if resp.StatusCode >= 400 && ShouldRetry(in.OAuth, resp.StatusCode) {
			if ShouldFailover(resp.StatusCode) {
				// Retain even partial read evidence for an eligible exhausted 403.
				// Reset supplies captured bytes to health, which can consume that
				// reader again before subsequent generic error handling rereads it.
				respBody, _ := p.ReadErrorBody()
				p.ResetErrorBody(respBody)

				// Bound this routine diagnostic independently of detailed-body policy.
				p.Log(fmt.Sprintf("[Forward] Upstream error (retry exhausted, failover): Provider=%d(%s) Status=%d RequestID=%s Body=%s",
					in.ProviderID, in.ProviderName, resp.StatusCode, resp.RequestID, p.Truncate(string(respBody), 1000)))

				decision := p.Health(ctx, "retry", resp.StatusCode, resp.Headers, respBody, reqModel)
				if decision.Generic {
					return early(p.HandleError(ctx, reqModel, false))
				}
				p.Observe(Notice{
					Platform:           in.Platform,
					ProviderID:         in.ProviderID,
					ProviderName:       in.ProviderName,
					UpstreamStatusCode: resp.StatusCode,
					UpstreamRequestID:  resp.RequestID,
					Kind:               "retry_exhausted_failover",
					Message:            upstream.ExtractErrorMessage(respBody),
					Detail: func() string {
						if in.LogErrorBody {
							return p.Truncate(string(respBody), in.LogErrorBodyMaxBytes)
						}
						return ""
					}(),
				})
				return true, p.FailoverError(resp.StatusCode, respBody, decision.RetrySameProvider)
			}
			return early(p.HandleError(ctx, reqModel, true))
		}

		// Status eligibility selects this HTTP recovery path unless health asks
		// for generic output. Failover itself remains subject to the surrounding
		// request loop's output state and remaining provider budget.
		if resp.StatusCode >= 400 && ShouldFailover(resp.StatusCode) {
			// Preserve captured partial bytes despite a read error; resetting makes
			// that evidence available for the policy reader before the selected
			// failover error retains the local byte slice.
			respBody, _ := p.ReadErrorBody()
			p.ResetErrorBody(respBody)

			// Bound routine failure logs separately from optional detailed observation.
			p.Log(fmt.Sprintf("[Forward] Upstream error (failover): Provider=%d(%s) Status=%d RequestID=%s Body=%s",
				in.ProviderID, in.ProviderName, resp.StatusCode, resp.RequestID, p.Truncate(string(respBody), 1000)))

			decision := p.Health(ctx, "failover", resp.StatusCode, resp.Headers, respBody, reqModel)
			if decision.Generic {
				return early(p.HandleError(ctx, reqModel, false))
			}
			p.Observe(Notice{
				Platform:           in.Platform,
				ProviderID:         in.ProviderID,
				UpstreamStatusCode: resp.StatusCode,
				UpstreamRequestID:  resp.RequestID,
				Kind:               "failover",
				Message:            upstream.ExtractErrorMessage(respBody),
				Detail: func() string {
					if in.LogErrorBody {
						return p.Truncate(string(respBody), in.LogErrorBodyMaxBytes)
					}
					return ""
				}(),
			})
			return true, p.FailoverError(resp.StatusCode, respBody, decision.RetrySameProvider)
		}
		if resp.StatusCode >= 400 {
			if resp.StatusCode == 400 && in.FailoverOn400 {
				respBody, readErr := p.ReadErrorBody()
				// Classification requires readable 400 evidence. Delegate a failed
				// read to ordinary error handling; this branch has not installed a
				// replacement reader, so the adapter determines any remaining bytes.
				if readErr != nil {
					return early(p.HandleError(ctx, reqModel, false))
				}
				p.ResetErrorBody(respBody)

				// Recover only the recognized 400 body class under the enabled setting.
				// Record a sanitized summary before health can select generic output
				// or permit a failover signal from the captured source evidence.
				if p.Failover400(respBody) {
					upstreamMsg := strings.TrimSpace(upstream.ExtractErrorMessage(respBody))
					upstreamMsg = p.Sanitize(upstreamMsg)
					upstreamDetail := ""
					if in.LogErrorBody {
						maxBytes := in.LogErrorBodyMaxBytes
						// A missing detail budget retains a bounded body excerpt.
						if maxBytes <= 0 {
							maxBytes = 2048
						}
						upstreamDetail = p.Truncate(string(respBody), maxBytes)
					}
					p.Observe(Notice{
						Platform:           in.Platform,
						ProviderID:         in.ProviderID,
						ProviderName:       in.ProviderName,
						UpstreamStatusCode: resp.StatusCode,
						UpstreamRequestID:  resp.RequestID,
						Kind:               "failover_on_400",
						Message:            upstreamMsg,
						Detail:             upstreamDetail,
					})

					if in.LogErrorBody {
						p.Log(fmt.Sprintf(
							"Provider %d: 400 error, attempting failover: %s",
							in.ProviderID,
							p.TruncateBytes(respBody, in.LogErrorBodyMaxBytes),
						))
					} else {
						p.Log(fmt.Sprintf("Provider %d: 400 error, attempting failover", in.ProviderID))
					}
					decision := p.Health(ctx, "failover", resp.StatusCode, resp.Headers, respBody, reqModel)
					if decision.Generic {
						return early(p.HandleError(ctx, reqModel, false))
					}
					return true, p.FailoverError(resp.StatusCode, respBody, decision.RetrySameProvider)
				}
			}
			return early(p.HandleError(ctx, reqModel, false))
		}

		// Successful exchange may select different retry/signature bytes. Publish
		// that wire body before acceptance so subsequent parsed-state consumers
		// and completion hooks describe the request that was actually selected.
		if !bytes.Equal(lastWireBody, body) {
			if err := replaceBody(lastWireBody); err != nil {
				return true, err
			}
		}

		return false, nil
	}
	attempt, err := p.Execute(ctx, MessageExecution{Model: reqModel, OriginalModel: originalModel, Stream: reqStream, Mimic: shouldMimicClaudeCode, StartedAt: startTime, Body: body, ReplaceBody: replaceBody, Hooks: MessageHooks{
		Before: func(ctx context.Context, response *ExchangeResponse, wire []byte) (bool, error) {
			var err error
			stopped, err = before(ctx, response, wire)
			return stopped, err
		},
		Wire: func(wire []byte) { lastWireBody = wire },
		Accepted: func() {
			if parsed.OnUpstreamAccepted != nil {
				parsed.OnUpstreamAccepted()
			}
		},
		BeforeStream: func() { writerSizeBeforeStream = p.Size() },
		Stream: func(result *StreamOutcome) {
			if result != nil {
				streamResult = result
			}
		},
	}})

	if stopped {
		return earlyResult, err
	}
	if reqStream {
		if err != nil {
			if raw, ok := p.StreamError(err); ok {
				body := []byte(raw)
				semanticStatus := 403
				var semanticDecision ErrorDecision
				// An overload event before any new stream bytes uses synthetic 529
				// health policy. Size excludes heartbeat traffic, while Written below
				// determines whether a plain JSON error can still be presented.
				if p.Size() == writerSizeBeforeStream && gjson.GetBytes(body, "error.type").String() == "overloaded_error" {
					semanticStatus = 529
					semanticDecision = p.Health(ctx, "failover_synthetic", semanticStatus, resp.Headers, body, reqModel)
				}
				upstreamMsg := p.Sanitize(strings.TrimSpace(upstream.ExtractErrorMessage(body)))

				upstreamDetail := ""
				if in.LogErrorBody {
					maxBytes := in.LogErrorBodyMaxBytes
					// Retain bounded detail when no positive budget was configured.
					if maxBytes <= 0 {
						maxBytes = 2048
					}
					upstreamDetail = p.Truncate(raw, maxBytes)
				}

				p.Observe(Notice{
					Platform:           in.Platform,
					ProviderID:         in.ProviderID,
					ProviderName:       in.ProviderName,
					UpstreamStatusCode: semanticStatus,
					UpstreamRequestID:  resp.RequestID,
					Kind:               "stream_error",
					Message:            upstreamMsg,
					Detail:             upstreamDetail,
				})

				// Bound routine stream-error logs separately from optional body detail.
				p.Log(fmt.Sprintf(
					"[Forward] SSE error event in stream: Provider=%d(%s) RequestID=%s Body=%s",
					in.ProviderID, in.ProviderName, resp.RequestID,
					p.Truncate(raw, 1000),
				))

				decision := semanticDecision
				// Other stream events receive volatile 403 treatment even after bytes
				// have progressed. Persist the observation only with unchanged size;
				// HTTP commitment still prevents the later plain JSON output path.
				if semanticStatus != 529 {
					decision = p.Health(ctx, "volatile", 403, resp.Headers, body, reqModel)
					if p.Size() == writerSizeBeforeStream {
						decision = p.Health(ctx, "persist", 403, resp.Headers, body, reqModel)
					}
				}
				// Plain JSON requires uncommitted HTTP headers. Provider recovery remains
				// possible after heartbeat-only commitment because adjusted size excludes
				// those bytes and no provider content has reached the client.
				if decision.Generic && !p.Written() {
					p.GenericError()
					return nil, fmt.Errorf("upstream SSE error not in custom error codes")
				}
				if p.Size() <= 0 && decision.Failover {
					return nil, p.FailoverError(semanticStatus, body, decision.RetrySameProvider)
				}
				return nil, err
			}
			// Ordinary reader failure can retain positive usage from the same attempt.
			// Failover suppresses that partial result, and requested speed comes from
			// selected wire bytes rather than the earlier logical preparation body.
			requestSpeed := gjson.GetBytes(lastWireBody, "speed").String()
			if partial := PartialUsage(resp, streamResult, originalModel, mappedModel, startTime, requestSpeed, p.IsFailover(err)); partial != nil {
				partial.UpstreamResponseModel = attempt.UpstreamResponseModel
				return partial, err
			}
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	usage := &attempt.Usage

	clientDisconnect := attempt.ClientDisconnect
	if resp == nil {
		return nil, err
	}
	return &Result{
		RequestID:                   resp.RequestID,
		UpstreamHeaders:             resp.Headers,
		Usage:                       *usage,
		Model:                       originalModel,
		UpstreamModel:               mappedModel,
		UpstreamResponseServiceTier: p.ServiceTier(),
		UpstreamResponseModel:       attempt.UpstreamResponseModel,
		Stream:                      reqStream,
		Duration:                    time.Since(startTime),
		Timings:                     nil,
		ClientDisconnect:            clientDisconnect,
	}, nil
}
