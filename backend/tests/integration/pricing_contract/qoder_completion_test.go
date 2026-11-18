package pricingcontract

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewaycapture "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// A Qoder stream can deliver content and measured usage before an upstream
// error ends the attempt. The request fixture feeds that partial result into
// completion, checking release and recording counts while separately injecting
// settlement and analysis-log failures into the downstream stores.

// TestQoderNativeChainCompletionFailures emits content and usage before a
// wrapped upstream error. It checks retained token counts and distinct recorder
// outcomes for settlement and marked log-insertion failures.
func TestQoderNativeChainCompletionFailures(t *testing.T) {
	for _, tc := range []struct {
		name string // subtest failure label
		// billingFail and logFail select independent store errors after the
		// stream has produced content and usage. Each row enables one failure
		// for its corresponding recorder-outcome assertions.
		//
		// billingFail: injected settlement failure; logFail: injected
		// usage insertion failure
		billingFail, logFail bool
	}{{name: "settlement-failure", billingFail: true}, {name: "record-failure", logFail: true}} {
		t.Run(tc.name, func(t *testing.T) {
			provider, platform, client := gatewaytestkit.NewDefaultQoderFixture()
			client.Body = qoderWrappedSSELineForTest(t, map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": "served"}}}}) + qoderWrappedSSELineForTest(t, map[string]any{"usage": map[string]any{"prompt_tokens": 12, "completion_tokens": 3}}) + qoderWrappedErrorSSELineForTest(t, 502, map[string]any{"code": "500", "message": "fixture failure"})
			usageRepo := &gatewaytestkit.UsageLogStore{}
			billingRepo := &gatewaytestkit.SettlementStore{}
			if tc.billingFail {
				billingRepo.Err = errors.New("fixture billing unavailable")
			}
			if tc.logFail {
				usageRepo.Err = usage.MarkUsageLogCreateNotPersisted(errors.New("fixture usage unavailable"))
			}
			completion := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &gatewaytestkit.UserStore{}, &gatewaytestkit.SubscriptionStore{})
			rec := httptest.NewRecorder()
			body := []byte(`{"model":"auto","stream":true,"messages":[{"role":"user","content":"hi"}]}`)
			// Execute the prepared Qoder attempt directly and feed its partial
			// usage-bearing result into completion recording.
			executor, input := platform.Runtime.PrepareQoderTarget(gatewayhttp.QoderRequestMetadata(nil), provider, body, protocol.ProtocolOpenAIChatCompletions, "auto")
			r, err := executor.Execute(context.Background(), input, &gateway.OutputTracker{Sink: gatewayhttp.ResponseSink{Writer: rec}})
			require.Error(t, err)
			require.True(t, r.Served && r.HasUsage)
			completionErr := completion.RecordMessages(context.Background(), &gatewaycapture.MessagesCapture{
				Result:   forwardcore.MessagesFromAttempt(r),
				APIKey:   &apikey.APIKey{ID: 505},
				User:     &identity.User{ID: 605},
				Provider: gatewaycapture.ExecutionCompletionRecord(gatewaycapture.NewExecutionProvider(provider)),
			})
			require.Contains(t, rec.Body.String(), "served")
			require.Len(t, client.Requests, 1)
			require.Equal(t, 1, billingRepo.Calls)
			require.Equal(t, 1, usageRepo.Calls)
			require.Equal(t, 12, usageRepo.LastLog.InputTokens)
			require.Equal(t, 3, usageRepo.LastLog.OutputTokens)
			if tc.billingFail {
				require.ErrorIs(t, completionErr, billingRepo.Err)
				require.Zero(t, usageRepo.LastLog.ActualCost)
			} else {
				require.NoError(t, completionErr)
			}
		})
	}
}

// qoderWrappedSSELineForTest serializes a payload into Qoder's nested body SSE
// format. It fails the test on either JSON encoding error and returns one data
// event with its terminating blank line.
func qoderWrappedSSELineForTest(t *testing.T, inner map[string]any) string {
	t.Helper()
	body, err := json.Marshal(inner)
	require.NoError(t, err)
	// Qoder's wrapper body is a JSON string containing the encoded inner
	// object. The second marshal supplies the escaping expected by
	// the stream reader.
	wrapper, err := json.Marshal(map[string]string{"body": string(body)})
	require.NoError(t, err)
	return "data: " + string(wrapper) + "\n\n"
}

// qoderWrappedErrorSSELineForTest builds a Qoder SSE wrapper carrying an error
// status and nested payload. It fails the test on either JSON encoding error
// and terminates the data event with a blank line.
func qoderWrappedErrorSSELineForTest(t *testing.T, statusCode int, inner map[string]any) string {
	t.Helper()
	body, err := json.Marshal(inner)
	require.NoError(t, err)
	// Keep the encoded payload inside the wrapper's body string while
	// statusCodeValue carries the upstream failure. The reader interprets
	// those two fields separately.
	wrapper, err := json.Marshal(map[string]any{
		"body":            string(body),
		"statusCodeValue": statusCode,
	})
	require.NoError(t, err)
	return "data: " + string(wrapper) + "\n\n"
}
