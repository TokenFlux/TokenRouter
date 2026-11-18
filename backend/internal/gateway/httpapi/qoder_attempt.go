package httpapi

import (
	"context"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayadapter "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// qoder_attempt.go executes Qoder attempts and retains usage-bearing partial
// results.
// A native Qoder attempt can fail after serving measurable usage. The HTTP
// adapter returns that partial result only when execution reports both Served
// and HasUsage; a failure with either observation absent returns the error
// alone. The outer request loop decides refresh, provider switching and
// completion from this qualified result.
//
// Converted client output uses the first nonblank response-model override or
// the trimmed body model when no override exists. Request metadata clones
// headers and records detected client identity before target preparation,
// keeping this attempt's input available to platform execution. Result fields
// carry observed tokens, duration and disconnect state into completion, with
// no synthesized first-token timing.

// ForwardQoderAttempt executes one selected Qoder target and writes through
// the current Gin response. The first nonblank explicit response model wins,
// otherwise the trimmed body model is used; callers own provider failover and
// completion.
//
// Errors are observed, and retain a partial result only when
// execution reports both Served and HasUsage. Returned usage, duration and
// client-disconnect state come from execution without synthesized first-token
// timing.
// @project-doc docs/architecture/gateway_request_lifecycle.md#qoder_gateway_execution
func ForwardQoderAttempt(ctx context.Context, c *gin.Context, runtime *gatewayadapter.QoderRuntime, value *provider.Record, body []byte, wire protocol.ProtocolID, responseModels ...string) (*forward.MessagesResult, error) {
	responseModel := qoder.FirstNonEmptyQoder(responseModels...)
	// Preserve an explicit response-model override when supplied; body identity is
	// the fallback so converted output can still name the requested client model.
	if responseModel == "" {
		responseModel = strings.TrimSpace(qoder.GjsonString(body, "model"))
	}
	executor, input := runtime.PrepareQoderTarget(QoderRequestMetadata(c), value, body, wire, responseModel)
	result, err := executor.Execute(ctx, input, NewResponseSink(c))
	if err != nil {
		runtime.ObserveQoderFailure(ctx, value, err)
		// Without both service and usage evidence, expose only the error to outer
		// orchestration rather than a completion result that could record unobserved work.
		if !result.Served || !result.HasUsage {
			return nil, err
		}
	}
	return &forward.MessagesResult{RequestID: result.RequestID, Model: result.Model, UpstreamModel: result.UpstreamModel, Usage: result.Usage, Stream: result.Stream, Duration: result.Duration, ClientDisconnect: result.ClientDisconnect}, err
}

// QoderRequestMetadata snapshots request headers and detected client identity
// for one Qoder attempt. Headers are cloned when a request exists; missing
// request state leaves them absent, while the API-key identity reader supplies
// its own fallback.
func QoderRequestMetadata(c *gin.Context) qoder.RequestMetadata {
	result := qoder.RequestMetadata{APIKeyID: APIKeyIDFromContext(c)}
	if c != nil && c.Request != nil {
		result.Headers = c.Request.Header.Clone()
		result.ClaudeCode = requeststate.IsClaudeCodeClient(c.Request.Context())
	}
	return result
}
