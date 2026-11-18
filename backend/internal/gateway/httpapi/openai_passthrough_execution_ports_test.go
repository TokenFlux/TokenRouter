package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// TestOpenAIResponsesRejectedFieldRetryStateRejectsDuplicateBodyAndCap repeats
// initial and repaired request bodies through one state. It checks duplicate
// rejection and admission stopping after the configured number of unique
// repair variants.
func TestOpenAIResponsesRejectedFieldRetryStateRejectsDuplicateBodyAndCap(t *testing.T) {
	initialBody := []byte(`{"model":"gpt-5.5"}`)
	state := openai.NewOpenAIResponsesRejectedFieldRetryState(initialBody)

	require.False(t, state.Allow(initialBody))
	for attempt := range openai.MaxResponsesRejectedFieldRetries {
		nextBody := []byte(fmt.Sprintf(`{"model":"gpt-5.5","variant":%d}`, attempt))
		require.True(t, state.Allow(nextBody))
		require.False(t, state.Allow(nextBody))
	}
	require.False(t, state.Allow([]byte(`{"model":"gpt-5.5","variant":"overflow"}`)))
}

// TestNormalizeOpenAIResponsesRejectedFieldRetryBodyRejectsAmbiguousErrors
// supplies the listed conflicting or ineligible error paths. It checks no
// adopted repair and nil retry body for each ambiguous namespace/token-control
// fixture.
func TestNormalizeOpenAIResponsesRejectedFieldRetryBodyRejectsAmbiguousErrors(t *testing.T) {
	tests := []struct {
		name         string // fixture case name
		body         []byte // client request JSON bytes
		responseBody []byte // upstream rejection JSON bytes
	}{
		{
			name:         "namespace belongs to message",
			body:         []byte(`{"input":[{"type":"message","namespace":"keep"}]}`),
			responseBody: []byte(`{"error":{"code":"unknown_parameter","message":"Unknown parameter: 'input[0].namespace'.","param":"input[0].namespace"}}`),
		},
		{
			name:         "max output tokens only mentioned",
			body:         []byte(`{"max_output_tokens":4096}`),
			responseBody: []byte(`{"error":{"code":"invalid_request_error","message":"max_output_tokens must be positive","param":"max_output_tokens"}}`),
		},
		{
			name:         "structured param overrides namespace mention",
			body:         []byte(`{"input":[{"type":"function_call","namespace":"keep","arguments":"{}"}]}`),
			responseBody: []byte(`{"error":{"code":"unknown_parameter","message":"Unknown parameter: 'input[0].namespace'.","param":"tools"}}`),
		},
		{
			name:         "nested max output tokens param is not top level",
			body:         []byte(`{"max_output_tokens":4096,"input":[{"type":"message","content":{"max_output_tokens":"keep"}}]}`),
			responseBody: []byte(`{"error":{"code":"unknown_parameter","message":"Unknown parameter: input[0].content.max_output_tokens","param":"input[0].content.max_output_tokens"}}`),
		},
		{
			name:         "structured target conflicts with message target",
			body:         []byte(`{"max_output_tokens":4096,"truncation":"auto"}`),
			responseBody: []byte(`{"error":{"code":"unsupported_parameter","message":"Unsupported parameter: truncation.","param":"max_output_tokens"}}`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			retryBody, _, changed, err := openai.NormalizeOpenAIResponsesRejectedFieldRetryBody(http.StatusBadRequest, tt.body, tt.responseBody)
			require.NoError(t, err)
			require.False(t, changed)
			require.Nil(t, retryBody)
		})
	}
}

// TestNormalizeOpenAIResponsesRejectedFieldRetryBodyRepairsAutomationMissingRootType
// explicitly rejects an automation tool root schema type. It checks an adopted
// object-type repair and the exact root-type rejection reason.
func TestNormalizeOpenAIResponsesRejectedFieldRetryBodyRepairsAutomationMissingRootType(t *testing.T) {
	body := []byte(`{"tools":[{"type":"function","name":"automation_update","parameters":{"oneOf":[{"type":"object"},{"type":"object","properties":{}}]}}]}`)
	responseBody := []byte(`{"error":{"code":"invalid_function_parameters","message":"Invalid schema for function 'automation_update': got 'type: \"None\"'.","param":"tools[0].parameters"}}`)

	retryBody, reason, changed, err := openai.NormalizeOpenAIResponsesRejectedFieldRetryBody(http.StatusBadRequest, body, responseBody)

	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "tool parameter root type rejection", reason)
	require.Equal(t, "object", gjson.GetBytes(retryBody, "tools.0.parameters.type").String())
}

// TestNormalizeOpenAIResponsesRejectedFieldRetryBodyDoesNotGuessAutomationRootType
// varies the rejected path, classification and message around an automation
// schema. It checks unchanged adoption state and absent retry bytes for these
// unsupported signals.
func TestNormalizeOpenAIResponsesRejectedFieldRetryBodyDoesNotGuessAutomationRootType(t *testing.T) {
	body := []byte(`{"tools":[{"type":"function","name":"automation_update","parameters":{"oneOf":[{"type":"object"}]}}]}`)
	tests := []string{
		`{"error":{"code":"invalid_function_parameters","message":"got type: \"None\"","param":"metadata.parameters"}}`,
		`{"error":{"code":"invalid_request_error","message":"got type: \"None\"","param":"tools[0].parameters"}}`,
		`{"error":{"code":"invalid_function_parameters","message":"expected an object","param":"tools[0].parameters"}}`,
	}
	for _, response := range tests {
		retryBody, _, changed, err := openai.NormalizeOpenAIResponsesRejectedFieldRetryBody(http.StatusBadRequest, body, []byte(response))
		require.NoError(t, err)
		require.False(t, changed)
		require.Nil(t, retryBody)
	}
}

// TestNormalizeOpenAIResponsesRejectedFieldRetryBodyFindsNamespacePathInMessage
// names one rejected namespace after another accepted index. It checks removal
// of only the rejected function-call namespace and retention of the other
// value.
func TestNormalizeOpenAIResponsesRejectedFieldRetryBodyFindsNamespacePathInMessage(t *testing.T) {
	body := []byte(`{"input":[{"type":"function_call","namespace":"keep","arguments":"{}"},{"type":"function_call","namespace":"remove","arguments":"{}"}]}`)
	responseBody := []byte(`{"error":{"code":"unknown_parameter","message":"input[0] was accepted; Unknown parameter: 'input[1].namespace'."}}`)

	retryBody, _, changed, err := openai.NormalizeOpenAIResponsesRejectedFieldRetryBody(http.StatusBadRequest, body, responseBody)

	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "keep", gjson.GetBytes(retryBody, "input.0.namespace").String())
	require.False(t, gjson.GetBytes(retryBody, "input.1.namespace").Exists())
}

// TestNormalizeOpenAIResponsesRejectedFieldRetryBodyBindsNamespacePathToRejectionPhrase
// mentions an accepted namespace before its rejection phrase. It checks the
// repair follows the rejected index and retains the accepted namespace.
func TestNormalizeOpenAIResponsesRejectedFieldRetryBodyBindsNamespacePathToRejectionPhrase(t *testing.T) {
	body := []byte(`{"input":[{"type":"function_call","namespace":"keep","arguments":"{}"},{"type":"function_call","namespace":"remove","arguments":"{}"}]}`)
	responseBody := []byte(`{"error":{"code":"unknown_parameter","message":"input[0].namespace is supported; Unknown parameter: input[1].namespace."}}`)

	retryBody, _, changed, err := openai.NormalizeOpenAIResponsesRejectedFieldRetryBody(http.StatusBadRequest, body, responseBody)

	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "keep", gjson.GetBytes(retryBody, "input.0.namespace").String())
	require.False(t, gjson.GetBytes(retryBody, "input.1.namespace").Exists())
}

// TestNormalizeOpenAIResponsesRejectedFieldRetryBodyKeepsRequiredNamespace
// receives a missing-identity message with an inconsistent compatibility code.
// The supplied namespace remains intact and no recovery request is admitted.
func TestNormalizeOpenAIResponsesRejectedFieldRetryBodyKeepsRequiredNamespace(t *testing.T) {
	body := []byte(`{"input":[{"type":"function_call","namespace":"browser","name":"js","arguments":"{}"}]}`)
	response := []byte(`{"error":{"code":"unknown_parameter","param":"input[0].namespace","message":"Missing namespace for function_call 'js'. Round-trip the model's function_call item with its namespace field included."}}`)
	retry, _, changed, err := openai.NormalizeOpenAIResponsesRejectedFieldRetryBody(http.StatusBadRequest, body, response)
	require.NoError(t, err)
	require.False(t, changed)
	require.Nil(t, retry)
}

// TestNormalizeOpenAIResponsesRejectedFieldRetryBodyDoesNotTreatMaxOutputTokensSuggestionAsRejection
// suggests max_output_tokens after rejecting the different max_tokens control.
// It checks no adopted change and absent retry bytes for that unrelated
// suggested parameter.
func TestNormalizeOpenAIResponsesRejectedFieldRetryBodyDoesNotTreatMaxOutputTokensSuggestionAsRejection(t *testing.T) {
	body := []byte(`{"max_tokens":4096,"max_output_tokens":2048}`)
	responseBody := []byte(`{"error":{"code":"unknown_parameter","message":"Unknown parameter: max_tokens. Use max_output_tokens instead."}}`)

	retryBody, _, changed, err := openai.NormalizeOpenAIResponsesRejectedFieldRetryBody(http.StatusBadRequest, body, responseBody)

	require.NoError(t, err)
	require.False(t, changed)
	require.Nil(t, retryBody)
}

// TestNormalizeOpenAIResponsesRejectedFieldRetryBodyBindsMaxOutputTokensToRejectionPhrase
// explicitly rejects top-level max_output_tokens through HTTP 400
// unsupported-parameter evidence. It checks changed adoption state and removal
// of that exact field from the retry body.
func TestNormalizeOpenAIResponsesRejectedFieldRetryBodyBindsMaxOutputTokensToRejectionPhrase(t *testing.T) {
	body := []byte(`{"max_output_tokens":2048}`)
	responseBody := []byte(`{"error":{"code":"unsupported_parameter","message":"Unsupported parameter: max_output_tokens."}}`)

	retryBody, _, changed, err := openai.NormalizeOpenAIResponsesRejectedFieldRetryBody(http.StatusBadRequest, body, responseBody)

	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, gjson.GetBytes(retryBody, "max_output_tokens").Exists())
}

// TestNormalizeOpenAIResponsesRejectedFieldRetryBodyRemovesExactIndexedStatus
// rejects reasoning status through structured or message evidence. It checks
// removal at the selected index and retention of the different message item's
// status.
func TestNormalizeOpenAIResponsesRejectedFieldRetryBodyRemovesExactIndexedStatus(t *testing.T) {
	body := []byte(`{"input":[{"type":"message","status":"keep","content":"one"},{"type":"reasoning","status":"remove","summary":[]}]}`)
	responses := []struct {
		name string // fixture case name
		body []byte // upstream rejection JSON bytes
	}{
		{
			name: "structured param",
			body: []byte(`{"error":{"code":"unknown_parameter","message":"Unknown parameter: 'input[1].status'.","param":"input[1].status"}}`),
		},
		{
			name: "message param",
			body: []byte(`{"error":{"code":"unsupported_parameter","message":"Unsupported parameter: input[1].status."}}`),
		},
	}
	for _, tt := range responses {
		t.Run(tt.name, func(t *testing.T) {
			retryBody, _, changed, err := openai.NormalizeOpenAIResponsesRejectedFieldRetryBody(http.StatusBadRequest, body, tt.body)
			require.NoError(t, err)
			require.True(t, changed)
			require.Equal(t, "keep", gjson.GetBytes(retryBody, "input.0.status").String())
			require.False(t, gjson.GetBytes(retryBody, "input.1.status").Exists())
		})
	}
}

// TestNormalizeOpenAIResponsesRejectedFieldRetryBodyNormalizesExactNullContent
// applies one indexed null-content rejection to the listed item shapes. It
// checks message empty-string repair, reasoning removal and no adoption for
// unknown/non-null items.
func TestNormalizeOpenAIResponsesRejectedFieldRetryBodyNormalizesExactNullContent(t *testing.T) {
	tests := []struct {
		name string // fixture case name
		body []byte // client request JSON bytes
		// wantChange, wantValue and wantExists describe an adopted repair together.
		// Value and existence are checked only after change is true; unchanged
		// cases instead require an absent retry body.
		wantChange bool   // expected adopted body repair
		wantValue  string // expected normalized content string
		wantExists bool   // expected normalized content presence
	}{
		{
			name:       "message becomes empty string",
			body:       []byte(`{"input":[{"type":"message","role":"assistant","content":null}]}`),
			wantChange: true,
			wantValue:  "",
			wantExists: true,
		},
		{
			name:       "reasoning content is removed",
			body:       []byte(`{"input":[{"type":"reasoning","content":null,"summary":[]}]}`),
			wantChange: true,
			wantExists: false,
		},
		{
			name:       "unknown item is unchanged",
			body:       []byte(`{"input":[{"type":"future_item","content":null}]}`),
			wantChange: false,
		},
		{
			name:       "non null content is unchanged",
			body:       []byte(`{"input":[{"type":"message","content":"keep"}]}`),
			wantChange: false,
		},
	}
	responseBody := []byte(`{"error":{"code":"invalid_type","message":"Invalid type for 'input[0].content': expected one of a string or a list of input items, but got null instead.","param":"input[0].content"}}`)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			retryBody, _, changed, err := openai.NormalizeOpenAIResponsesRejectedFieldRetryBody(http.StatusBadRequest, tt.body, responseBody)
			require.NoError(t, err)
			require.Equal(t, tt.wantChange, changed)
			if !tt.wantChange {
				require.Nil(t, retryBody)
				return
			}
			content := gjson.GetBytes(retryBody, "input.0.content")
			require.Equal(t, tt.wantExists, content.Exists())
			if tt.wantExists {
				require.Equal(t, tt.wantValue, content.String())
				require.Equal(t, gjson.String, content.Type)
			}
		})
	}
}

// TestNormalizeOpenAIResponsesRejectedFieldRetryBodyRemovesExactReasoningContentAboveMaximumZero
// rejects one reasoning content array with maximum zero. It checks the exact
// repair reason, removed reasoning content and retained neighboring message
// text.
func TestNormalizeOpenAIResponsesRejectedFieldRetryBodyRemovesExactReasoningContentAboveMaximumZero(t *testing.T) {
	body := []byte(`{"input":[{"type":"reasoning","content":[{"type":"reasoning_text","text":"remove"}],"summary":[]},{"type":"message","content":[{"type":"input_text","text":"keep"}]}]}`)
	responseBody := []byte(`{"error":{"code":"array_above_max_length","message":"Invalid 'input[0].content': array too long. Expected an array with maximum length 0, but got an array with length 1 instead.","param":"input[0].content","type":"invalid_request_error"}}`)

	retryBody, reason, changed, err := openai.NormalizeOpenAIResponsesRejectedFieldRetryBody(http.StatusBadRequest, body, responseBody)

	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "indexed reasoning content maximum-length rejection", reason)
	require.False(t, gjson.GetBytes(retryBody, "input.0.content").Exists())
	require.Equal(t, "keep", gjson.GetBytes(retryBody, "input.1.content.0.text").String())
}

// TestNormalizeOpenAIResponsesRejectedFieldRetryBodyRejectsUnsafeReasoningMaximumZeroMutations
// supplies nonreasoning, conflicting-index and wrong-code fixtures. It checks
// that each ambiguous rejection yields false adoption state and nil retry
// bytes.
func TestNormalizeOpenAIResponsesRejectedFieldRetryBodyRejectsUnsafeReasoningMaximumZeroMutations(t *testing.T) {
	tests := []struct {
		name         string // fixture case name
		body         []byte // client request JSON bytes
		responseBody []byte // upstream rejection JSON bytes
	}{
		{
			name:         "message content is not reasoning",
			body:         []byte(`{"input":[{"type":"message","content":[{"type":"input_text","text":"keep"}]}]}`),
			responseBody: []byte(`{"error":{"code":"array_above_max_length","message":"Invalid 'input[0].content': array too long. Expected an array with maximum length 0, but got an array with length 1 instead.","param":"input[0].content"}}`),
		},
		{
			name:         "structured param and message disagree",
			body:         []byte(`{"input":[{"type":"reasoning","content":[{"text":"keep"}]},{"type":"reasoning","content":[{"text":"keep too"}]}]}`),
			responseBody: []byte(`{"error":{"code":"array_above_max_length","message":"Invalid 'input[1].content': array too long. Expected an array with maximum length 0, but got an array with length 1 instead.","param":"input[0].content"}}`),
		},
		{
			name:         "different error code",
			body:         []byte(`{"input":[{"type":"reasoning","content":[{"text":"keep"}]}]}`),
			responseBody: []byte(`{"error":{"code":"invalid_request_error","message":"Invalid 'input[0].content': array too long. Expected an array with maximum length 0, but got an array with length 1 instead.","param":"input[0].content"}}`),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			retryBody, _, changed, err := openai.NormalizeOpenAIResponsesRejectedFieldRetryBody(http.StatusBadRequest, tt.body, tt.responseBody)
			require.NoError(t, err)
			require.False(t, changed)
			require.Nil(t, retryBody)
		})
	}
}

// TestNormalizeOpenAIResponsesRejectedFieldRetryBodyRemovesExplicitlyRejectedTopLevelTruncation
// supplies structured and message-only unsupported-control evidence. It checks
// the exact repair reason, removed truncation field and retained input text.
func TestNormalizeOpenAIResponsesRejectedFieldRetryBodyRemovesExplicitlyRejectedTopLevelTruncation(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","truncation":"auto","input":"keep"}`)
	responses := [][]byte{
		[]byte(`{"error":{"code":"unsupported_parameter","message":"Unsupported parameter: 'truncation'.","param":"truncation"}}`),
		[]byte(`{"error":{"code":"unknown_parameter","message":"Unknown parameter: truncation."}}`),
	}
	for _, responseBody := range responses {
		retryBody, reason, changed, err := openai.NormalizeOpenAIResponsesRejectedFieldRetryBody(http.StatusBadRequest, body, responseBody)
		require.NoError(t, err)
		require.True(t, changed)
		require.Equal(t, "truncation parameter rejection", reason)
		require.False(t, gjson.GetBytes(retryBody, "truncation").Exists())
		require.Equal(t, "keep", gjson.GetBytes(retryBody, "input").String())
	}
}

// TestNormalizeOpenAIResponsesRejectedFieldRetryBodyRejectsUnsafeIndexedMutations
// tests nested paths, missing indexes, nonrejection mentions and mismatched
// null evidence. It checks all listed fixtures leave repair unadopted with nil
// retry bytes.
func TestNormalizeOpenAIResponsesRejectedFieldRetryBodyRejectsUnsafeIndexedMutations(t *testing.T) {
	tests := []struct {
		name         string // fixture case name
		body         []byte // client request JSON bytes
		responseBody []byte // upstream rejection JSON bytes
	}{
		{
			name:         "nested status path",
			body:         []byte(`{"input":[{"type":"message","content":{"status":"keep"}}]}`),
			responseBody: []byte(`{"error":{"code":"unknown_parameter","message":"Unknown parameter: input[0].content.status.","param":"input[0].content.status"}}`),
		},
		{
			name:         "status index out of bounds",
			body:         []byte(`{"input":[{"type":"message","status":"keep"}]}`),
			responseBody: []byte(`{"error":{"code":"unknown_parameter","message":"Unknown parameter: input[4].status.","param":"input[4].status"}}`),
		},
		{
			name:         "status path only mentioned",
			body:         []byte(`{"input":[{"type":"message","status":"keep"}]}`),
			responseBody: []byte(`{"error":{"code":"invalid_request_error","message":"input[0].status must be completed","param":"input[0].status"}}`),
		},
		{
			name:         "content param and message disagree",
			body:         []byte(`{"input":[{"type":"message","content":null},{"type":"message","content":null}]}`),
			responseBody: []byte(`{"error":{"code":"invalid_type","message":"Invalid type for input[1].content: got null instead.","param":"input[0].content"}}`),
		},
		{
			name:         "content error only mentions null",
			body:         []byte(`{"input":[{"type":"message","content":null}]}`),
			responseBody: []byte(`{"error":{"code":"invalid_type","message":"content cannot be null","param":"input[0].content"}}`),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			retryBody, _, changed, err := openai.NormalizeOpenAIResponsesRejectedFieldRetryBody(http.StatusBadRequest, tt.body, tt.responseBody)
			require.NoError(t, err)
			require.False(t, changed)
			require.Nil(t, retryBody)
		})
	}
}

// TestNormalizeOpenAIResponsesRejectedFieldRetryBodyRemovesModelRejectedPromptCacheBreakpoint
// rejects top-level and indexed model cache hints. It checks exact reason,
// removed selected path and retained unrelated input or breakpoint.
func TestNormalizeOpenAIResponsesRejectedFieldRetryBodyRemovesModelRejectedPromptCacheBreakpoint(t *testing.T) {
	tests := []struct {
		name         string // fixture case name
		body         []byte // client request JSON bytes
		responseBody []byte // upstream rejection JSON bytes
		removedPath  string // expected removed JSON path
		preserved    string // expected retained JSON path
		reason       string // expected repair diagnostic reason
	}{
		{
			name:         "top level",
			body:         []byte(`{"model":"gpt-5.6-sol","prompt_cache_breakpoint":{"type":"message_start"},"input":"hello"}`),
			responseBody: []byte(`{"error":{"code":"invalid_parameter","message":"prompt_cache_breakpoint is not supported on this model","param":"prompt_cache_breakpoint"}}`),
			removedPath:  "prompt_cache_breakpoint",
			preserved:    "input",
			reason:       "prompt_cache_breakpoint parameter rejection",
		},
		{
			name:         "indexed path from message",
			body:         []byte(`{"input":[{"type":"message","prompt_cache_breakpoint":{"type":"message_start"}},{"type":"message","prompt_cache_breakpoint":{"type":"message_end"}}]}`),
			responseBody: []byte(`{"error":{"code":"invalid_parameter","message":"input[1].prompt_cache_breakpoint is not supported on this model"}}`),
			removedPath:  "input.1.prompt_cache_breakpoint",
			preserved:    "input.0.prompt_cache_breakpoint",
			reason:       "indexed prompt_cache_breakpoint parameter rejection",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			retryBody, reason, changed, err := openai.NormalizeOpenAIResponsesRejectedFieldRetryBody(http.StatusBadRequest, tt.body, tt.responseBody)

			require.NoError(t, err)
			require.True(t, changed)
			require.Equal(t, tt.reason, reason)
			require.False(t, gjson.GetBytes(retryBody, tt.removedPath).Exists())
			require.True(t, gjson.GetBytes(retryBody, tt.preserved).Exists())
		})
	}
}

// TestNormalizeOpenAIResponsesRejectedFieldRetryBodyRejectsAmbiguousPromptCacheBreakpointErrors
// conflicts structured/message targets or names an out-of-range index. It
// checks no adopted repair and nil retry bytes for those fixtures.
func TestNormalizeOpenAIResponsesRejectedFieldRetryBodyRejectsAmbiguousPromptCacheBreakpointErrors(t *testing.T) {
	body := []byte(`{"prompt_cache_breakpoint":{"type":"message_start"},"input":[{"type":"message","prompt_cache_breakpoint":{"type":"message_end"}}]}`)
	tests := []struct {
		name         string // fixture case name
		responseBody []byte // upstream rejection JSON bytes
	}{
		{
			name:         "structured param disagrees",
			responseBody: []byte(`{"error":{"code":"invalid_parameter","message":"input[0].prompt_cache_breakpoint is not supported on this model","param":"prompt_cache_breakpoint"}}`),
		},
		{
			name:         "index out of bounds",
			responseBody: []byte(`{"error":{"code":"invalid_parameter","message":"input[4].prompt_cache_breakpoint is not supported on this model","param":"input[4].prompt_cache_breakpoint"}}`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			retryBody, _, changed, err := openai.NormalizeOpenAIResponsesRejectedFieldRetryBody(http.StatusBadRequest, body, tt.responseBody)

			require.NoError(t, err)
			require.False(t, changed)
			require.Nil(t, retryBody)
		})
	}
}

// TestNormalizeOpenAIResponsesRejectedFieldRetryBodyAcceptsEitherCacheModelRejectionSignal
// compares an explicit invalid-parameter code with model-unsupported wording.
// It checks adopted breakpoint removal and retained input under either
// supplied signal.
func TestNormalizeOpenAIResponsesRejectedFieldRetryBodyAcceptsEitherCacheModelRejectionSignal(t *testing.T) {
	tests := []struct {
		name         string // fixture case name
		responseBody []byte // upstream rejection JSON bytes
	}{
		{
			name:         "invalid parameter code",
			responseBody: []byte(`{"error":{"code":"invalid_parameter","message":"This optional cache hint cannot be used here","param":"prompt_cache_breakpoint"}}`),
		},
		{
			name:         "model rejection message",
			responseBody: []byte(`{"error":{"code":"invalid_request_error","message":"prompt_cache_breakpoint is not supported on this model","param":"prompt_cache_breakpoint"}}`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			retryBody, _, changed, err := openai.NormalizeOpenAIResponsesRejectedFieldRetryBody(http.StatusBadRequest, []byte(`{"prompt_cache_breakpoint":true,"input":"keep"}`), tt.responseBody)

			require.NoError(t, err)
			require.True(t, changed)
			require.False(t, gjson.GetBytes(retryBody, "prompt_cache_breakpoint").Exists())
			require.Equal(t, "keep", gjson.GetBytes(retryBody, "input").String())
		})
	}
}

// TestOpenAIResponsesRejectedFieldRetryStateAllowsPromptCacheBreakpointVariantOnce
// normalizes a rejected indexed cache hint. It checks adoption of that variant
// and one allowance followed by rejection of the repeated body.
func TestOpenAIResponsesRejectedFieldRetryStateAllowsPromptCacheBreakpointVariantOnce(t *testing.T) {
	body := []byte(`{"input":[{"prompt_cache_breakpoint":{"type":"message_start"}}]}`)
	responseBody := []byte(`{"error":{"code":"invalid_parameter","message":"input[0].prompt_cache_breakpoint is not supported on this model","param":"input[0].prompt_cache_breakpoint"}}`)
	state := openai.NewOpenAIResponsesRejectedFieldRetryState(body)

	retryBody, _, changed, err := openai.NormalizeOpenAIResponsesRejectedFieldRetryBody(http.StatusBadRequest, body, responseBody)
	require.NoError(t, err)
	require.True(t, changed)
	require.True(t, state.Allow(retryBody))
	require.False(t, state.Allow(retryBody))
}

// TestNormalizeOpenAIResponsesRejectedFieldRetryBodyClearsStatusForWholeType
// rejects status on one of ten tool_search_output items. It checks removal
// across that type, retained call identities and retained status on the
// different message type.
func TestNormalizeOpenAIResponsesRejectedFieldRetryBodyClearsStatusForWholeType(t *testing.T) {
	input := make([]string, 0, 12)
	for i := range 10 {
		input = append(input, `{"type":"tool_search_output","status":"completed","call_id":"call_`+strconv.Itoa(i)+`","tools":[]}`)
	}
	input = append(input, `{"type":"message","role":"user","status":"completed","content":"hi"}`)
	body := []byte(`{"input":[` + strings.Join(input, ",") + `]}`)

	responseBody := []byte(`{"error":{"code":"unknown_parameter","message":"Unknown parameter: 'input[7].status'.","param":"input[7].status"}}`)
	retryBody, reason, changed, err := openai.NormalizeOpenAIResponsesRejectedFieldRetryBody(http.StatusBadRequest, body, responseBody)
	require.NoError(t, err)
	require.True(t, changed)
	require.NotEmpty(t, reason)

	for i := range 10 {
		require.False(t, gjson.GetBytes(retryBody, "input."+strconv.Itoa(i)+".status").Exists(),
			"every tool_search_output must lose its status in a single retry, index %d did not", i)
		require.Equal(t, "call_"+strconv.Itoa(i), gjson.GetBytes(retryBody, "input."+strconv.Itoa(i)+".call_id").String(),
			"unrelated fields must survive")
	}
	require.Equal(t, "completed", gjson.GetBytes(retryBody, "input.10.status").String(),
		"a different item type keeps its status: the rejection only proves this type has none")
}

// TestNormalizeOpenAIResponsesRejectedFieldRetryBodyClearsUntypedStatusAtIndexOnly
// rejects one untyped item's status. It checks the indexed removal and
// retention of the other untyped item's status value.
func TestNormalizeOpenAIResponsesRejectedFieldRetryBodyClearsUntypedStatusAtIndexOnly(t *testing.T) {
	body := []byte(`{"input":[{"status":"keep_a"},{"status":"remove"}]}`)
	responseBody := []byte(`{"error":{"code":"unknown_parameter","message":"Unknown parameter: 'input[1].status'.","param":"input[1].status"}}`)

	retryBody, _, changed, err := openai.NormalizeOpenAIResponsesRejectedFieldRetryBody(http.StatusBadRequest, body, responseBody)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "keep_a", gjson.GetBytes(retryBody, "input.0.status").String())
	require.False(t, gjson.GetBytes(retryBody, "input.1.status").Exists())
}

// TestOpenAIResponsesRejectedFieldRetryStateForRequestAllowsSameTransformAcrossProviders
// creates two attempt states on one Gin request. It checks distinct state
// objects, shared budget and admission of the same repair once per state.
func TestOpenAIResponsesRejectedFieldRetryStateForRequestAllowsSameTransformAcrossProviders(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	initialBody := []byte(`{"model":"gpt-5.5","truncation":"auto"}`)
	retryBody := []byte(`{"model":"gpt-5.5"}`)

	providerA := openAIResponsesRejectedFieldRetryStateForRequest(c, initialBody)
	require.True(t, providerA.Allow(retryBody))
	require.False(t, providerA.Allow(retryBody), "one provider must not repeat the same transform")

	providerB := openAIResponsesRejectedFieldRetryStateForRequest(c, initialBody)
	require.NotSame(t, providerA, providerB)
	require.Same(t, providerA.Budget(), providerB.Budget())
	require.True(t, providerB.Allow(retryBody), "a failover provider must be allowed to apply the same transform")
}

// TestOpenAIResponsesRejectedFieldRetryStateForRequestSharesBoundedBudgetAcrossProviders
// consumes one repair from each request-linked attempt state. It checks the
// next state cannot admit another novel body after the shared allowance is
// exhausted.
func TestOpenAIResponsesRejectedFieldRetryStateForRequestSharesBoundedBudgetAcrossProviders(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	for attempt := range openai.MaxResponsesRejectedFieldRetries {
		state := openAIResponsesRejectedFieldRetryStateForRequest(c, []byte(fmt.Sprintf(`{"provider":%d}`, attempt)))
		require.True(t, state.Allow([]byte(`{"same":"retry"}`)))
	}
	overflow := openAIResponsesRejectedFieldRetryStateForRequest(c, []byte(`{"provider":"overflow"}`))
	require.False(t, overflow.Allow([]byte(`{"new":"retry"}`)))
}

// TestPassthroughSinkRecordsAppliedTurnState delivers headers before any heartbeat.
// The passthrough adapter must record the matching provider when its header
// reaches HTTP output, preserving ownership checks for a later provider switch.
func TestPassthroughSinkRecordsAppliedTurnState(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Request.Header.Set("session-id", "passthrough-applied")
	c.Set("api_key", &apikey.APIKey{ID: 7})
	origins := session.NewCodexTurnOrigins(time.Now)
	adapter := &openAIPassthroughExecutionAdapter{openAIMessagesExecutionAdapter: &openAIMessagesExecutionAdapter{
		c: c, provider: &gatewayprovider.ExecutionProvider{Record: providercore.Record{ID: 42}},
		s: &OpenAITextExecutor{Requests: &OpenAIRequests{Turns: &CodexTurnStateHeaders{Origins: origins}}},
	}}
	output := upstream.NewOutputContext(adapter.Sink())
	output.Writer.Header().Set(CodexTurnStateHeader, "delivered-state")
	_, err := output.Writer.Write([]byte("data: {}\n\n"))
	require.NoError(t, err)
	owner, known := origins.Owner("7\x00passthrough-applied")
	require.True(t, known)
	require.Equal(t, int64(42), owner)
	require.Equal(t, "delivered-state", rec.Result().Header.Get(CodexTurnStateHeader))
}

// TestOpenAIRejectedExplicitCacheTTLIsNotRetried supplies a model rejection
// for a cache breakpoint with a requested lifetime. The existing field
// compatibility retry must leave the request for ordinary error delivery.
func TestOpenAIRejectedExplicitCacheTTLIsNotRetried(t *testing.T) {
	response := []byte(`{"error":{"code":"invalid_parameter","message":"input[0].prompt_cache_breakpoint is not supported on this model","param":"input[0].prompt_cache_breakpoint"}}`)
	body := []byte(`{"prompt_cache_options":{"ttl":"30m"},"input":[{"type":"message","prompt_cache_breakpoint":{"ttl":"12h"}}]}`)
	retry, _, changed, err := openai.NormalizeOpenAIResponsesRejectedFieldRetryBody(http.StatusBadRequest, body, response)
	require.NoError(t, err)
	require.False(t, changed)
	require.Nil(t, retry)
}
