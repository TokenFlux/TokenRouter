package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	openaiexecution "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// openai_protocol_execution_passthrough.go dispatches passthrough execution
// and records instruction and timeout-header diagnostics.
// Passthrough execution retains request bytes for the selected provider while
// the shared runner applies required compatibility and model policies. Its
// HTTP adapter captures returned model observations, and local diagnostics
// identify rejected instructions and timeout headers without choosing the
// outer provider-retry loop.

// Passthrough runs one provider attempt with its canonical image-intent bytes,
// model and client stream preference. Keep c and the bound services available
// until synchronous reading finishes; the shared runner owns response
// recovery, and returned observations can accompany an error.
func (s *OpenAITextExecutor) Passthrough(ctx context.Context, c *gin.Context, provider *gatewayprovider.ExecutionProvider, body, canonicalImageIntentBody []byte, reqModel string, attemptImageIntentInvalidated bool, reasoningEffort *string, reqStream bool, startTime time.Time, tlsRouterMatch ...egress.TLSFingerprintRouterMatchResult) (*forwardcore.OpenAIResult, error) {
	return s.passthroughWithPreludeState(ctx, c, provider, body, canonicalImageIntentBody, reqModel, attemptImageIntentInvalidated, reasoningEffort, reqStream, startTime, "", false, false, tlsRouterMatch...)
}

// passthroughWithPreludeState runs one passthrough attempt while forwarding
// compatibility markers already satisfied during PreparePrelude.
func (s *OpenAITextExecutor) passthroughWithPreludeState(ctx context.Context, c *gin.Context, provider *gatewayprovider.ExecutionProvider, body, canonicalImageIntentBody []byte, reqModel string, attemptImageIntentInvalidated bool, reasoningEffort *string, reqStream bool, startTime time.Time, sanitizedSchemasPlatform string, sanitizedReasoningReplay, sanitizedInputItemIDs bool, tlsRouterMatch ...egress.TLSFingerprintRouterMatchResult) (*forwardcore.OpenAIResult, error) {
	input := openaiexecution.PassthroughInput{
		Body:                         body,
		CanonicalImageIntentBody:     canonicalImageIntentBody,
		Model:                        reqModel,
		ImageIntentInvalidated:       attemptImageIntentInvalidated,
		ReasoningEffort:              reasoningEffort,
		Stream:                       reqStream,
		StartedAt:                    startTime,
		SanitizedToolSchemasPlatform: sanitizedSchemasPlatform,
		SanitizedReasoningReplay:     sanitizedReasoningReplay,
		SanitizedInputItemIDs:        sanitizedInputItemIDs,
	}
	p := &openAIPassthroughExecutionAdapter{openAIMessagesExecutionAdapter: &openAIMessagesExecutionAdapter{s: s, c: c, provider: provider, tls: tlsRouterMatch}}
	result, err := openaiexecution.RunPassthrough(ctx, input, p)
	out := openaiexecution.ToForwardResult(result)
	captureResponseModel(c, out)
	return out, err
}

// logOpenAIPassthroughInstructionsRejected logs an instruction rejection with
// provider and request diagnostics. A nil context selects background logging,
// and a missing provider supplies zero or empty identity fields; the appended
// request fields follow their own capture rules.
func logOpenAIPassthroughInstructionsRejected(
	ctx context.Context,
	c *gin.Context,
	provider *gatewayprovider.ExecutionProvider,
	reqModel string,
	rejectReason string,
	body []byte,
) {
	if ctx == nil {
		ctx = context.Background()
	}
	providerID := int64(0)
	providerName := ""
	providerType := ""
	if provider != nil {
		providerID = provider.Record.ID
		providerName = strings.TrimSpace(provider.Record.Name)
		providerType = strings.TrimSpace(provider.Record.Type)
	}
	fields := []zap.Field{
		zap.String("component", "service.openai_gateway"),
		zap.Int64("provider_id", providerID),
		zap.String("provider_name", providerName),
		zap.String("provider_type", providerType),
		zap.String("request_model", strings.TrimSpace(reqModel)),
		zap.String("reject_reason", strings.TrimSpace(rejectReason)),
	}
	fields = AppendCodexRejectedRequestFields(fields, c, body)
	logging.FromContext(ctx).With(fields...).Warn("OpenAI passthrough rejected Codex request locally: valid instructions are missing")
}

// shouldFailoverOpenAIPassthroughResponse delegates classification using the
// selected credential type and request-scoped error predicates. Pool-mode
// retry statuses participate through the provider callback; callers still
// enforce commitment and output rules before starting another attempt.
func shouldFailoverOpenAIPassthroughResponse(provider *gatewayprovider.ExecutionProvider, status int, body []byte) bool {
	return openaiexecution.ShouldFailoverPassthrough(status, body, openaiexecution.PassthroughFailureOptions{
		APIKey:        provider != nil && provider.Record.Type == capability.ProviderTypeAPIKey,
		Cyber:         func(b []byte) bool { hit, _, _ := openai.DetectOpenAICyberPolicy(b); return hit },
		ContextWindow: func(b []byte) bool { return openai.IsOpenAIContextWindowError("", b) },
		AccessState:   func(s int, b []byte) bool { return gatewayprovider.IsOpenAIHTTPUpstreamAccessStateError(s, "", b) },
		BodyTooLarge:  func(s int, b []byte) bool { return gatewayprovider.IsOpenAIRequestBodyTooLargeError(s, "", b) },
		PoolRetryable: func(s int) bool {
			return provider != nil && provider.View().IsPoolMode() && provider.View().IsPoolModeRetryableStatus(s)
		},
	})
}

// collectOpenAIPassthroughTimeoutHeaders returns sorted lowercase
// timeout-header diagnostics from the supplied map. Nil input returns nil,
// recognized values are joined with vertical bars, and this collection leaves
// header admission or filtering to the request builder.
func collectOpenAIPassthroughTimeoutHeaders(h http.Header) []string {
	if h == nil {
		return nil
	}
	var matched []string
	for key, values := range h {
		lowerKey := strings.ToLower(strings.TrimSpace(key))
		if isOpenAIPassthroughTimeoutHeader(lowerKey) {
			entry := lowerKey
			if len(values) > 0 {
				entry = fmt.Sprintf("%s=%s", lowerKey, strings.Join(values, "|"))
			}
			matched = append(matched, entry)
		}
	}
	sort.Strings(matched)
	return matched
}
