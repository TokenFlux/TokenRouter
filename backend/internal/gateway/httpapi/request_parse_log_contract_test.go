//go:build unit

package httpapi

import (
	"strings"
	"testing"

	testassert "github.com/TokenFlux/TokenRouter/internal/testutil/assertion"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func newObservedLogger(t *testing.T) (*zap.Logger, *observer.ObservedLogs) {
	t.Helper()
	core, logs := observer.New(zap.WarnLevel)
	return zap.New(core), logs
}

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

func TestLogRequestBodyParseFailure_DerivesErrorWhenNil(t *testing.T) {
	log, logs := newObservedLogger(t)
	body := []byte(`{"model": bad}`)

	LogRequestBodyParseFailure(log, body, nil)

	fields := loggedFields(t, logs)
	require.Equal(t, len(body), fields["body_len"])
	require.Contains(t, fields["error"], "invalid json")
	require.Contains(t, fields["error"], "offset=11")
}

func TestLogRequestBodyParseFailure_ShortBodyHasNoTail(t *testing.T) {
	log, logs := newObservedLogger(t)
	body := []byte(`{"broken":`)

	LogRequestBodyParseFailure(log, body, nil)

	fields := loggedFields(t, logs)
	require.Contains(t, fields, "body_head")
	require.NotContains(t, fields, "body_tail")
	require.Contains(t, testassert.MustType[string](fields["body_head"]), `{\"broken\":`)
}

func TestLogRequestBodyParseFailure_LargeBodyBoundedSnippets(t *testing.T) {
	log, logs := newObservedLogger(t)
	// 约 1MB 的请求体只记录受限的首尾片段，头部保留结构前缀，尾部保留末端字节。
	body := []byte(`{"model":"claude-sonnet-4-6","big":"` + strings.Repeat("A", 1<<20) + `"`)

	LogRequestBodyParseFailure(log, body, nil)

	fields := loggedFields(t, logs)
	require.Equal(t, len(body), fields["body_len"])
	head := testassert.MustType[string](fields["body_head"])
	tail := testassert.MustType[string](fields["body_tail"])
	require.Contains(t, head, "claude-sonnet-4-6")
	require.Contains(t, tail, "AAA")
	require.NotContains(t, tail, "claude-sonnet-4-6")
	// strconv.Quote 会增加外层引号和转义，四倍长度是宽松上限。
	require.LessOrEqual(t, len(head), parseFailureSnippetLen*4)
	require.LessOrEqual(t, len(tail), parseFailureSnippetLen*4)
}

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

func TestLogRequestBodyParseFailure_NilLoggerNoPanic(t *testing.T) {
	require.NotPanics(t, func() {
		LogRequestBodyParseFailure(nil, []byte(`{`), nil)
	})
}
