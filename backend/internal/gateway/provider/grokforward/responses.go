package grokforward

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// responses.go prepares and executes Grok Responses requests while retaining
// client tools and model identities.
// Grok Responses requests can carry client tools and cache metadata that need
// different vendor representations. Preparation keeps the caller model for
// output, maps a billing model to the upstream model, and records client-tool
// mappings so response readers can reconstruct tool shapes after the vendor
// exchange. Compact requests become summary turns before cache routing and
// credential resolution.
//
// Cache identity normally comes from the patched tool payload, but Claude Code
// session metadata requires the supplied body. Exchange recovery may replace
// the effective payload; error handling and final effort observation use those
// resulting bytes. Health policy decides generic presentation or provider-loop
// failover before any successful result is assembled.

// Forward executes a selected Grok Responses attempt and returns its completion
// data after successful reading. OAuth and API Key providers are accepted,
// while image models are rejected for this endpoint before transport. Client-
// tool mapping and Compact adaptation run before cache preparation and
// credential resolution.
//
// Cache identity uses the effective tool payload unless Claude Code session
// metadata requires the supplied input. ResponsesExecutor may replay decoding
// failures within this provider and returns the resulting body to the response
// hook. That hook preserves error bytes for presentation, records health
// decisions, and returns typed failures to the outer provider loop; the
// successful result keeps caller, billing, and vendor model identities
// separate.
func Forward(ctx context.Context, p Ports, o Options, in Input) (*forwardcore.OpenAIResult, error) {
	body, originalModel, reqStream, startTime := in.Body, in.OriginalModel, in.Stream, in.StartedAt

	if in.ProviderType != "oauth" && in.ProviderType != "apikey" {
		return nil, fmt.Errorf("grok provider type %s is not supported by Responses forwarding", in.ProviderType)
	}
	billingModel := p.BillingModel(originalModel)
	upstreamModel := p.UpstreamModel(billingModel)
	if o.ResolveAttributes != nil {
		attributes, err := o.ResolveAttributes(ctx, upstreamModel)
		if err != nil {
			return nil, err
		}
		o.Codec.Attributes = func(model string) modelcatalog.Attributes {
			if model == upstreamModel {
				return attributes
			}
			return modelcatalog.Attributes{}
		}
	}
	if o.Codec.Attributes != nil && o.Codec.Attributes(upstreamModel).OutputOnly("image") {
		err := fmt.Errorf("model %s is an image model and is not available on the Responses endpoint; use /v1/images/generations instead", upstreamModel)
		// Report the endpoint mismatch as 400 before the handler can convert it to a generic 502.
		if in.HTTPPresent {
			p.InvalidRequest(err.Error(), "model")
		}
		return nil, err
	}
	patchedBody, clientToolMapping, err := o.Codec.PatchGrokResponsesBodyWithClientTools(body, upstreamModel)
	if err != nil {
		p.SetError(http.StatusBadRequest, err.Error(), "")
		p.InvalidRequest(err.Error(), "tools")
		return nil, err
	}
	p.ClientTools(clientToolMapping)
	// Emulate Compact with a Responses summary turn and wrap the result as a compaction item.
	if in.Compact {
		patchedBody, err = o.Codec.BuildGrokCompactRequestBody(patchedBody)
		if err != nil {
			return nil, err
		}
	}
	// Derive cache identity from the upstream payload so additional_tools contributes
	// to the stable tool prefix. Use the original body when Claude Code session identity
	// would otherwise be lost during metadata stripping.
	cacheIdentityBody := patchedBody
	if grok.ExtractClaudeCodeSessionIDFromPayload(body) != "" {
		cacheIdentityBody = body
	}
	cacheIdentity := p.CacheIdentity(cacheIdentityBody, upstreamModel)
	mixedCacheIntentBody := append([]byte(nil), patchedBody...)
	patchedBody, err = grok.ApplyGrokResponsesCacheIdentity(patchedBody, body, cacheIdentity, in.OAuth)
	if err != nil {
		return nil, fmt.Errorf("apply grok prompt cache identity: %w", err)
	}
	// Add web_search and x_search to the Free OAuth client-tool route
	// to avoid routing it to the uncacheable build-free tier.
	patchedBody, err = p.FreeCacheRoute(patchedBody, mixedCacheIntentBody, cacheIdentity)
	if err != nil {
		return nil, fmt.Errorf("apply grok Free function-tool cache route: %w", err)
	}
	err = p.Credential(ctx)
	if err != nil {
		return nil, err
	}
	upstreamCtx, releaseUpstreamCtx := p.Detach(ctx)
	// Keep the detached context available throughout provider-local exchange
	// recovery and response reading; release its handle after all attempt
	// processing finishes.
	defer releaseUpstreamCtx()
	p.ResolveProxy()
	upstreamStart := time.Now()
	var handled bool
	var handledResult *forwardcore.OpenAIResult
	var handleErr error
	target := &grok.ResponsesTarget{
		ProviderID: in.ProviderID,
		Model:      upstreamModel,
		Enter:      o.Enter,
		Exchange: grok.ResponsesExchange{
			Build: func(body []byte) (*http.Request, error) {
				return p.Build(upstreamCtx, body, cacheIdentity, true)
			},
			Do: func(req *http.Request) (*http.Response, error) {
				return p.Do(req)
			},
			ReadError: p.ReadError,
			AfterExchange: func(err error) error {
				p.Latency(time.Since(upstreamStart).Milliseconds())
				if err != nil {
					return p.TransportError(ctx, err)
				}
				return nil
			},
			OnReplay: func() {
				p.ReplayNotice(cacheIdentity != "")
			},
		},
		BeforeResponse: func(resp *http.Response, body []byte) (bool, error) {
			patchedBody = body
			if resp.StatusCode >= 400 {
				handled = true
				respBody := p.ReadError(resp)
				// Replay captured bytes for shared error presentation. The executor's
				// deferred close now applies to this replacement; earlier body closure
				// depends on the exchange probe, because this hook does not close it.
				resp.Body = io.NopCloser(bytes.NewReader(respBody))
				upstreamMsg := p.ErrorMessage(respBody)
				if upstreamMsg == "" {
					upstreamMsg = fmt.Sprintf("xAI upstream returned status %d", resp.StatusCode)
				}
				decision := p.Health(ctx, resp.StatusCode, resp.Header, respBody, upstreamModel, true)
				kind := "http_error"
				if decision.Failover {
					kind = "failover"
				}
				p.Observe(Notice{
					Platform:           in.Platform,
					ProviderID:         in.ProviderID,
					ProviderName:       in.ProviderName,
					UpstreamStatusCode: resp.StatusCode,
					UpstreamRequestID:  firstNonEmpty(resp.Header.Get("x-request-id"), resp.Header.Get("xai-request-id")),
					Kind:               kind,
					Message:            upstreamMsg,
				})
				if decision.Generic {
					handledResult, handleErr = p.HandleError(ctx, resp, patchedBody, upstreamModel)
					return true, handleErr
				}
				// Quota and rate-limit evidence marks the selected team model, while the
				// predicate leaves capacity-only failures available for ordinary provider
				// selection.
				if p.ShouldMarkTeam(resp.StatusCode, respBody) {
					p.MarkTeam(upstreamModel)
				}
				// Return response-derived retry limits with the typed failover error so the
				// outer loop can decide provider reuse before switching targets.
				if kind == "failover" {
					retry := p.RetryMetadata(resp.StatusCode, respBody)
					return true, p.Failure(newFailure(resp, respBody, retry, decision.RetrySameProvider))
				}
				handledResult, handleErr = p.HandleError(ctx, resp, patchedBody, upstreamModel)
				return true, handleErr
			}
			p.ObserveSuccess(ctx, resp.Header, resp.StatusCode, upstreamModel)
			return false, nil
		},
		MaxLineSize: o.MaxLineSize,
		ClientTools: clientToolMapping,
		ReadResponse: func(resp *http.Response, input upstream.AttemptInput, _ upstream.OutputSink) (upstream.ResponsesObservation, error) {
			if input.Stream {
				return p.ReadStream(ctx, resp, startTime, originalModel, upstreamModel)
			}
			return p.ReadNonStream(ctx, resp, originalModel, upstreamModel)
		},
	}
	sink := p.Sink()
	nativeResult, err := (grok.ResponsesExecutor{}).Execute(upstreamCtx, upstream.AttemptInput{
		Protocol:      protocol.ProtocolOpenAIResponses,
		Body:          patchedBody,
		ResponseModel: originalModel,
		Stream:        reqStream,
		Target:        target,
	}, sink)
	if handled {
		return handledResult, err
	}
	if err != nil {
		return nil, err
	}
	usage := &protocolopenai.ForwardUsage{
		InputTokens:              nativeResult.Usage.InputTokens,
		ImageInputTokens:         nativeResult.ImageInputTokens,
		ImageCacheReadTokens:     nativeResult.ImageCacheReadTokens,
		OutputTokens:             nativeResult.Usage.OutputTokens,
		CacheCreationInputTokens: nativeResult.Usage.CacheCreationInputTokens,
		CacheReadInputTokens:     nativeResult.Usage.CacheReadInputTokens,
		ImageOutputTokens:        nativeResult.Usage.ImageOutputTokens,
	}

	responseID := nativeResult.ResponseID
	searchCount := nativeResult.SearchCount
	imageCount := nativeResult.ObservedImages
	imageOutputSizes := nativeResult.ImageOutputSizes
	reasoningEffort := p.Effort(patchedBody, originalModel)
	result := &forwardcore.OpenAIResult{
		RequestID:             nativeResult.RequestID,
		UpstreamResponseModel: nativeResult.UpstreamResponseModel,
		UpstreamHeaders:       nativeResult.UpstreamHeaders,
		ResponseID:            responseID,
		Usage:                 *usage,
		Model:                 originalModel,
		BillingModel:          billingModel,
		UpstreamModel:         upstreamModel,
		ReasoningEffort:       reasoningEffort,
		Stream:                reqStream,
		OpenAIWSMode:          false,
		ResponseHeaders:       nativeResult.UpstreamHeaders.Clone(),
		Duration:              time.Since(startTime),
		Timings:               nativeResult.Timings,
	}
	// Carry search and image counts into the result so settlement can apply
	// per-search and per-image prices.
	if searchCount > 0 {
		result.SearchCount = searchCount
	}
	if imageCount > 0 {
		result.ImageCount = imageCount
		result.ImageOutputSizes = imageOutputSizes
	}
	return result, nil
}
