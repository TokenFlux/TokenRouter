package httpapi

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest/observer"

	testassert "github.com/TokenFlux/TokenRouter/internal/testutil/assertion"
)

// loggedFields extracts fields from exactly one captured parsing diagnostic. It
// converts the body length to int and the stored error to its message;
// unexpected log counts fail the calling test.
func loggedFields(t *testing.T, logs *observer.ObservedLogs) map[string]any {
	t.Helper()
	entries := logs.All()
	require.Len(t, entries, 1)
	fields := map[string]any{}
	for _, f := range entries[0].Context {
		switch f.Key {
		case "body_len":
			fields[f.Key] = int(f.Integer)
		case "error":
			fields[f.Key] = testassert.MustType[error](f.Interface).Error()
		default:
			fields[f.Key] = f.String
		}
	}
	return fields
}

// TestLogRequestBodyParseFailure_DerivesErrorWhenNil reports malformed JSON
// with no supplied error. The captured diagnostic must retain body length and
// identify the invalid token offset.
func TestLogRequestBodyParseFailure_DerivesErrorWhenNil(t *testing.T) {
	log, logs := newObservedLogger(t)
	body := []byte(`{"model": bad}`)

	LogRequestBodyParseFailure(log, body, nil)

	fields := loggedFields(t, logs)
	require.Equal(t, len(body), fields["body_len"])
	require.Contains(t, fields["error"], "invalid json")
	require.Contains(t, fields["error"], "offset=11")
}

// TestLogRequestBodyParseFailure_ShortBodyHasNoTail reports a body shorter than
// the excerpt budget. It requires an escaped head field and excludes a
// redundant tail field.
func TestLogRequestBodyParseFailure_ShortBodyHasNoTail(t *testing.T) {
	log, logs := newObservedLogger(t)
	body := []byte(`{"broken":`)

	LogRequestBodyParseFailure(log, body, nil)

	fields := loggedFields(t, logs)
	require.Contains(t, fields, "body_head")
	require.NotContains(t, fields, "body_tail")
	require.Contains(t, testassert.MustType[string](fields["body_head"]), `{\"broken\":`)
}

// TestLogRequestBodyParseFailure_LargeBodyBoundedSnippets reports a large
// unterminated JSON string. The logged length remains complete while bounded
// excerpts retain distinct beginning and ending content.
func TestLogRequestBodyParseFailure_LargeBodyBoundedSnippets(t *testing.T) {
	log, logs := newObservedLogger(t)
	// The roughly 1 MiB body checks bounded snippets that retain the beginning and ending bytes.
	body := []byte(`{"model":"claude-sonnet-4-6","big":"` + strings.Repeat("A", 1<<20) + `"`)

	LogRequestBodyParseFailure(log, body, nil)

	fields := loggedFields(t, logs)
	require.Equal(t, len(body), fields["body_len"])
	head := testassert.MustType[string](fields["body_head"])
	tail := testassert.MustType[string](fields["body_tail"])
	require.Contains(t, head, "claude-sonnet-4-6")
	require.Contains(t, tail, "AAA")
	require.NotContains(t, tail, "claude-sonnet-4-6")
	// strconv.Quote adds quotes and escapes; four times the snippet size is a loose bound for this fixture.
	require.LessOrEqual(t, len(head), parseFailureSnippetLen*4)
	require.LessOrEqual(t, len(tail), parseFailureSnippetLen*4)
}

// TestLogRequestBodyParseFailure_EscapesControlCharacters includes newline and
// control bytes in malformed JSON. It requires escaped spellings in the excerpt
// and rejects literal controls in the logged field.
func TestLogRequestBodyParseFailure_EscapesControlCharacters(t *testing.T) {
	log, logs := newObservedLogger(t)
	body := []byte("{\"model\":\x01\n\"x\"}")

	LogRequestBodyParseFailure(log, body, nil)

	fields := loggedFields(t, logs)
	head := testassert.MustType[string](fields["body_head"])
	require.NotContains(t, head, "\n")
	require.NotContains(t, head, "\x01")
	require.Contains(t, head, `\n`)
	require.Contains(t, head, `\x01`)
}

// TestLogRequestBodyParseFailure_NilLoggerNoPanic calls the reporter without a
// logger or explicit error. The missing logging dependency must leave this
// diagnostic path safe to invoke.
func TestLogRequestBodyParseFailure_NilLoggerNoPanic(t *testing.T) {
	require.NotPanics(t, func() {
		LogRequestBodyParseFailure(nil, []byte(`{`), nil)
	})
}

// TestParseOpenAICompatibleStream covers omitted, true, false, string, number
// and null JSON stream fields. Only omission and booleans must be valid, with
// omission retaining buffered intent.
func TestParseOpenAICompatibleStream(t *testing.T) {
	tests := []struct {
		name       string // stream field case label
		body       string // encoded request JSON fixture
		wantStream bool   // expected client stream intent
		wantOK     bool   // expected field type validity
	}{
		{name: "missing", body: `{"model":"gpt-5"}`, wantStream: false, wantOK: true},
		{name: "true", body: `{"model":"gpt-5","stream":true}`, wantStream: true, wantOK: true},
		{name: "false", body: `{"model":"gpt-5","stream":false}`, wantStream: false, wantOK: true},
		{name: "string", body: `{"model":"gpt-5","stream":"true"}`, wantStream: false, wantOK: false},
		{name: "number", body: `{"model":"gpt-5","stream":1}`, wantStream: false, wantOK: false},
		{name: "null", body: `{"model":"gpt-5","stream":null}`, wantStream: false, wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotStream, gotOK := ParseOpenAICompatibleStream([]byte(tt.body))

			require.Equal(t, tt.wantStream, gotStream)
			require.Equal(t, tt.wantOK, gotOK)
		})
	}
}
