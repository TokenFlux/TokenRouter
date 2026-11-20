package openaiattempt

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/provider"
)

// heartbeat_retry_scenario_test.go covers openai_chat_attempts.go,
// openai_messages_attempts.go, responses_attempts.go, and support.go with
// heartbeat commitment before a provider failure or retry exhaustion.

// heartbeatRetryPorts runs real retry decisions inside the ordinary provider loop.
type heartbeatRetryPorts struct {
	textflow.ResponsePorts              // real protocol retry adapter
	t                      *testing.T   // active assertion scope
	c                      *gin.Context // shared client response
	attempts               int          // selected provider count
	completedSwitches      int          // successful switch count
}

// heartbeatFailurePorts drives real bridge failure presentation after a first
// attempt commits HTTP 200 SSE via heartbeats while streamStarted stays false.
type heartbeatFailurePorts struct {
	textflow.ResponsePorts              // real protocol attempt bridge
	t                      *testing.T   // active assertion scope
	c                      *gin.Context // shared client response
	heartbeat              string       // protocol idle heartbeat frame
	failover               bool         // true returns UpstreamFailoverError
	attempts               int          // selected provider count
}

// TestTextAttemptsRetryAfterQueueHeartbeat starts both attempt bridges after a
// queue heartbeat commits HTTP 200. More heartbeat bytes preserve replay, while
// the first business bytes prevent the next provider from extending that stream.
func TestTextAttemptsRetryAfterQueueHeartbeat(t *testing.T) {
	for _, protocol := range []string{"chat", "messages"} {
		t.Run(protocol, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			c.Header("Content-Type", "text/event-stream")
			n, err := c.Writer.Write([]byte(": queued\n\n"))
			require.NoError(t, err)
			gatewayhttp.RecordStreamHeartbeat(c, n)
			c.Writer.Flush()
			require.True(t, c.Writer.Written())
			base := responsesAttemptBridge{
				c: c, reqLog: zap.NewNop(), provider: &gatewayprovider.ExecutionProvider{},
				writerSizeBeforeForward: gatewayhttp.HeartbeatAdjustedWrittenSize(c),
				fixed: &openAIExecutionDependencies{
					recordOpenAICyberWarning: func(*gin.Context, *zap.Logger, *apikey.APIKey, *gatewayprovider.ExecutionProvider, string, int, []byte, string) {
					},
					reportOpenAIProviderScheduleResult: func(*gatewayprovider.ExecutionProvider, string, bool, *int, ...error) bool { return true },
				},
			}
			n, err = c.Writer.Write([]byte(": queued again\n\n"))
			require.NoError(t, err)
			gatewayhttp.RecordStreamHeartbeat(c, n)
			ports := &heartbeatRetryPorts{t: t, c: c}
			if protocol == "chat" {
				ports.ResponsePorts = &openAIChatAttemptBridge{responsesAttemptBridge: base}
			} else {
				ports.ResponsePorts = &openAIMessageAttemptBridge{responsesAttemptBridge: base}
			}
			textflow.RunResponses(textflow.ResponseOptions{MaxSwitches: 1}, ports)
			require.Equal(t, 2, ports.attempts)
			require.Equal(t, 1, ports.completedSwitches)
			require.Contains(t, rec.Body.String(), "data: second provider")
			require.NotEqual(t, base.writerSizeBeforeForward, gatewayhttp.HeartbeatAdjustedWrittenSize(c))
		})
	}
}

// CanAttempt permits both local providers in this connected request fixture.
func (p *heartbeatRetryPorts) CanAttempt() bool { return true }

// Select returns a distinct provider and checks the failed target was excluded.
// The fixture accepts exactly two attempts, preventing an accidental retry loop
// from silently selecting additional providers or hanging the test.
func (p *heartbeatRetryPorts) Select(excluded map[int64]struct{}) (textflow.ResponseSelection, error) {
	p.attempts++
	require.LessOrEqual(p.t, p.attempts, 2)
	if p.attempts == 2 {
		require.Contains(p.t, excluded, int64(1))
	}
	return textflow.ResponseSelection{Available: true, Selection: textflow.Selection{Provider: provider.ProviderSnapshot{ID: int64(p.attempts)}}}, nil
}

// Acquire supplies immediate capacity for the selected local provider fixture.
func (p *heartbeatRetryPorts) Acquire() bool { return true }

// Forward fails the first provider after a heartbeat and completes the second.
// Both attempts share one HTTP response, reproducing the output boundary that
// previously prevented provider switching after queue heartbeats committed headers.
func (p *heartbeatRetryPorts) Forward() textflow.ResponseOutcome {
	if p.attempts == 1 {
		n, err := p.c.Writer.Write([]byte(": waiting for first provider\n\n"))
		require.NoError(p.t, err)
		gatewayhttp.RecordStreamHeartbeat(p.c, n)
		failure := &forwardcore.UpstreamFailoverError{StatusCode: http.StatusBadGateway}
		return textflow.ResponseOutcome{Outcome: textflow.Outcome{Err: failure, Failure: &textflow.AttemptFailure{Cause: failure, Policy: failure.RetryFailure()}}}
	}
	_, err := p.c.Writer.Write([]byte("data: second provider\n\n"))
	require.NoError(p.t, err)
	return textflow.ResponseOutcome{}
}

// Switched leaves scheduling feedback outside this output-boundary test fixture.
func (p *heartbeatRetryPorts) Switched() {}

// Switching leaves provider transition logging outside this output-boundary fixture.
func (p *heartbeatRetryPorts) Switching(*textflow.AttemptFailure, int, int) {}

// Success accepts the healthy second provider without recording production feedback.
func (p *heartbeatRetryPorts) Success() {}

// Complete leaves billing outside this test of response retry eligibility.
func (p *heartbeatRetryPorts) Complete() {}

// Completed retains the switch count observed by the successful outer loop.
func (p *heartbeatRetryPorts) Completed(switches int) { p.completedSwitches = switches }

// TestTextAttemptsHeartbeatCommitEmitsSSEErrorOnFailure reproduces a streaming
// request whose headers and heartbeats commit HTTP 200 before the upstream fails
// while streamStarted remains false. Every protocol must emit a valid SSE error
// frame without trailing raw JSON across single-provider selection failure,
// retry exhaustion, and non-failover errors, while recording Ops stream status.
func TestTextAttemptsHeartbeatCommitEmitsSSEErrorOnFailure(t *testing.T) {
	protocols := []struct {
		name      string              // subtest label
		endpoint  string              // client request path
		proto     protocol.ProtocolID // client wire protocol
		heartbeat string              // idle heartbeat payload
		wantEvent string              // expected terminal SSE event
	}{
		{"messages", gatewayhttp.EndpointMessages, protocol.ProtocolAnthropicMessages, "event: ping\ndata: {\"type\": \"ping\"}\n\n", "event: error\n"},
		{"chat", gatewayhttp.EndpointChatCompletions, protocol.ProtocolOpenAIChatCompletions, ": keepalive\n\n", "event: error\n"},
		{"responses", gatewayhttp.EndpointResponses, protocol.ProtocolOpenAIResponses, ": keepalive\n\n", "event: response.failed\n"},
	}
	modes := []struct {
		name        string // failure scenario label
		maxSwitches int    // switch budget for RunResponses
		failover    bool   // whether Forward returns UpstreamFailoverError
	}{
		{"single_provider_selection_failure", 1, true},
		{"retry_exhausted", 0, true},
		{"non_failover_other_failure", 1, false},
	}

	for _, proto := range protocols {
		for _, mode := range modes {
			t.Run(proto.name+"/"+mode.name, func(t *testing.T) {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				req := httptest.NewRequest(http.MethodPost, proto.endpoint, nil)
				c.Request = req.WithContext(requeststate.WithClientProtocol(req.Context(), proto.proto))

				streamStarted := false
				support := &Support{}
				fixed := &openAIExecutionDependencies{
					anthropicStreamingAwareError: func(c *gin.Context, status int, errType, message string, started bool) {
						gatewayhttp.DefaultOpenAIErrorOutput().WriteAnthropicStreamingError(c, status, errType, message, started)
					},
					ensureAnthropicErrorResponse:        support.EnsureAnthropicErrorResponse,
					ensureOpenAIForwardErrorResponse:    gatewayhttp.DefaultOpenAIErrorOutput().EnsureResponse,
					ensureOpenAIStreamReadErrorResponse: support.EnsureOpenAIStreamReadErrorResponse,
					handleAnthropicFailoverExhausted:    support.HandleAnthropicFailoverExhausted,
					handleFailoverExhausted:             support.HandleFailoverExhausted,
					handleFailoverExhaustedSimple:       support.HandleFailoverExhaustedSimple,
					handleStreamingAwareError:           gatewayhttp.DefaultOpenAIErrorOutput().StreamError,
					observeOpenAIProviderHealthFailure:  func(context.Context, *gatewayprovider.ExecutionProvider, error) bool { return false },
					recordOpenAICyberWarning: func(*gin.Context, *zap.Logger, *apikey.APIKey, *gatewayprovider.ExecutionProvider, string, int, []byte, string) {
					},
					recordOpenAIForwardErrorCyberWarning: func(*gin.Context, *zap.Logger, *apikey.APIKey, *gatewayprovider.ExecutionProvider, string, int, error) bool {
						return false
					},
					reportOpenAIProviderScheduleResult: func(*gatewayprovider.ExecutionProvider, string, bool, *int, ...error) bool { return true },
				}
				base := responsesAttemptBridge{
					c:                       c,
					reqLog:                  zap.NewNop(),
					provider:                &gatewayprovider.ExecutionProvider{},
					streamStarted:           &streamStarted,
					writerSizeBeforeForward: gatewayhttp.HeartbeatAdjustedWrittenSize(c),
					fixed:                   fixed,
				}
				ports := &heartbeatFailurePorts{
					t:         t,
					c:         c,
					heartbeat: proto.heartbeat,
					failover:  mode.failover,
				}
				switch proto.name {
				case "messages":
					ports.ResponsePorts = &openAIMessageAttemptBridge{responsesAttemptBridge: base}
				case "chat":
					ports.ResponsePorts = &openAIChatAttemptBridge{responsesAttemptBridge: base}
				default:
					ports.ResponsePorts = &base
				}

				textflow.RunResponses(textflow.ResponseOptions{MaxSwitches: mode.maxSwitches}, ports)

				require.Equal(t, http.StatusOK, rec.Code)
				body := rec.Body.String()
				require.Contains(t, body, proto.heartbeat)
				require.Contains(t, body, proto.wantEvent)
				assertValidSSEStreamOnly(t, body)

				streamErr, ok := gatewayhttp.GetOpsStreamError(c)
				require.True(t, ok, "SSE terminal error must record OpsStreamError")
				require.Equal(t, http.StatusBadGateway, streamErr.IntendedStatus)
			})
		}
	}
}

// TestSupportEnsureAnthropicErrorResponse_HeartbeatAndTerminalDedup exercises
// Anthropic fallback error output after heartbeat commitment, non-SSE JSON
// output, and a previously delivered terminal SSE event. Heartbeat-only streams
// must emit an SSE error with Ops status, while non-SSE and terminated streams
// must skip duplicate fallback output.
func TestSupportEnsureAnthropicErrorResponse_HeartbeatAndTerminalDedup(t *testing.T) {
	support := &Support{}

	t.Run("heartbeat committed SSE emits error", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, gatewayhttp.EndpointMessages, nil)
		c.Header("Content-Type", "text/event-stream")
		n, err := c.Writer.Write([]byte("event: ping\ndata: {\"type\": \"ping\"}\n\n"))
		require.NoError(t, err)
		gatewayhttp.RecordStreamHeartbeat(c, n)
		c.Writer.Flush()

		wrote := support.EnsureAnthropicErrorResponse(c, false)
		require.True(t, wrote)
		require.Equal(t, http.StatusOK, rec.Code)
		require.Contains(t, rec.Body.String(), "event: error\n")
		assertValidSSEStreamOnly(t, rec.Body.String())
		streamErr, ok := gatewayhttp.GetOpsStreamError(c)
		require.True(t, ok)
		require.Equal(t, http.StatusBadGateway, streamErr.IntendedStatus)
	})

	t.Run("non-SSE written JSON skips fallback", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, gatewayhttp.EndpointMessages, nil)
		c.JSON(http.StatusBadRequest, gin.H{"type": "error", "error": gin.H{"type": "invalid_request_error", "message": "bad"}})

		wrote := support.EnsureAnthropicErrorResponse(c, false)
		require.False(t, wrote)
		require.NotContains(t, rec.Body.String(), "event: error")
	})

	t.Run("delivered terminal SSE suppresses duplicate fallback", func(t *testing.T) {
		engine := gin.New()
		var wroteFallback bool
		engine.POST(gatewayhttp.EndpointMessages, gatewayhttp.TextRequestTiming(), gatewayhttp.DownstreamSSEKeepalive(time.Hour, time.Hour), func(c *gin.Context) {
			c.Header("Content-Type", "text/event-stream")
			_, err := c.Writer.WriteString("event: ping\ndata: {\"type\": \"ping\"}\n\n")
			require.NoError(t, err)
			gatewayhttp.DefaultOpenAIErrorOutput().WriteAnthropicStreamingError(c, http.StatusBadGateway, "upstream_error", "boom", false)
			require.True(t, gatewayhttp.TerminalSSEDelivered(c))
			wroteFallback = support.EnsureAnthropicErrorResponse(c, false)
		})
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, gatewayhttp.EndpointMessages, nil))

		require.False(t, wroteFallback)
		require.Equal(t, 1, strings.Count(rec.Body.String(), "event: error\n"))
		assertValidSSEStreamOnly(t, rec.Body.String())
	})
}

// CanAttempt keeps this failure-presentation fixture eligible to select once.
func (p *heartbeatFailurePorts) CanAttempt() bool { return true }

// Select supplies one provider on the first attempt and reports no available
// providers once that provider is excluded, reproducing single-provider groups.
func (p *heartbeatFailurePorts) Select(excluded map[int64]struct{}) (textflow.ResponseSelection, error) {
	p.attempts++
	if _, seen := excluded[1]; seen || p.attempts > 1 {
		return textflow.ResponseSelection{}, errors.New("no available providers")
	}
	return textflow.ResponseSelection{Available: true, Selection: textflow.Selection{Provider: provider.ProviderSnapshot{ID: 1}}}, nil
}

// Acquire grants immediate capacity for the local failure fixture.
func (p *heartbeatFailurePorts) Acquire() bool { return true }

// Forward commits HTTP 200 text/event-stream with one heartbeat without setting
// streamStarted, then returns either a 502 failover error or a non-failover error.
func (p *heartbeatFailurePorts) Forward() textflow.ResponseOutcome {
	p.c.Header("Content-Type", "text/event-stream")
	n, err := p.c.Writer.Write([]byte(p.heartbeat))
	require.NoError(p.t, err)
	gatewayhttp.RecordStreamHeartbeat(p.c, n)
	p.c.Writer.Flush()
	if p.failover {
		failure := &forwardcore.UpstreamFailoverError{StatusCode: http.StatusBadGateway}
		return textflow.ResponseOutcome{Outcome: textflow.Outcome{Err: failure, Failure: &textflow.AttemptFailure{Cause: failure, Policy: failure.RetryFailure()}}}
	}
	nonFailover := errors.New("unexpected upstream stream abort")
	return textflow.ResponseOutcome{Outcome: textflow.Outcome{Err: nonFailover}}
}

// Switched leaves provider-switch metrics outside this failure fixture.
func (p *heartbeatFailurePorts) Switched() {}

// Switching leaves provider-switch logging outside this failure fixture.
func (p *heartbeatFailurePorts) Switching(*textflow.AttemptFailure, int, int) {}

// Failed leaves post-OtherFailure logging outside this failure fixture.
func (p *heartbeatFailurePorts) Failed() {}

// assertValidSSEStreamOnly checks that every non-blank line in body is valid
// SSE framing and every data line contains valid JSON without trailing raw JSON.
func assertValidSSEStreamOnly(t *testing.T, body string) {
	t.Helper()
	for line := range strings.SplitSeq(body, "\n") {
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, ":") || strings.HasPrefix(line, "event: ") {
			continue
		}
		require.True(t, strings.HasPrefix(line, "data: "), "non-SSE line found in stream body: %q (full body: %q)", line, body)
		payload := strings.TrimPrefix(line, "data: ")
		require.True(t, json.Valid([]byte(payload)), "invalid JSON in SSE data line: %q", payload)
	}
}
