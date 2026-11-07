package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"

	egressadapter "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewaymedia "github.com/TokenFlux/TokenRouter/internal/gateway/media"
	mediaprovider "github.com/TokenFlux/TokenRouter/internal/gateway/media/provider"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	openaiprotocol "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// openai_auxiliary_embeddings.go executes selected-provider Embeddings
// requests and returns usage with client, billing, and upstream model
// identities.
// Embedding requests can use a client alias while the selected provider
// requires a different model and protocol base. The payload uses the mapped,
// normalized model; completion data keeps the original client name, billing
// name, and upstream name separately. Batch content and other input fields
// remain in the bytes sent to the native executor.
//
// The native executor builds the POST, owns response cleanup, and parses usage.
// HTTP policy distinguishes invalid input from provider failures before
// applying configured health and retry decisions. Failover returns captured
// response evidence before output is committed, while terminal responses use
// filtered headers and bounded body reading. The outer media entry owns
// completion submission after this attempt returns.

// ForwardEmbeddings executes selected-provider embedding input and returns
// observed usage. It requires a model, protocol API key, and validated
// OpenAI-format base before sending. Native execution owns response cleanup and
// uses DetachUpstreamContext; caller-facing errors can be written before a
// returned execution failure.
//
// Provider mapping determines the wire model and billing identity while the
// returned Model retains the client's trimmed name. Invalid-input errors pass
// through before health policy. Classified provider failures return to the
// outer media loop with retry evidence while its client writer remains
// available.
func (s *OpenAIAuxiliary) ForwardEmbeddings(
	ctx context.Context,
	c *gin.Context,
	provider *gatewayprovider.ExecutionProvider,
	body []byte,
	defaultMappedModel string,
) (*forwardcore.OpenAIResult, error) {
	startTime := time.Now()

	originalModel := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	if originalModel == "" {
		WriteEmbeddingsError(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return nil, fmt.Errorf("missing model in request")
	}

	billingModel := gatewayprovider.ExecutionModelPolicy(provider).ForwardModel(originalModel, defaultMappedModel)
	upstreamModel := gatewayprovider.ExecutionModelPolicy(provider).NormalizeOpenAI(billingModel)
	SetOpsUpstreamModel(c, upstreamModel)
	upstreamBody := body
	// Replace the wire model while retaining originalModel for client
	// attribution and billingModel for completion, so provider aliases do
	// not replace the client's identity.
	if upstreamModel != originalModel {
		upstreamBody = openaiprotocol.ReplaceModelInBody(body, upstreamModel)
	}

	logging.L().Debug("openai embeddings: forwarding",
		zap.Int64("provider_id", provider.Record.ID),
		zap.String("original_model", originalModel),
		zap.String("billing_model", billingModel),
		zap.String("upstream_model", upstreamModel),
	)

	apiKey := strings.TrimSpace(provider.View().GetOpenAIProtocolAPIKey())
	if apiKey == "" {
		return nil, fmt.Errorf("provider %d missing api_key", provider.Record.ID)
	}
	// A provider's Anthropic protocol base can end in /anthropic. Use its
	// OpenAI-format base so embeddings follows the corresponding compatible
	// API path rather than that suffix.
	baseURL := gatewayprovider.ExecutionProtocolTarget(provider).GetOpenAIFormatBaseURL()
	if baseURL == "" {
		baseURL = "https://api.openai.com"
	}
	validatedURL, err := s.Requests.ValidateBaseURL(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid base_url: %w", err)
	}
	targetURL := buildOpenAIEmbeddingsURL(validatedURL)

	proxyURL := ""
	if provider.Record.Proxy != nil {
		proxyURL = provider.Record.Proxy.URL()
	}
	forwardHeaders := make(http.Header)
	for key, values := range c.Request.Header {
		if AllowOpenAIRawChatHeader(strings.ToLower(key)) {
			forwardHeaders[key] = append([]string(nil), values...)
		}
	}
	target := &mediaprovider.EmbeddingsOptions{
		ProviderID: provider.Record.ID,

		Model: upstreamModel,

		URL: targetURL,

		Token: apiKey,

		ForwardHeaders: forwardHeaders,

		UserAgent: provider.View().GetOpenAIUserAgent(),

		ApplyHeaders: gatewayprovider.BindExecutionHeaders(provider),

		RequestContext: gatewayprovider.DetachUpstreamContext,

		Enter: s.Enter,

		StartedAt: startTime,

		Do: func(request *http.Request) (*http.Response, error) {
			return s.Requests.Transport.Do(request, proxyURL, provider.Record.ID, provider.Record.Concurrency)
		},

		TransportError: func(err error) error {
			safeErr := logredact.SanitizeUpstreamQueries(err.Error())
			SetOpsUpstreamError(c, 0, safeErr, "")
			AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
				Platform: provider.Record.Platform,

				ProviderID: provider.Record.ID,

				ProviderName: provider.Record.Name,

				UpstreamStatusCode: 0,

				Kind: "request_error",

				Message: safeErr,
			})
			WriteEmbeddingsError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed")
			return fmt.Errorf("upstream request failed: %s", safeErr)
		},

		ReadErrorBody: s.Output.ReadErrorBody,

		HTTPError: func(resp *http.Response, respBody []byte) error {
			upstreamMsg := logredact.SanitizeUpstreamQueries(strings.TrimSpace(upstream.ExtractErrorMessage(respBody)))
			var decision providercore.UpstreamErrorDecision
			// Invalid client input passes through before
			// provider-health effects; the shared resolver then
			// orders policy, generic output, and retryable provider
			// failures.
			return gatewaymedia.ResolveEmbeddingFailure(resp.StatusCode, gatewaymedia.EmbeddingFailurePorts{
				InvalidRequest: func() bool { return openai.IsOpenAIClientInvalidRequestError(resp.StatusCode, upstreamMsg, respBody) },
				ApplyPolicy: func() {
					if provider.Record.Platform == capability.PlatformGrok {
						decision = gatewayprovider.ApplyGrokExecutionHealth(ctx, s.Output.GrokHealth, provider, resp.StatusCode, resp.Header, respBody, "", upstreamModel)
					} else {
						decision = gatewayprovider.ApplyOpenAIResponseHealth(ctx, s.Output.Health, provider, resp.StatusCode, resp.Header, respBody, false, upstreamModel)
					}
				},
				Generic: func() bool { return decision.ShouldReturnGenericError() },
				Failover: func() bool {
					defaultFailover := gatewayprovider.ShouldFailoverOpenAIResponse(resp.StatusCode, upstreamMsg, respBody)
					if provider.Record.Platform == capability.PlatformGrok {
						defaultFailover = gatewayprovider.ShouldFailoverGrokResponse(resp.StatusCode, respBody)
					}
					return decision.ShouldFailover(gatewayprovider.ExecutionErrorPolicy(provider), resp.StatusCode, defaultFailover)
				},
				RecordFailover: func() {
					upstreamDetail := ""
					if s.Output.Options.LogUpstreamErrorBody {
						maxBytes := s.Output.Options.LogUpstreamErrorBodyMaxBytes
						if maxBytes <= 0 {
							maxBytes = 2048
						}
						// Cap optional diagnostics at a
						// UTF-8 boundary while
						// returning captured response
						// bytes separately for the
						// outer retry classification
						// and error handling.
						upstreamDetail = logredact.TruncateUTF8(string(respBody), maxBytes)
					}
					AppendOpsUpstreamError(c, ops.OpsUpstreamErrorEvent{
						Platform: provider.Record.Platform,

						ProviderID: provider.Record.ID,

						ProviderName: provider.Record.Name,

						UpstreamStatusCode: resp.StatusCode,

						UpstreamRequestID: resp.Header.Get("x-request-id"),

						Kind: "failover",

						Message: upstreamMsg,

						Detail: upstreamDetail,
					}, telemetry.RequestAttemptFromResponse(resp))
				},
				NewFailover: func() error {
					// Failover construction reapplies
					// OpenAI health and uses its stop
					// result to gate pool retries after the
					// shared resolver applied the initial
					// policy.
					shouldDisable := gatewayprovider.ApplyOpenAIResponseHealth(ctx, s.Output.Health, provider, resp.StatusCode, resp.Header, respBody, false, upstreamModel).StopScheduling
					retryableOnSameProvider := !shouldDisable && provider.View().IsPoolMode() && provider.View().IsPoolModeRetryableStatus(resp.StatusCode)
					// OAuth 429 and access-state failures
					// carry typed recovery metadata;
					// ordinary embedding failures retain
					// the status/body shape used by the
					// outer media retry loop.
					if provider.View().IsOpenAIOAuth() && resp.StatusCode == http.StatusTooManyRequests {
						return (gatewayprovider.OpenAIFailoverPolicy{Health: s.Output.Health}).NewProviderFailure(provider, resp.StatusCode, resp.Header, respBody, upstreamMsg, shouldDisable, retryableOnSameProvider)
					}
					if gatewayprovider.IsOpenAIHTTPUpstreamAccessStateError(resp.StatusCode, upstreamMsg, respBody) {
						return gatewayprovider.NewOpenAIUpstreamFailure(resp.StatusCode, resp.Header, respBody, upstreamMsg, retryableOnSameProvider)
					}
					return &forwardcore.UpstreamFailoverError{StatusCode: resp.StatusCode, ResponseBody: respBody, RetryableOnSameProvider: retryableOnSameProvider}
				},
				Forward: func() { WriteEmbeddingsUpstreamResponse(c, resp, respBody, s.Output.Headers) },
				Write: func(response gatewaymedia.ErrorResponse) {
					WriteEmbeddingsError(c, response.Status, response.Type, response.Message)
				},
			})
		},

		ReadBody: func(reader io.Reader) ([]byte, error) {
			return ReadUpstreamResponseBody(reader, s.Output.Options.ReadLimit, c, OpenAIResponseTooLarge)
		},

		ReadFailure: func(err error) error {
			if !errors.Is(err, httpclient.ErrResponseBodyTooLarge) {
				WriteEmbeddingsError(c, http.StatusBadGateway, "api_error", "Failed to read upstream response")
			}
			return fmt.Errorf("read upstream body: %w", err)
		},

		WriteHeaders: func(output, input http.Header) {
			egressadapter.WriteFilteredHeaders(output, input, s.Output.Headers)
		},
	}
	result, err := (mediaprovider.Embeddings{Options: *target}).Execute(ctx, upstream.AttemptInput{
		Protocol:      protocol.ProtocolEmbeddings,
		Body:          upstreamBody,
		ResponseModel: originalModel,
	}, ResponseSink{Writer: c.Writer})
	if err != nil {
		return nil, err
	}
	return &forwardcore.OpenAIResult{
		RequestAttempt: result.RequestAttempt,
		RequestID:      result.RequestID,

		UpstreamHeaders: result.UpstreamHeaders,

		Usage: openaiprotocol.ForwardUsage{
			InputTokens:              result.Usage.InputTokens,
			ImageInputTokens:         result.ImageInputTokens,
			ImageCacheReadTokens:     result.ImageCacheReadTokens,
			OutputTokens:             result.Usage.OutputTokens,
			CacheReadInputTokens:     result.Usage.CacheReadInputTokens,
			CacheCreationInputTokens: result.Usage.CacheCreationInputTokens,
		},

		Model: originalModel,

		BillingModel: billingModel,

		UpstreamModel: upstreamModel,

		Stream: false,

		Duration: result.Duration,
	}, nil
}

// buildOpenAIEmbeddingsURL appends the embedding endpoint using
// OpenAI-compatible base-path handling.
func buildOpenAIEmbeddingsURL(base string) string {
	return httpclient.BuildOpenAIEndpointURL(base, "/v1/embeddings")
}
