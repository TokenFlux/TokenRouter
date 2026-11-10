package openai

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestReplaceModelInBody_PreservesTopLevelFieldOrder changes a model between
// other top-level fields. The resulting bytes must contain the requested model
// and retain the asserted token positions for alpha, model, messages, and omega.
func TestReplaceModelInBody_PreservesTopLevelFieldOrder(t *testing.T) {
	body := []byte(`{"alpha":1,"model":"claude-3-5-sonnet-latest","messages":[],"omega":2}`)

	result := ReplaceModelInBody(body, "claude-3-5-sonnet-20241022")
	resultStr := string(result)

	assertJSONTokenOrder(t, resultStr, `"alpha"`, `"model"`, `"messages"`, `"omega"`)
	require.Contains(t, resultStr, `"model":"claude-3-5-sonnet-20241022"`)
}

// TestReplaceModelInBody covers empty, matching, differing, and absent model
// fields. The patch must retain untouched fixture bytes or insert the requested
// model while retaining the asserted temperature field marker.
func TestReplaceModelInBody(t *testing.T) {
	tests := []struct {
		name     string                            // model-patching input scenario
		body     []byte                            // supplied request bytes
		newModel string                            // requested replacement model
		check    func(t *testing.T, result []byte) // case result assertions
	}{
		{
			name:     "empty body",
			body:     []byte{},
			newModel: "new-model",
			check: func(t *testing.T, result []byte) {
				require.Equal(t, []byte{}, result)
			},
		},
		{
			name:     "model already equal",
			body:     []byte(`{"model":"claude-sonnet-4","temperature":0.7}`),
			newModel: "claude-sonnet-4",
			check: func(t *testing.T, result []byte) {
				require.Equal(t, []byte(`{"model":"claude-sonnet-4","temperature":0.7}`), result)
			},
		},
		{
			name:     "model different",
			body:     []byte(`{"model":"claude-sonnet-4","temperature":0.7}`),
			newModel: "claude-opus-4",
			check: func(t *testing.T, result []byte) {
				require.Contains(t, string(result), `"model":"claude-opus-4"`)
				require.Contains(t, string(result), `"temperature"`)
			},
		},
		{
			name:     "no model field",
			body:     []byte(`{"temperature":0.7}`),
			newModel: "claude-opus-4",
			check: func(t *testing.T, result []byte) {
				require.Contains(t, string(result), `"model":"claude-opus-4"`)
				require.Contains(t, string(result), `"temperature"`)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ReplaceModelInBody(tt.body, tt.newModel)
			tt.check(t, result)
		})
	}
}

// TestReplaceModelInBody_InvalidJSON attempts a model patch on a malformed
// object and an array. The malformed case must return nonnil bytes, while an
// unsupported array write must return the supplied array unchanged.
func TestReplaceModelInBody_InvalidJSON(t *testing.T) {
	// This malformed object exercises an attempted field insertion. The
	// assertion requires nonnil output and leaves complete JSON validity for
	// the caller's separate request validation.
	brokenBody := []byte("{broken")
	result := ReplaceModelInBody(brokenBody, "new-model")
	require.NotNil(t, result)

	// An array cannot receive the object model field through this patch.
	// The helper returns its supplied bytes when sjson rejects that write.
	arrayBody := []byte("[]")
	result2 := ReplaceModelInBody(arrayBody, "new-model")
	require.Equal(t, arrayBody, result2)
}

// TestNormalizeChatDeveloperMessages_PreservesSourceFieldOrder places content
// before an escaped role and a scalar instruction in the next message. It
// checks the exact output bytes and verifies that the source remains intact.
func TestNormalizeChatDeveloperMessages_PreservesSourceFieldOrder(t *testing.T) {
	const body = ` {
		"messages": [
			{"content": [{"type":"text","text":"Read \"A\"."},{"type":"text","text":"Then act."}], "r\u006fle": "devel\u006fper"},
			{"role": "developer", "content": "\u0042", "name": "policy"},
			{"role": "user", "content": "go"}
		],
		"custom": 9007199254740993
	} `
	const want = ` {
		"messages": [
			{"content": "Read \"A\".\n\nThen act.", "r\u006fle": "system"},
			{"role": "system", "content": "\u0042", "name": "policy"},
			{"role": "user", "content": "go"}
		],
		"custom": 9007199254740993
	} `

	input := []byte(body)
	result, err := NormalizeChatDeveloperMessages(input)

	require.NoError(t, err)
	require.Equal(t, want, string(result))
	require.Equal(t, body, string(input))
}

// assertJSONTokenOrder requires each token's first byte-string occurrence to
// follow the preceding token. Missing or out-of-order tokens fail the test;
// the helper checks positions directly without decoding JSON field structure.
func assertJSONTokenOrder(t *testing.T, body string, tokens ...string) {
	t.Helper()

	last := -1
	for _, token := range tokens {
		pos := strings.Index(body, token)
		require.NotEqualf(t, -1, pos, "missing token %s in body %s", token, body)
		require.Greaterf(t, pos, last, "token %s should appear after previous tokens in body %s", token, body)
		last = pos
	}
}
