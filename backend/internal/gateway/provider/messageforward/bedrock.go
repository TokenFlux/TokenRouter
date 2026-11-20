package messageforward

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/bedrock"
)

// bedrock.go prepares Bedrock Messages routes, authentication, beta policy,
// and response handling.
// Bedrock model routing selects a source region and an exact upstream
// identifier before attributes or credentials are prepared. Requests use either
// an API key or a SigV4 signer, while the executor keeps the selected route
// through request exchange and response adaptation.
//
// Beta policy checks the client header before platform translation, then checks
// tokens inferred from the translated features before filtering the result.
// Optional group compatibility rewrites the body and its beta header together.
// HTTP failures return through the Messages health and error adapter after the
// platform retry budget, retaining the requested client model separately from
// the mapped Bedrock model in successful completion metadata.

// bedrock prepares the resolved AWS route, model attributes, beta tokens, and
// credentials for one Messages attempt. The executor owns successful response
// cleanup; HTTP failures are classified by the runtime before result
// conversion.
func (r *Runtime) bedrock(
	ctx context.Context,
	output HTTPBoundary, state *AttemptState,
	provider *gatewayprovider.ExecutionProvider,
	parsed *requeststate.ParsedRequest,
	startTime time.Time,
) (*forward.Result, error) {
	reqModel := parsed.Model
	reqStream := parsed.Stream
	body := parsed.Body.Bytes()

	route, err := gatewayprovider.ExecutionModelPolicy(provider).WithContext(ctx).BedrockRoute(reqModel)
	if err != nil {
		logging.LegacyPrintf("service.gateway", "[Bedrock] %s", bedrock.BedrockRoutingDiagnostic(err))
		return nil, err
	}
	region, mappedModel := route.SourceRegion, route.ModelID
	attributes, attributesErr := gatewayprovider.ResolveModelAttributes(ctx, provider, mappedModel, r.dependencies.Attributes, "anthropic_messages")
	if attributesErr != nil {
		return nil, attributesErr
	}
	body, err = gatewayprovider.ApplyModelAttributeParameters(body, attributes)
	if err != nil {
		return nil, err
	}
	body = stripAnthropicWireEffortIfMatched(ctx, provider, body, reqModel)
	// Rewrite ephemeral cache lifetimes before Bedrock sanitization so Claude 4.5+
	// routes retain 1h while older non-TTL Bedrock models strip unsupported TTL fields.
	if gatewayprovider.ShouldForceClaude1hCache(ctx, provider, mappedModel, reqModel) {
		body = anthropic.InjectAnthropicCacheControlTTL1h(body)
	}
	if mappedModel != reqModel {
		logging.LegacyPrintf("service.gateway", "[Bedrock] Model mapping: %s -> %s (provider: %s)", reqModel, mappedModel, provider.Record.Name)
	}

	betaHeader := ""
	if output.RequestPresent() {
		betaHeader = output.RequestHeaders().Get("anthropic-beta")
	}

	// Resolve beta policy before translating the body so blocked client
	// tokens and inferred platform tokens are checked before the Bedrock
	// payload is sent.
	betaTokens, err := r.bedrockBetaTokens(ctx, provider, betaHeader, body, mappedModel, attributes.Rules)
	if err != nil {
		return nil, err
	}

	bedrockBody, err := bedrock.PrepareBedrockRequestBodyWithTokens(body, mappedModel, betaTokens, false, attributes.Rules)
	if err != nil {
		return nil, fmt.Errorf("prepare bedrock request body: %w", err)
	}

	proxyURL := ""
	if provider.Record.ProxyID != nil && provider.Record.Proxy != nil {
		proxyURL = provider.Record.Proxy.URL()
	}

	logging.LegacyPrintf("service.gateway", "[Bedrock] Selected Bedrock forwarding: provider=%d name=%s model=%s->%s stream=%v",
		provider.Record.ID, provider.Record.Name, reqModel, mappedModel, reqStream)

	var signer *bedrock.BedrockSigner
	var bedrockAPIKey string
	if provider.View().IsBedrockAPIKey() {
		bedrockAPIKey = provider.View().GetCredential("api_key")
		if bedrockAPIKey == "" {
			return nil, fmt.Errorf("api_key not found in bedrock credentials")
		}
	} else {
		signer, err = provideradapter.NewBedrockSignerFromProvider(gatewayprovider.ExecutionRecord(provider))
		if err != nil {
			return nil, fmt.Errorf("create bedrock signer: %w", err)
		}
	}

	options, policy := r.bedrockOptions(ctx, output, state, provider, mappedModel, region, reqStream, signer, bedrockAPIKey, proxyURL)
	streamOptions := bedrock.StreamOptions{ProviderID: provider.Record.ID}
	if r.options.StreamInterval > 0 {
		streamOptions.Interval = r.options.StreamInterval
	}
	if r.dependencies.Health != nil {
		streamOptions.OnTimeout = func(ctx context.Context, model string) {
			r.dependencies.Health.Core.HandleStreamTimeout(ctx, gatewayprovider.ExecutionRecord(provider), model)
		}
	}
	readLimit := r.options.ResponseReadLimit
	hadHTTPError := false
	var errorResult *forward.Result
	target := &bedrock.Target{ProviderID: provider.Record.ID, Request: options, Retry: policy, Stream: streamOptions, StartedAt: startTime, Enter: r.dependencies.Enter, Accepted: parsed.OnUpstreamAccepted, ReadBody: func(r io.Reader) ([]byte, error) {
		return output.ReadResponseBody(r, readLimit, MessagesBody)
	}, HTTPError: func(ctx context.Context, resp *http.Response) (upstream.AttemptResult, error) {
		hadHTTPError = true
		var err error
		errorResult, err = r.bedrockError(ctx, resp, output, state, provider, mappedModel)
		return upstream.AttemptResult{}, err
	}}
	result, err := (bedrock.Executor{}).Execute(ctx, upstream.AttemptInput{Protocol: protocol.ProtocolAnthropicMessages, Body: bedrockBody, Stream: reqStream, ResponseModel: reqModel, Target: target}, output.Sink())
	if hadHTTPError {
		return errorResult, err
	}
	if err != nil {
		return nil, err
	}
	message := forward.MessagesFromAttempt(result)
	converted := (*forward.Result)(message)
	converted.UpstreamHeaders = result.UpstreamHeaders
	return converted, nil
}

// bedrockError selects outer replay or client presentation after a Bedrock HTTP
// failure. Retry-eligible statuses use exhausted-retry health policy, while
// other failover statuses use ordinary health policy. A generic provider
// decision takes precedence over the status-based failover choice.
//
// The failover branches retain captured bytes for the returned error and
// rebuild the reader before health inspection. Health consumes that rebuilt
// reader, so a subsequent handleError call receives its remaining bytes. The
// executor owns response cleanup after this callback returns.
func (r *Runtime) bedrockError(
	ctx context.Context,
	resp *http.Response,
	output HTTPBoundary, state *AttemptState,
	provider *gatewayprovider.ExecutionProvider,
	mappedModel string,
) (*forward.Result, error) {
	// Retain error bytes for the failover signal and rebuild the reader for
	// health inspection after retry exhaustion. Health can consume that
	// reader before generic presentation reads it again.
	if forward.ShouldRetry(provider.View().IsOAuth(), resp.StatusCode) {
		if forward.ShouldFailover(resp.StatusCode) {
			respBody, _ := r.readErrorBody(resp)
			_ = resp.Body.Close()
			resp.Body = io.NopCloser(bytes.NewReader(respBody))

			logging.LegacyPrintf("service.gateway", "[Bedrock] Upstream error (retry exhausted, failover): Provider=%d(%s) Status=%d Body=%s",
				provider.Record.ID, provider.Record.Name, resp.StatusCode, logredact.TruncateUTF8(string(respBody), 1000))

			decision := r.retryHealth(ctx, resp, provider, mappedModel)
			if decision.ShouldReturnGenericError() {
				return r.handleError(ctx, output, state, provider, resp, false, mappedModel)
			}
			output.Observe(forward.Notice{
				Platform:           provider.Record.Platform,
				ProviderID:         provider.Record.ID,
				ProviderName:       provider.Record.Name,
				UpstreamStatusCode: resp.StatusCode,
				Kind:               "retry_exhausted_failover",
				Message:            upstream.ExtractErrorMessage(respBody),
			})
			return nil, &forward.UpstreamFailoverError{
				StatusCode:              resp.StatusCode,
				ResponseBody:            respBody,
				RetryableOnSameProvider: decision.RetryableOnSameProvider(gatewayprovider.ExecutionErrorPolicy(provider), resp.StatusCode),
			}
		}
		return r.handleError(ctx, output, state, provider, resp, true, mappedModel)
	}

	// Capture this nonretry status for outer replay and rebuild its reader
	// for failoverHealth. A generic decision then passes the reader's
	// remaining bytes to handleError.
	if forward.ShouldFailover(resp.StatusCode) {
		respBody, _ := r.readErrorBody(resp)
		_ = resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(respBody))

		decision := r.failoverHealth(ctx, resp, provider, mappedModel)
		if decision.ShouldReturnGenericError() {
			return r.handleError(ctx, output, state, provider, resp, false, mappedModel)
		}
		output.Observe(forward.Notice{
			Platform:           provider.Record.Platform,
			ProviderID:         provider.Record.ID,
			ProviderName:       provider.Record.Name,
			UpstreamStatusCode: resp.StatusCode,
			Kind:               "failover",
			Message:            upstream.ExtractErrorMessage(respBody),
		})
		return nil, &forward.UpstreamFailoverError{
			StatusCode:              resp.StatusCode,
			ResponseBody:            respBody,
			RetryableOnSameProvider: decision.RetryableOnSameProvider(gatewayprovider.ExecutionErrorPolicy(provider), resp.StatusCode),
		}
	}

	return r.handleError(ctx, output, state, provider, resp, false, mappedModel)
}

// bedrockOptions binds AWS request credentials and transport to bounded retry
// callbacks for this provider. Diagnostics use sanitized URLs and optional body
// detail, while the upstream executor implements retry timing and response
// closure.
func (r *Runtime) bedrockOptions(ctx context.Context, output HTTPBoundary, state *AttemptState, provider *gatewayprovider.ExecutionProvider, modelID, region string, stream bool, signer *bedrock.BedrockSigner, apiKey, proxyURL string) (bedrock.RequestOptions, bedrock.RetryPolicy) {
	options := bedrock.RequestOptions{ModelID: modelID, Region: region, Stream: stream, Signer: signer, APIKey: apiKey, APIKeyMode: provider.View().IsBedrockAPIKey(), Do: func(req *http.Request) (*http.Response, error) {
		return r.dependencies.Transport.DoWithTLS(req, proxyURL, provider.Record.ID, provider.Record.Concurrency, nil)
	}}
	policy := bedrock.RetryPolicy{MaxAttempts: maxRetryAttempts, MaxElapsed: maxRetryElapsed, Delay: forward.RetryDelay, ShouldRetry: func(status int) bool { return forward.ShouldRetry(provider.View().IsOAuth(), status) }, ReadErrorBody: r.readErrorBody, TransportError: func(err error, url string) error {
		return r.transportError(ctx, output, provider, err, forward.Notice{UpstreamURL: logredact.SafeUpstreamURL(url)})
	}, ObserveRetry: func(resp *http.Response, body []byte, url string, attempt int, delay time.Duration) {
		detail := ""
		if r.options.LogErrorBody {
			detail = logredact.TruncateUTF8(string(body), r.options.LogErrorBodyMaxBytes)
		}
		output.Observe(forward.Notice{Platform: provider.Record.Platform, ProviderID: provider.Record.ID, ProviderName: provider.Record.Name, UpstreamStatusCode: resp.StatusCode, UpstreamURL: logredact.SafeUpstreamURL(url), Kind: "retry", Message: upstream.ExtractErrorMessage(body), Detail: detail})
		logging.LegacyPrintf("service.gateway", "[Bedrock] provider %d: upstream error %d, retry %d/%d after %v", provider.Record.ID, resp.StatusCode, attempt, maxRetryAttempts, delay)
	}}
	return options, policy
}

// bedrockBetaTokens returns platform beta tokens admitted by the current header
// and feature policy. Client header blocks are checked before translation,
// allowing the policy to recognize the client spelling.
//
// Bedrock token resolution can translate a beta or infer one from a tool in the
// body. The resolved token set is checked again for blocks before filtering, so
// a body-implied feature receives the same policy check as a header feature and
// the final removal set applies to platform spelling.
func (r *Runtime) bedrockBetaTokens(ctx context.Context, target *gatewayprovider.ExecutionProvider, header string, body []byte, model string, rules ...modelrules.ExecutionRules) ([]string, error) {
	policy := r.evaluateBeta(ctx, target, header, model)
	if policy.BlockErr != nil {
		return nil, policy.BlockErr
	}
	// Resolve platform tokens from both the client header and body before
	// checking injected features, so body-implied beta requirements also
	// receive block policy evaluation.
	tokens := bedrock.ResolveBedrockBetaTokens(header, body, model, rules...)
	if err := r.checkBetaTokens(ctx, tokens, target, model); err != nil {
		return nil, err
	}
	return anthropic.FilterBetaTokens(tokens, policy.FilterSet), nil
}

// PrepareBedrockCompatibility rewrites body and headers when the selected group
// enables Bedrock compatibility. Nil group, absent policy reader, lookup
// failure, and disabled policy retain the supplied body and current headers.
//
// Body preparation removes unsupported Claude Code fields, adjusts
// model-specific thinking, applies matching character replacement to tool calls
// and results, and resolves body beta tokens. The existing nonempty header is
// then resolved against that prepared body and replaced or removed. Callers
// supply writable headers and retain the returned bytes for the same request.
func (r *Runtime) PrepareBedrockCompatibility(ctx context.Context, headers http.Header, body []byte, model string, target *gatewayprovider.ExecutionProvider, groupID *int64) []byte {
	if groupID == nil || r.dependencies.GroupPolicies == nil {
		return body
	}
	// Read the group compatibility opt-in before rewriting body or headers
	// so unavailable policy leaves the client preparation input available
	// for the ordinary path.
	policy, err := r.dependencies.GroupPolicies.GetGroupPolicy(ctx, *groupID)
	if err != nil || policy == nil || !policy.IsBedrockCCCompatEnabled(target.Record.Platform) {
		return body
	}
	// Apply the group compatibility body sequence before recomputing its
	// beta header so fields, thinking blocks, and tool identities use the
	// Bedrock representation together.
	body = bedrock.SanitizeBedrockCCFields(body)
	body = bedrock.SanitizeBedrockThinking(body, model)
	body = bedrock.SanitizeBedrockToolUseIDs(body)
	body = bedrock.SanitizeBedrockCCBetaTokens(body, model)
	if beta := headers.Get("anthropic-beta"); beta != "" {
		if filtered := bedrock.ResolveBedrockBetaTokens(beta, body, model); len(filtered) > 0 {
			headers.Set("anthropic-beta", strings.Join(filtered, ", "))
		} else {
			headers.Del("anthropic-beta")
		}
	}
	return body
}
