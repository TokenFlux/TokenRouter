package openai

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

// TestReadCompatBufferedTerminalRetainsImageAccounting reports image usage before
// a terminal with missing or partial usage. Both image buckets must reach billing
// alongside accumulated text and cache counts after client conversion finishes.
func TestReadCompatBufferedTerminalRetainsImageAccounting(t *testing.T) {
	for _, terminalUsage := range []string{"", `,"usage":{"output_tokens":10}`} {
		t.Run(terminalUsage, func(t *testing.T) {
			body := "data: " + `{"type":"response.in_progress","response":{"usage":{"input_tokens":100,"output_tokens":3,"input_tokens_details":{"cached_tokens":20,"image_tokens":40},"output_tokens_details":{"image_tokens":2}}}}` + "\n\n" +
				"data: " + `{"type":"response.completed","response":{"id":"resp_image","status":"completed","output":[]` + terminalUsage + `}}` + "\n\n"
			response := &http.Response{Body: io.NopCloser(strings.NewReader(body))}
			final, usage, _, err := ReadCompatBufferedTerminal(response, CompatBufferedOptions{
				MaxLineSize:      1 << 20,
				StreamInterval:   func() time.Duration { return 0 },
				RestoreToolNames: func(body []byte) []byte { return body },
				Observe:          func([]byte, string) {},
				Log: func(message string, err error, _ time.Duration) {
					t.Errorf("%s: %v", message, err)
				},
			})
			require.NoError(t, err)
			require.Equal(t, 100, usage.InputTokens)
			require.Equal(t, 20, usage.CacheReadInputTokens)
			require.Equal(t, 40, usage.ImageInputTokens)
			require.Equal(t, 2, usage.ImageOutputTokens)
			require.Equal(t, usage.InputTokens, final.Usage.InputTokens)
			require.Equal(t, usage.OutputTokens, final.Usage.OutputTokens)
		})
	}
}

// TestReadCompatBufferedTerminalRetainsSparseCacheBreakdown supplies cache usage
// before a terminal containing output alone. The returned response and billing
// snapshot must agree without losing earlier input or charging writes twice.
func TestReadCompatBufferedTerminalRetainsSparseCacheBreakdown(t *testing.T) {
	body := "data: " + `{"type":"response.in_progress","response":{"usage":{"input_tokens":100,"output_tokens":3,"cached_tokens":60,"cache_write_tokens":20}}}` + "\n\n" +
		"data: " + `{"type":"response.completed","response":{"id":"resp_cache","status":"completed","output":[],"usage":{"output_tokens":10}}}` + "\n\n"
	response := &http.Response{Body: io.NopCloser(strings.NewReader(body))}
	final, usage, _, err := ReadCompatBufferedTerminal(response, CompatBufferedOptions{
		MaxLineSize: 1 << 20, StreamInterval: func() time.Duration { return 0 },
		RestoreToolNames: func(body []byte) []byte { return body }, Observe: func([]byte, string) {},
		Log: func(message string, err error, _ time.Duration) { t.Errorf("%s: %v", message, err) },
	})
	require.NoError(t, err)
	require.Equal(t, 100, final.Usage.InputTokens)
	require.Equal(t, 110, final.Usage.TotalTokens)
	require.Equal(t, 60, final.Usage.InputTokensDetails.CachedTokens)
	require.Equal(t, 20, final.Usage.CacheCreationInputTokens)
	require.Equal(t, protocolopenai.CopyForwardUsage(final.Usage), usage)
}

// TestOpenAICompatTerminalResponseSynthesizesBareError supplies a bare
// Responses error without a nested response object. It checks synthesis of
// failed status, code, and message for buffered compatibility output.
func TestOpenAICompatTerminalResponseSynthesizesBareError(t *testing.T) {
	t.Parallel()

	payload := []byte(`{"type":"error","code":"upstream_error","error":{"message":"provider failed"}}`)
	event := &protocolopenai.ResponsesStreamEvent{Type: "error", Code: "upstream_error"}
	response := CompatTerminalResponse(event, payload)
	require.NotNil(t, response)
	require.Equal(t, "failed", response.Status)
	require.Equal(t, "upstream_error", response.Error.Code)
	require.Equal(t, "provider failed", response.Error.Message)
}

// TestReadCompatBufferedTerminalFrameEndings combines text and tool deltas with
// several final-frame shapes terminated by blank lines or EOF. It verifies
// usage precedence, output accumulation, tool restoration before observation,
// and the distinct malformed-frame logging behavior.
func TestReadCompatBufferedTerminalFrameEndings(t *testing.T) {
	prefix := "data: " + `{"type":"response.output_text.delta","delta":"hello","usage":{"input_tokens":2,"output_tokens":1}}` + "\n\n" +
		"data: " + `{"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","call_id":"call_1","name":"tool_alias"}}` + "\n\n" +
		"data: " + `{"type":"response.function_call_arguments.delta","output_index":1,"delta":"{}"}` + "\n\n"
	tests := []struct {
		name         string                            // buffered frame case description
		frame        string                            // final SSE frame fixture
		eventType    string                            // expected observed event type
		wantResponse *protocolopenai.ResponsesResponse // expected terminal response object
		wantUsage    protocolopenai.ForwardUsage       // expected final token usage
		wantText     string                            // expected accumulated output text
		malformed    bool                              // malformed final frame flag
	}{
		{
			name: "nested usage takes precedence",
			frame: "event: response.completed\ndata: " +
				`{"usage":{"input_tokens":100,"output_tokens":200},"response":{"id":"resp_1","status":"completed",` +
				`"usage":{"input_tokens":7,"output_tokens":11,"input_tokens_details":{"cached_tokens":2}}}}`,
			eventType: "response.completed",
			wantResponse: &protocolopenai.ResponsesResponse{
				ID: "resp_1", Status: "completed",
				Usage: &protocolopenai.ResponsesUsage{
					InputTokens: 7, OutputTokens: 11, TotalTokens: 18,
					InputTokensDetails: &protocolopenai.ResponsesInputTokensDetails{CachedTokens: 2},
				},
			},
			wantUsage: protocolopenai.ForwardUsage{InputTokens: 7, OutputTokens: 11, CacheReadInputTokens: 2},
			wantText:  "hello",
		},
		{
			name: "top level usage fills response",
			frame: "data: " + `{"type":"response.done","usage":{"input_tokens":3,"output_tokens":5},` +
				`"response":{"id":"resp_1","status":"completed"}}`,
			eventType: "response.done",
			wantResponse: &protocolopenai.ResponsesResponse{
				ID: "resp_1", Status: "completed",
				Usage: &protocolopenai.ResponsesUsage{InputTokens: 3, OutputTokens: 5, TotalTokens: 8},
			},
			wantUsage: protocolopenai.ForwardUsage{InputTokens: 3, OutputTokens: 5},
			wantText:  "hello",
		},
		{
			name: "zero nested usage",
			frame: "data: " + `{"type":"response.completed","usage":{"input_tokens":100,"output_tokens":200},` +
				`"response":{"id":"resp_1","status":"completed","usage":{"input_tokens":0,"output_tokens":0}}}`,
			eventType: "response.completed",
			wantResponse: &protocolopenai.ResponsesResponse{
				ID: "resp_1", Status: "completed", Usage: &protocolopenai.ResponsesUsage{},
			},
			wantText: "hello",
		},
		{
			name:      "unterminated text delta",
			frame:     "event: response.output_text.delta\ndata: " + `{"delta":" world","usage":{"input_tokens":13,"output_tokens":8}}`,
			eventType: "response.output_text.delta",
			wantUsage: protocolopenai.ForwardUsage{InputTokens: 13, OutputTokens: 8},
			wantText:  "hello world",
		},
		{
			name:      "bare error",
			frame:     "data: " + `{"type":"error","code":"upstream_error","error":{"message":"provider failed"}}`,
			eventType: "error",
			wantResponse: &protocolopenai.ResponsesResponse{
				Status: "failed",
				Error:  &protocolopenai.ResponsesError{Code: "upstream_error", Message: "provider failed"},
			},
			wantUsage: protocolopenai.ForwardUsage{InputTokens: 2, OutputTokens: 1},
			wantText:  "hello",
		},
		{
			name:      "malformed JSON",
			frame:     "data: {",
			wantUsage: protocolopenai.ForwardUsage{InputTokens: 2, OutputTokens: 1},
			wantText:  "hello",
			malformed: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, ending := range []struct {
				name   string // buffered frame case description
				suffix string // final frame ending bytes
			}{
				{name: "blank line", suffix: "\n\n"},
				{name: "EOF"},
			} {
				t.Run(ending.name, func(t *testing.T) {
					var steps []string
					resp := &http.Response{Body: io.NopCloser(strings.NewReader(prefix + tt.frame + ending.suffix))}
					response, usage, acc, err := ReadCompatBufferedTerminal(resp, CompatBufferedOptions{
						MaxLineSize:    1024 * 1024,
						StreamInterval: func() time.Duration { return 0 },
						RestoreToolNames: func(payload []byte) []byte {
							steps = append(steps, "restore")
							return bytes.ReplaceAll(payload, []byte("tool_alias"), []byte("lookup"))
						},
						Observe: func(payload []byte, eventType string) {
							steps = append(steps, "observe:"+eventType)
							require.NotContains(t, string(payload), "tool_alias")
						},
						Log: func(message string, err error, interval time.Duration) {
							steps = append(steps, "log:"+message)
							require.Error(t, err)
							require.Zero(t, interval)
						},
					})
					require.NoError(t, err)
					require.Equal(t, tt.wantResponse, response)
					require.Equal(t, tt.wantUsage, usage)
					require.Equal(t, []protocolopenai.ResponsesOutput{
						{Type: "message", Role: "assistant", Content: []protocolopenai.ResponsesContentPart{{Type: "output_text", Text: tt.wantText}}},
						{Type: "function_call", CallID: "call_1", Name: "lookup", Arguments: "{}"},
					}, acc.BuildOutput())
					wantSteps := []string{
						"restore", "observe:response.output_text.delta",
						"restore", "observe:response.output_item.added",
						"restore", "observe:response.function_call_arguments.delta",
						"restore",
					}
					if tt.malformed {
						if ending.suffix != "" {
							wantSteps = append(wantSteps, "log:failed to parse event")
						}
					} else {
						wantSteps = append(wantSteps, "observe:"+tt.eventType)
					}
					require.Equal(t, wantSteps, steps)
				})
			}
		})
	}
}
