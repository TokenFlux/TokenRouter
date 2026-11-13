package bridge

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A consumer can abandon native compatibility iteration before all tool parts
// have been visited. The first payload's usage is captured before that
// output, so this fixture can read its counts while checking that only the
// reached tool generated an identity.

// TestNativeGeminiCompatIteratorPreservesPartialUsageAndLazyIDs stops on the
// first tool start in a two-call payload. It checks one generated ID and the
// already recorded input, output, and cached counts.
func TestNativeGeminiCompatIteratorPreservesPartialUsageAndLazyIDs(t *testing.T) {
	generated := 0
	// The serial fixture returns two hexadecimal characters per requested byte,
	// making the generator's byte-unit callback visible while counting only tool starts reached.
	runtime := NativeGeminiRuntime{RandomHex: func(size int) string { generated++; return strings.Repeat("a", size*2) }}
	state := NewNativeGeminiCompatStream(runtime)
	response := map[string]any{"candidates": []any{map[string]any{"content": map[string]any{"parts": []any{
		map[string]any{"functionCall": map[string]any{"name": "first", "args": map[string]any{}}},
		map[string]any{"functionCall": map[string]any{"name": "second", "args": map[string]any{}}},
	}}}}}
	raw := []byte(`{"usageMetadata":{"promptTokenCount":12,"cachedContentTokenCount":2,"candidatesTokenCount":3}}`)
	for event := range state.Process(response, raw) {
		if event.Type == "content_block_start" {
			break
		}
	}
	require.Equal(t, 1, generated)
	require.Equal(t, 10, state.Usage().InputTokens)
	require.Equal(t, 3, state.Usage().OutputTokens)
	require.Equal(t, 2, state.Usage().CacheReadInputTokens)
}

// TestNativeGeminiStreamsKeepToolCallsSeparate feeds both native stream
// converters same-name calls and continuations of a single call. Every tool
// block must receive the arguments of exactly one invocation.
func TestNativeGeminiStreamsKeepToolCallsSeparate(t *testing.T) {
	for _, tc := range []struct {
		name   string     // invocation pattern
		args   []string   // expected complete inputs
		frames [][]string // function calls in each SSE payload
	}{
		{
			name: "distinct upstream IDs",
			args: []string{`{"q":"a"}`, `{"q":"b"}`},
			frames: [][]string{{
				`{"id":"a","name":"lookup","args":{"q":"a"}}`,
				`{"id":"b","name":"lookup","args":{"q":"b"}}`,
			}},
		},
		{
			name: "identical calls in one frame",
			args: []string{`{"q":"a"}`, `{"q":"a"}`, `{}`},
			frames: [][]string{{
				`{"name":"lookup","args":{"q":"a"}}`,
				`{"name":"lookup","args":{"q":"a"}}`,
				`{"name":"lookup"}`,
			}},
		},
		{
			name: "repeated complete call across frames",
			args: []string{`{"q":"a"}`, `{"q":"b"}`},
			frames: [][]string{
				{`{"name":"lookup","args":{"q":"a"}}`},
				{`{"name":"lookup","args":{"q":"a"}}`},
				{`{"name":"lookup","args":{"q":"b"}}`},
			},
		},
		{
			name: "cumulative batch repeats parallel calls",
			args: []string{`{"q":"a"}`, `{"q":"b"}`, `{"q":"b"}`},
			frames: [][]string{
				{
					`{"name":"lookup","args":{"q":"a"}}`,
					`{"name":"lookup","args":{"q":"b"}}`,
				},
				{
					`{"name":"lookup","args":{"q":"a"}}`,
					`{"name":"lookup","args":{"q":"b"}}`,
					`{"name":"lookup","args":{"q":"b"}}`,
				},
				{
					`{"name":"lookup","args":{"q":"a"}}`,
					`{"name":"lookup","args":{"q":"b"}}`,
					`{"name":"lookup","args":{"q":"b"}}`,
				},
			},
		},
		{
			name: "valid scalar is part of an argument object",
			args: []string{`{"q":"a"}`},
			frames: [][]string{
				{`{"name":"lookup","args":"{\"q\":"}`},
				{`{"name":"lookup","args":"\"a\""}`},
				{`{"name":"lookup","args":"}"}`},
			},
		},
		{
			name: "significant whitespace in string fragment",
			args: []string{`{"q":"a b"}`},
			frames: [][]string{
				{`{"name":"lookup","args":"{\"q\":\"a"}`},
				{`{"name":"lookup","args":" "}`},
				{`{"name":"lookup","args":"b\"}"}`},
			},
		},
		{
			name: "nested argument object is a string fragment",
			args: []string{`{"q":{"a":1}}`},
			frames: [][]string{
				{`{"name":"lookup","args":"{\"q\":"}`},
				{`{"name":"lookup","args":"{\"a\":1}"}`},
				{`{"name":"lookup","args":"}"}`},
			},
		},
		{
			name: "opening brace is both a prefix and a nested fragment",
			args: []string{`{"q":{"a":1}}`},
			frames: [][]string{
				{`{"name":"lookup","args":"{\"q\":"}`},
				{`{"name":"lookup","args":"{"}`},
				{`{"name":"lookup","args":"\"a\":1}}"}`},
			},
		},
		{
			name: "continued call with ID",
			args: []string{`{"q":"a"}`},
			frames: [][]string{
				{`{"id":"a","name":"lookup"}`},
				{`{"id":"a","name":"lookup","args":{"q":"a"}}`},
				{`{"id":"a","name":"lookup","args":{"q":"a"}}`},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			generated := 0
			runtime := NativeGeminiRuntime{RandomHex: func(int) string {
				generated++
				return strconv.Itoa(generated)
			}}
			messages := NewNativeGeminiMessagesStream(runtime)
			compat := NewNativeGeminiCompatStream(runtime)
			var messagesArgs, compatArgs []string
			for _, frame := range tc.frames {
				parts := make([]string, 0, len(frame))
				for _, call := range frame {
					parts = append(parts, `{"functionCall":`+call+`}`)
				}
				raw := []byte(`{"candidates":[{"content":{"parts":[` + strings.Join(parts, ",") + `]}}]}`)
				var payload map[string]any
				require.NoError(t, json.Unmarshal(raw, &payload))
				for event := range messages.Process(payload, raw) {
					if event.Name == "" {
						continue
					}
					wire, err := json.Marshal(event.Data)
					require.NoError(t, err)
					var decoded AnthropicStreamEvent
					require.NoError(t, json.Unmarshal(wire, &decoded))
					if decoded.ContentBlock != nil {
						require.JSONEq(t, "{}", string(decoded.ContentBlock.Input))
						messagesArgs = append(messagesArgs, "")
					}
					if decoded.Delta != nil {
						messagesArgs[len(messagesArgs)-1] += decoded.Delta.PartialJSON
					}
				}
				for event := range compat.Process(payload, raw) {
					if event.ContentBlock != nil {
						compatArgs = append(compatArgs, "")
					}
					if event.Delta != nil {
						compatArgs[len(compatArgs)-1] += event.Delta.PartialJSON
					}
				}
			}
			for event := range compat.Finish() {
				if event.Delta != nil && event.Delta.Type == "input_json_delta" {
					compatArgs[len(compatArgs)-1] += event.Delta.PartialJSON
				}
			}
			require.Len(t, messagesArgs, len(tc.args))
			require.Len(t, compatArgs, len(tc.args))
			for i, want := range tc.args {
				got := messagesArgs[i]
				if got == "" {
					got = "{}"
				}
				require.JSONEq(t, want, got)
				require.JSONEq(t, want, compatArgs[i])
			}
		})
	}
}
