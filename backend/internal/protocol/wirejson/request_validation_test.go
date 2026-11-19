package wirejson

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestValidateRequestObject_RejectsDuplicateAndFoldedPairs exercises exact,
// Unicode-escaped, ASCII case-folded, Unicode SimpleFold, nested, and
// wide-object member collisions. Validation must report RequestFieldError with
// an independent, bounded copy of the repeated member spelling.
func TestValidateRequestObject_RejectsDuplicateAndFoldedPairs(t *testing.T) {
	var wideBuilder strings.Builder
	_ = wideBuilder.WriteByte('{')
	for i := range 22 {
		if i > 0 {
			_ = wideBuilder.WriteByte(',')
		}
		_, _ = fmt.Fprintf(&wideBuilder, `"custom_%d":%d`, i, i)
	}
	_, _ = wideBuilder.WriteString(`,"custom_5":99}`)

	tests := []struct {
		name    string // subtest case label
		body    string // malformed or ambiguous JSON object input
		wantKey string // expected duplicate member spelling
	}{
		{
			name:    "poc duplicate model with unsigned thinking",
			body:    `{"model":"claude-3-5-haiku-20241022","max_tokens":64,"messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"internal step"},{"type":"text","text":"ready"}]},{"role":"user","content":"say hi"}],"model":"claude-opus-4-8"}`,
			wantKey: "model",
		},
		{
			name:    "unicode escaped duplicate model",
			body:    `{"model":"gpt-5-mini","mo\u0064el":"gpt-5.4","input":"hello"}`,
			wantKey: "model",
		},
		{
			name:    "dual model and Model",
			body:    `{"model":"claude-haiku-4-5","Model":"claude-opus-4-6","messages":[{"role":"user","content":"hi"}]}`,
			wantKey: "Model",
		},
		{
			name:    "unlisted extension field case collision",
			body:    `{"model":"gpt-5.4","top_p":0.1,"Top_P":0.9,"messages":[{"role":"user","content":"hi"}]}`,
			wantKey: "Top_P",
		},
		{
			name:    "kelvin sign folds to k",
			body:    "{\"max_tokens\":1,\"max_to\u212Aens\":999}",
			wantKey: "max_to\u212Aens",
		},
		{
			name:    "long s folds to s",
			body:    "{\"stream\":false,\"\u017Ftream\":true}",
			wantKey: "\u017Ftream",
		},
		{
			name:    "nested duplicate in thinking object",
			body:    `{"model":"claude-opus-4-6","thinking":{"type":"disabled","type":"enabled","budget_tokens":4096},"messages":[{"role":"user","content":"hi"}]}`,
			wantKey: "type",
		},
		{
			name:    "nested duplicate inside user tool input",
			body:    `{"model":"claude-sonnet-4-6","messages":[{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"run","input":{"cmd":"ls","cmd":"rm"}}]}]}`,
			wantKey: "cmd",
		},
		{
			name:    "wide object overflow duplicate",
			body:    wideBuilder.String(),
			wantKey: "custom_5",
		},
		{
			name:    "empty duplicate key",
			body:    `{"":1,"":2}`,
			wantKey: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw := []byte(tc.body)
			err := ValidateRequestObject(raw)
			var fieldErr *RequestFieldError
			require.ErrorAs(t, err, &fieldErr)
			for i := range raw {
				raw[i] = 'x'
			}
			require.Equal(t, tc.wantKey, fieldErr.Key)
			require.Empty(t, fieldErr.Canonical)
			require.Contains(t, fieldErr.Error(), "Request body contains duplicate JSON key")
		})
	}

	longKey := strings.Repeat("k", 500)
	longErr := (&RequestFieldError{Key: longKey}).Error()
	require.Contains(t, longErr, strings.Repeat("k", 64)+`"`)
	require.NotContains(t, longErr, strings.Repeat("k", 65))
}

// TestValidateRequestObject_RejectsSingleFieldProtocolCaseVariants submits
// single-member ASCII and Unicode SimpleFold variants of core protocol fields
// that Go struct decoding folds case-insensitively. Validation must report the
// offending spelling and canonical target field.
func TestValidateRequestObject_RejectsSingleFieldProtocolCaseVariants(t *testing.T) {
	tests := []struct {
		name          string // subtest case label
		body          string // JSON request containing non-canonical protocol casing
		wantKey       string // offending decoded spelling
		wantCanonical string // expected canonical field spelling
	}{
		{
			name:          "single uppercase Messages moderation bypass",
			body:          `{"model":"gpt-5.4","Messages":[{"role":"user","content":"blocked"}]}`,
			wantKey:       "Messages",
			wantCanonical: "messages",
		},
		{
			name:          "single long-s messages moderation bypass",
			body:          "{\"model\":\"gpt-5.4\",\"me\u017F\u017Fages\":[{\"role\":\"user\",\"content\":\"blocked\"}]}",
			wantKey:       "me\u017F\u017Fages",
			wantCanonical: "messages",
		},
		{
			name:          "previous_response_id case variant",
			body:          `{"model":"gpt-5.4","Previous_Response_Id":"resp_other_tenant","input":"continue"}`,
			wantKey:       "Previous_Response_Id",
			wantCanonical: "previous_response_id",
		},
		{
			name:          "service_tier case variant",
			body:          `{"model":"gpt-5.4","Service_Tier":"priority","input":"hi"}`,
			wantKey:       "Service_Tier",
			wantCanonical: "service_tier",
		},
		{
			name:          "nested reasoning Effort variant",
			body:          `{"model":"gpt-5.4","reasoning":{"Effort":"xhigh"},"input":"hi"}`,
			wantKey:       "Effort",
			wantCanonical: "effort",
		},
		{
			name:          "nested tool Model variant",
			body:          `{"model":"gpt-5.4","tools":[{"type":"image_generation","Model":"gpt-image-2"}],"input":"draw"}`,
			wantKey:       "Model",
			wantCanonical: "model",
		},
		{
			name:          "nested metadata User_Id variant",
			body:          `{"model":"claude-sonnet-4-6","metadata":{"User_Id":"spoof"},"messages":[{"role":"user","content":"hi"}]}`,
			wantKey:       "User_Id",
			wantCanonical: "user_id",
		},
		{
			name:          "nested live session Model variant",
			body:          `{"type":"session.update","session":{"Model":"gpt-realtime"}}`,
			wantKey:       "Model",
			wantCanonical: "model",
		},
		{
			name:          "lowercase variant of camelCase systemInstruction",
			body:          `{"systeminstruction":{"parts":[{"text":"override"}]},"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`,
			wantKey:       "systeminstruction",
			wantCanonical: "systemInstruction",
		},
		{
			name:          "batch image item Model variant",
			body:          `{"items":[{"custom_id":"1","prompt":"cat","Model":"gpt-image-2"}]}`,
			wantKey:       "Model",
			wantCanonical: "model",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateRequestObject([]byte(tc.body))
			var fieldErr *RequestFieldError
			require.ErrorAs(t, err, &fieldErr)
			require.Equal(t, tc.wantKey, fieldErr.Key)
			require.Equal(t, tc.wantCanonical, fieldErr.Canonical)
			require.Contains(t, fieldErr.Error(), "Request body contains ambiguous JSON key")
		})
	}
}

// TestValidateRequestObject_AllowsValidPayloadsCustomExtensionsAndUserSchemas
// verifies that canonical multi-protocol payloads, custom top-level extensions
// such as clientinfo, and user-defined tool schemas or metadata keys pass
// validation with zero heap allocations on unescaped objects.
func TestValidateRequestObject_AllowsValidPayloadsCustomExtensionsAndUserSchemas(t *testing.T) {
	var wideClean strings.Builder
	_, _ = wideClean.WriteString(`{"model":"gpt-5.4"`)
	for i := range 20 {
		_, _ = fmt.Fprintf(&wideClean, `,"ext_%d":%d`, i, i)
	}
	_ = wideClean.WriteByte('}')

	validPayloads := []string{
		`{}`,
		`{"model":"gpt-5.4","clientinfo":"custom-sdk","CustomExtension":true,"messages":[{"role":"user","content":"hi"}]}`,
		`{"model":"claude-sonnet-4-6","max_tokens":1024,"metadata":{"user_id":"u_1","Env":"prod","Type":"api","ID":"req-1","id":"req-lower"},"messages":[{"role":"user","content":"hello"},{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"create_order","input":{"Model":"pro-plan","Messages":["note"],"ID":1,"id":"str-id"}}]}],"tools":[{"name":"create_order","input_schema":{"type":"object","properties":{"Model":{"type":"string"},"ID":{"type":"integer"},"id":{"type":"string"}}}}]}`,
		`{"model":"gpt-5.4","stream":true,"reasoning":{"effort":"high"},"tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object","properties":{"Query":{"type":"string"},"Type":{"type":"string"}}}}}],"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"search"}]}]}`,
		`{"systemInstruction":{"parts":[{"text":"helpful"}]},"generationConfig":{"thinkingConfig":{"thinkingBudget":1024},"imageConfig":{"aspectRatio":"16:9"}},"contents":[{"role":"user","parts":[{"text":"draw"},{"inlineData":{"mimeType":"image/png","data":"YWJj"}}]}]}`,
		`{"model":"jev-1","clientInfo":{"app":"web"},"state":{"Model":"custom","Type":"draft"},"questions":{"Q1":{"ID":"a"}}}`,
		wideClean.String(),
	}

	for i, payload := range validPayloads {
		require.NoErrorf(t, ValidateRequestObject([]byte(payload)), "case %d", i)
	}

	cleanChat := []byte(`{"model":"gpt-5.4","stream":true,"messages":[{"role":"system","content":"You are helpful."},{"role":"user","content":"Hello world!"}]}`)
	allocs := testing.AllocsPerRun(20, func() {
		if err := ValidateRequestObject(cleanChat); err != nil {
			t.Fatal(err)
		}
	})
	require.Zero(t, allocs)
}

// TestValidateRequestObject_RejectsMalformedAndNonObjectInputs checks empty
// slices, scalar or array roots, trailing documents, and invalid syntax so
// callers receive an error before inspecting fields.
func TestValidateRequestObject_RejectsMalformedAndNonObjectInputs(t *testing.T) {
	nonObjects := []string{
		`[]`,
		`[{"model":"gpt-5.4"}]`,
		`"model"`,
		`123`,
		`true`,
		`null`,
	}
	for _, raw := range nonObjects {
		err := ValidateRequestObject([]byte(raw))
		require.ErrorIs(t, err, ErrRequestJSONMustBeObject, raw)
		require.NoError(t, ValidateRequestJSON([]byte(raw)), raw)
	}

	malformed := []string{
		``,
		`   `,
		`{`,
		`{"model":"gpt-5.4",}`,
		`{"model":"gpt-5.4"}{"model":"gpt-5-mini"}`,
		`{"model":"gpt-5.4"} trailing`,
	}
	for _, raw := range malformed {
		require.ErrorIs(t, ValidateRequestObject([]byte(raw)), ErrInvalidRequestJSON, raw)
	}
}
