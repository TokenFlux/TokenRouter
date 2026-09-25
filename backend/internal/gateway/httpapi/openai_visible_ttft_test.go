package httpapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gatewaytestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"

	responseupstream "github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/gateway"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIVisibleOutputClassification(t *testing.T) {
	tests := []struct {
		name      string
		data      string
		eventType string
		want      bool
	}{
		{name: "keepalive", data: `{"type":"keepalive"}`, want: false},
		{name: "created", data: `{"type":"response.created"}`, want: false},
		{name: "empty output item", data: `{"type":"response.output_item.added","item":{"id":"item_test","type":"reasoning","summary":[]}}`, want: false},
		{name: "empty delta", data: `{"type":"response.output_text.delta","delta":""}`, want: false},
		{name: "text delta", data: `{"type":"response.output_text.delta","delta":"test output"}`, want: true},
		{name: "tool arguments", data: `{"type":"response.function_call_arguments.delta","delta":"{}"}`, want: true},
		{name: "partial image", data: `{"type":"response.image_generation_call.partial_image","partial_image_b64":"dGVzdA=="}`, want: true},
		{name: "completed image item", data: `{"type":"response.output_item.done","item":{"id":"item_test","type":"image_generation_call","result":"dGVzdA=="}}`, want: true},
		{name: "empty completed", data: `{"type":"response.completed","response":{"id":"resp_test","output":[]}}`, want: false},
		{name: "completed with output usage only", data: `{"type":"response.completed","response":{"id":"resp_test","usage":{"input_tokens":1,"output_tokens":2}}}`, want: false},
		{name: "completed with text", data: `{"type":"response.completed","response":{"id":"resp_test","output":[{"type":"message","content":[{"type":"output_text","text":"test output"}]}]}}`, want: true},
		{name: "done marker", data: `[DONE]`, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, openai.StreamDataStartsVisibleOutput(tt.data, tt.eventType))
		})
	}
}

func TestOpenAIResponsesTTFTStartsAtVisibleOutput(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		name := "native"
		if passthrough {
			name = "passthrough"
		}
		t.Run(name, func(t *testing.T) {
			result := runSyntheticVisibleTTFTStream(t, passthrough, 120*time.Millisecond, 0,
				`{"type":"response.output_text.delta","delta":"test output"}`)
			require.NotNil(t, result.FirstTokenMs)
			require.GreaterOrEqual(t, *result.FirstTokenMs, 100)
		})
	}
}

func TestOpenAIResponsesTTFTStartsAtCompletedImage(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		name := "native"
		if passthrough {
			name = "passthrough"
		}
		t.Run(name, func(t *testing.T) {
			result := runSyntheticVisibleTTFTStream(t, passthrough, 120*time.Millisecond, 0,
				`{"type":"response.output_item.done","item":{"id":"item_test","type":"image_generation_call","result":"dGVzdA=="}}`)
			require.NotNil(t, result.FirstTokenMs)
			require.GreaterOrEqual(t, *result.FirstTokenMs, 100)
		})
	}
}

func TestOpenAINativeMetadataDoesNotDisarmFirstOutputTimeout(t *testing.T) {

	svc := newResponsesFixture(responsesFixtureInputs{readers: newHTTPReadersFixture(&gatewaytestkit.FastPolicySettingsRepo{Values: map[string]string{gateway.SettingKeyOpenAITTFTMode: gateway.OpenAITTFTModeVisible}}, nil), options: &responsesFixtureOptions{Response: OpenAIResponseOptions{MaxLineSize: openAIResponseDefaultMaxLineSize, OpenAIFirstOutputTimeoutSeconds: 1}}})
	reader, writer := io.Pipe()
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		defer func() { _ = writer.Close() }()
		_, _ = io.WriteString(writer, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_test\"}}\n\n")
		_, _ = io.WriteString(writer, "data: {\"type\":\"response.output_item.added\",\"item\":{\"id\":\"item_test\",\"type\":\"reasoning\",\"summary\":[]}}\n\n")
		time.Sleep(1200 * time.Millisecond)
	}()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: reader}
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Name: "account_test", Platform: capability.PlatformOpenAI}}

	_, err := svc.Output.Stream(context.Background(), resp, c, account, time.Now(), "test-model", "test-model", "")
	var failoverErr *forwardcore.UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.True(t, failoverErr.SafeToFailoverAfterWrite)
	require.Empty(t, recorder.Body.String())
	select {
	case <-writerDone:
	case <-time.After(time.Second):
		t.Fatal("synthetic upstream writer did not exit")
	}
}

func runSyntheticVisibleTTFTStream(t *testing.T, passthrough bool, visibleDelay time.Duration, timeoutSeconds int, visibleEvent string) *responseupstream.StreamingResult {
	t.Helper()

	svc := newResponsesFixture(responsesFixtureInputs{readers: newHTTPReadersFixture(&gatewaytestkit.FastPolicySettingsRepo{Values: map[string]string{gateway.SettingKeyOpenAITTFTMode: gateway.OpenAITTFTModeVisible}}, nil), options: &responsesFixtureOptions{Response: OpenAIResponseOptions{MaxLineSize: openAIResponseDefaultMaxLineSize, OpenAIFirstOutputTimeoutSeconds: timeoutSeconds}}})
	reader, writer := io.Pipe()
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		defer func() { _ = writer.Close() }()
		_, _ = io.WriteString(writer, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_test\"}}\n\n")
		_, _ = io.WriteString(writer, "data: {\"type\":\"response.output_item.added\",\"item\":{\"id\":\"item_test\",\"type\":\"reasoning\",\"summary\":[]}}\n\n")
		time.Sleep(visibleDelay)
		_, _ = io.WriteString(writer, "data: "+visibleEvent+"\n\n")
		_, _ = io.WriteString(writer, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_test\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
	}()

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: reader}
	account := &gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1, Name: "account_test", Platform: capability.PlatformOpenAI}}
	started := time.Now()

	var result *responseupstream.StreamingResult
	var err error
	if passthrough {
		var passthroughResult *responseupstream.StreamingResult
		passthroughResult, err = svc.Output.PassthroughStream(context.Background(), resp, c, account, started, "test-model", "test-model")
		if passthroughResult != nil {
			result = &responseupstream.StreamingResult{FirstTokenMs: passthroughResult.FirstTokenMs}
		}
	} else {
		result, err = svc.Output.Stream(context.Background(), resp, c, account, started, "test-model", "test-model", "")
	}
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(t, recorder.Body.String(), `"type":"response.output_item.added"`)
	require.Contains(t, recorder.Body.String(), visibleEvent)
	select {
	case <-writerDone:
	case <-time.After(time.Second):
		t.Fatal("synthetic upstream writer did not exit")
	}
	return result
}
