package openai

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// benchmarkUsageSink retains each successful benchmark extraction so its
// selected count values remain available after the timed loop finishes.
var benchmarkUsageSink ForwardUsage

// TestProgressiveImageCacheUsage retains explicit modality counts across sparse
// SSE and WebSocket observations. Missing terminal usage preserves those counts;
// a complete terminal snapshot can authoritatively replace them with zero.
func TestProgressiveImageCacheUsage(t *testing.T) {
	for _, test := range []struct {
		name  string                      // usage transport scenario
		parse func([]byte, *ForwardUsage) // cumulative usage parser
	}{
		{name: "SSE", parse: ParseSSEUsageBytes},
		{name: "SSE with event label", parse: func(body []byte, usage *ForwardUsage) { ParseSSEUsageBytesWithType(body, "", usage) }},
		{name: "WebSocket", parse: ParseWSResponseUsageFromCompletedEvent},
	} {
		t.Run(test.name, func(t *testing.T) {
			var usage ForwardUsage
			test.parse([]byte(`{"type":"response.in_progress","response":{"usage":{"input_tokens":150,"input_tokens_details":{"image_tokens":80,"cached_tokens":50,"cached_tokens_details":{"image_tokens":30}}}}}`), &usage)
			require.Equal(t, 30, usage.ImageCacheReadTokens)
			test.parse([]byte(`{"type":"response.in_progress","response":{"usage":{"output_tokens":11}}}`), &usage)
			test.parse([]byte(`{"type":"response.completed","response":{"output":[]}}`), &usage)
			require.Equal(t, 150, usage.InputTokens)
			require.Equal(t, 80, usage.ImageInputTokens)
			require.Equal(t, 50, usage.CacheReadInputTokens)
			require.Equal(t, 30, usage.ImageCacheReadTokens)
			require.Equal(t, 11, usage.OutputTokens)
			test.parse([]byte(`{"type":"response.completed","response":{"usage":{"input_tokens":150,"input_tokens_details":{"image_tokens":80,"cached_tokens":50,"cached_tokens_details":{"image_tokens":0}}}}}`), &usage)
			require.Zero(t, usage.ImageCacheReadTokens)
			var aggregateOnly ForwardUsage
			test.parse([]byte(`{"type":"response.in_progress","response":{"usage":{"input_tokens":150,"input_tokens_details":{"image_tokens":80,"cached_tokens":50}}}}`), &aggregateOnly)
			require.Zero(t, aggregateOnly.ImageCacheReadTokens)
		})
	}
}

// BenchmarkOpenAIUsageExtractLegacy measures typed decoding of a fixed
// canonical usage response. Fixture construction precedes timing, and each
// successful extraction is retained in the shared usage sink.
func BenchmarkOpenAIUsageExtractLegacy(b *testing.B) {
	body := benchmarkOpenAIUsageJSONBytes()

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		usage, ok := legacyExtractOpenAIUsageFromJSONBytes(body)
		if ok {
			benchmarkUsageSink = usage
		}
	}
}

// BenchmarkOpenAIUsageExtractOptimized measures the production raw usage
// extractor on the fixed canonical response. Fixture construction precedes
// timing, and each successful extraction is retained in the shared usage sink.
func BenchmarkOpenAIUsageExtractOptimized(b *testing.B) {
	body := benchmarkOpenAIUsageJSONBytes()

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		usage, ok := ExtractOpenAIUsageFromJSONBytes(body)
		if ok {
			benchmarkUsageSink = usage
		}
	}
}

// TestExtractOpenAIUsageFromJSONBytes_MergesHostedImageGenToolUsage places
// main and hosted-image usage under a terminal response object. It checks
// retained input/output totals and the selected image-input/output detail
// counts.
func TestExtractOpenAIUsageFromJSONBytes_MergesHostedImageGenToolUsage(t *testing.T) {
	body := []byte(`{
		"type": "response.completed",
		"response": {
			"usage": {
				"input_tokens": 43792,
				"output_tokens": 1005,
				"total_tokens": 44797
			},
			"tool_usage": {
				"image_gen": {
					"input_tokens": 7918,
					"input_tokens_details": {"image_tokens": 7620, "text_tokens": 298},
					"output_tokens": 186,
					"output_tokens_details": {"image_tokens": 186, "text_tokens": 0},
					"total_tokens": 8104
				}
			}
		}
	}`)

	usage, ok := ExtractOpenAIUsageFromJSONBytes(body)
	require.True(t, ok)
	require.Equal(t, 43792, usage.InputTokens)
	require.Equal(t, 1005, usage.OutputTokens)
	require.Equal(t, 186, usage.ImageOutputTokens)
	require.Equal(t, 7620, usage.ImageInputTokens)
}

// TestExtractOpenAIUsageFromJSONBytes_NonStreamingMergesImageGen places main
// and hosted-image usage at the top level. It checks retained input/output
// totals and the selected image-input/output detail counts.
func TestExtractOpenAIUsageFromJSONBytes_NonStreamingMergesImageGen(t *testing.T) {
	body := []byte(`{
		"id": "resp_abc123",
		"object": "response",
		"usage": {
			"input_tokens": 5000,
			"output_tokens": 200
		},
		"tool_usage": {
			"image_gen": {
				"input_tokens": 3000,
				"input_tokens_details": {"image_tokens": 2800, "text_tokens": 200},
				"output_tokens": 150,
				"output_tokens_details": {"image_tokens": 150, "text_tokens": 0},
				"total_tokens": 3150
			}
		}
	}`)

	usage, ok := ExtractOpenAIUsageFromJSONBytes(body)
	require.True(t, ok)
	require.Equal(t, 5000, usage.InputTokens)
	require.Equal(t, 200, usage.OutputTokens)
	require.Equal(t, 150, usage.ImageOutputTokens)
	require.Equal(t, 2800, usage.ImageInputTokens)
}

// TestExtractOpenAIUsageFromJSONBytes_HostedImageGenFallbackRules supplies
// absent tool usage, existing image counters, and fractional/negative hosted
// details. The asserted image buckets remain zero or keep their selected base
// counts.
func TestExtractOpenAIUsageFromJSONBytes_HostedImageGenFallbackRules(t *testing.T) {
	tests := []struct {
		name string // response wire fixture label
		body string // raw response JSON or SSE
		// wantImageInput and wantImageOutput record independent selected buckets
		// from body. A valid hosted detail can fill one zero category even when
		// the other remains absent or already has a base observation.
		wantImageInput  int // expected image-input token count
		wantImageOutput int // expected image-output token count
	}{
		{
			name:            "without tool usage",
			body:            `{"usage":{"input_tokens":100,"output_tokens":50}}`,
			wantImageInput:  0,
			wantImageOutput: 0,
		},
		{
			name:            "base usage takes precedence",
			body:            `{"usage":{"input_tokens":100,"output_tokens":50,"input_tokens_details":{"image_tokens":20},"output_tokens_details":{"image_tokens":30}},"tool_usage":{"image_gen":{"input_tokens_details":{"image_tokens":90},"output_tokens_details":{"image_tokens":100}}}}`,
			wantImageInput:  20,
			wantImageOutput: 30,
		},
		{
			name:            "malformed tool details ignored",
			body:            `{"usage":{"input_tokens":100,"output_tokens":50},"tool_usage":{"image_gen":{"input_tokens_details":{"image_tokens":1.5},"output_tokens_details":{"image_tokens":-1}}}}`,
			wantImageInput:  0,
			wantImageOutput: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usage, ok := ExtractOpenAIUsageFromJSONBytes([]byte(tt.body))
			require.True(t, ok)
			require.Equal(t, tt.wantImageInput, usage.ImageInputTokens)
			require.Equal(t, tt.wantImageOutput, usage.ImageOutputTokens)
		})
	}
}

// TestMergeHostedImageGenToolUsage_EmptyImageGen supplies missing, null, and
// scalar hosted-image values beside existing aggregate counts. Every fixture
// must keep the complete ForwardUsage value equal to its snapshot.
func TestMergeHostedImageGenToolUsage_EmptyImageGen(t *testing.T) {
	tests := []struct {
		name string // response wire fixture label
		json string // raw hosted-image JSON fixture
	}{
		{name: "missing", json: `{}`},
		{name: "null", json: `{"image_gen":null}`},
		{name: "not object", json: `{"image_gen":42}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usage := ForwardUsage{InputTokens: 100, OutputTokens: 50}
			original := usage
			MergeHostedImageGenToolUsage(gjson.Get(tt.json, "image_gen"), &usage)
			require.Equal(t, original, usage)
		})
	}
}

// TestParseSSEUsageBytes_ResponseCompletedWithImageGen supplies a terminal
// response with main and hosted-image observations. The aggregate must expose
// the four asserted input/output and image-input/output counts.
func TestParseSSEUsageBytes_ResponseCompletedWithImageGen(t *testing.T) {
	data := []byte(`{
		"type": "response.completed",
		"response": {
			"usage": {"input_tokens": 10000, "output_tokens": 500},
			"tool_usage": {
				"image_gen": {
					"input_tokens_details": {"image_tokens": 3800, "text_tokens": 200},
					"output_tokens_details": {"image_tokens": 186, "text_tokens": 0}
				}
			}
		}
	}`)

	usage := &ForwardUsage{}
	ParseSSEUsageBytes(data, usage)

	require.Equal(t, 10000, usage.InputTokens)
	require.Equal(t, 500, usage.OutputTokens)
	require.Equal(t, 186, usage.ImageOutputTokens)
	require.Equal(t, 3800, usage.ImageInputTokens)
}

// TestReplaceModelInResponseBody supplies matching, unmatched, missing,
// invalid, empty, and nested-data response fixtures. Exact equality checks the
// expected top-level patch or unchanged bytes for each supplied case.
func TestReplaceModelInResponseBody(t *testing.T) {
	tests := []struct {
		name     string // response wire fixture label
		body     string // raw response JSON or SSE
		from     string // source model match value
		to       string // replacement model label value
		expected string // expected complete output text
	}{
		{
			name:     "替换顶层 model",
			body:     `{"id":"chatcmpl-123","model":"gpt-4o","choices":[]}`,
			from:     "gpt-4o",
			to:       "alias",
			expected: `{"id":"chatcmpl-123","model":"alias","choices":[]}`,
		},
		{
			name:     "model 不匹配不替换",
			body:     `{"id":"chatcmpl-123","model":"gpt-3.5-turbo","choices":[]}`,
			from:     "gpt-4o",
			to:       "alias",
			expected: `{"id":"chatcmpl-123","model":"gpt-3.5-turbo","choices":[]}`,
		},
		{
			name:     "无 model 字段不替换",
			body:     `{"id":"chatcmpl-123","choices":[]}`,
			from:     "gpt-4o",
			to:       "alias",
			expected: `{"id":"chatcmpl-123","choices":[]}`,
		},
		{
			name:     "非法 JSON 返回原值",
			body:     `not json`,
			from:     "gpt-4o",
			to:       "alias",
			expected: `not json`,
		},
		{
			name:     "空 body 返回原值",
			body:     ``,
			from:     "gpt-4o",
			to:       "alias",
			expected: ``,
		},
		{
			name:     "保持嵌套结构不变",
			body:     `{"model":"gpt-4o","usage":{"prompt_tokens":10,"completion_tokens":20},"choices":[{"message":{"role":"assistant","content":"hello"}}]}`,
			from:     "gpt-4o",
			to:       "alias",
			expected: `{"model":"alias","usage":{"prompt_tokens":10,"completion_tokens":20},"choices":[{"message":{"role":"assistant","content":"hello"}}]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ReplaceModelInResponseBody([]byte(tt.body), tt.from, tt.to)
			require.Equal(t, tt.expected, string(got))
		})
	}
}

// TestReplaceModelInSSELine supplies top-level/nested models, varied prefixes,
// sentinels, invalid/unmatched data, and simultaneous model fields. Exact
// expected lines check canonical changed prefixes, field retention, and
// top-level match priority.
func TestReplaceModelInSSELine(t *testing.T) {
	tests := []struct {
		name     string // response wire fixture label
		line     string // raw SSE data line
		from     string // source model match value
		to       string // replacement model label value
		expected string // expected complete output text
	}{
		{
			name:     "顶层 model 字段替换",
			line:     `data: {"id":"chatcmpl-123","model":"gpt-4o","choices":[]}`,
			from:     "gpt-4o",
			to:       "my-custom-model",
			expected: `data: {"id":"chatcmpl-123","model":"my-custom-model","choices":[]}`,
		},
		{
			name:     "嵌套 response.model 替换",
			line:     `data: {"type":"response","response":{"id":"resp-1","model":"gpt-4o","output":[]}}`,
			from:     "gpt-4o",
			to:       "my-model",
			expected: `data: {"type":"response","response":{"id":"resp-1","model":"my-model","output":[]}}`,
		},
		{
			name:     "model 不匹配时不替换",
			line:     `data: {"id":"chatcmpl-123","model":"gpt-3.5-turbo","choices":[]}`,
			from:     "gpt-4o",
			to:       "my-model",
			expected: `data: {"id":"chatcmpl-123","model":"gpt-3.5-turbo","choices":[]}`,
		},
		{
			name:     "无 model 字段时不替换",
			line:     `data: {"id":"chatcmpl-123","choices":[]}`,
			from:     "gpt-4o",
			to:       "my-model",
			expected: `data: {"id":"chatcmpl-123","choices":[]}`,
		},
		{
			name:     "空 data 行",
			line:     `data: `,
			from:     "gpt-4o",
			to:       "my-model",
			expected: `data: `,
		},
		{
			name:     "[DONE] 行",
			line:     `data: [DONE]`,
			from:     "gpt-4o",
			to:       "my-model",
			expected: `data: [DONE]`,
		},
		{
			name:     "非 data: 前缀行",
			line:     `event: message`,
			from:     "gpt-4o",
			to:       "my-model",
			expected: `event: message`,
		},
		{
			name:     "非法 JSON 不替换",
			line:     `data: {invalid json}`,
			from:     "gpt-4o",
			to:       "my-model",
			expected: `data: {invalid json}`,
		},
		{
			name:     "无空格 data: 格式",
			line:     `data:{"id":"x","model":"gpt-4o"}`,
			from:     "gpt-4o",
			to:       "my-model",
			expected: `data: {"id":"x","model":"my-model"}`,
		},
		{
			name:     "model 名含特殊字符",
			line:     `data: {"model":"org/model-v2.1-beta"}`,
			from:     "org/model-v2.1-beta",
			to:       "custom/alias",
			expected: `data: {"model":"custom/alias"}`,
		},
		{
			name:     "空行",
			line:     "",
			from:     "gpt-4o",
			to:       "my-model",
			expected: "",
		},
		{
			name:     "保持其他字段不变",
			line:     `data: {"id":"abc","object":"chat.completion.chunk","model":"gpt-4o","created":1234567890,"choices":[{"index":0,"delta":{"content":"hi"}}]}`,
			from:     "gpt-4o",
			to:       "alias",
			expected: `data: {"id":"abc","object":"chat.completion.chunk","model":"alias","created":1234567890,"choices":[{"index":0,"delta":{"content":"hi"}}]}`,
		},
		{
			name:     "顶层优先于嵌套：同时存在两个 model",
			line:     `data: {"model":"gpt-4o","response":{"model":"gpt-4o"}}`,
			from:     "gpt-4o",
			to:       "replaced",
			expected: `data: {"model":"replaced","response":{"model":"gpt-4o"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ReplaceModelInSSELine(tt.line, tt.from, tt.to)
			require.Equal(t, tt.expected, got)
		})
	}
}

// TestReplaceModelInSSEBody supplies multi-event, unmatched, mixed event/data,
// and empty buffered bodies. Exact equality checks model patches while
// retaining the expected line separators and unaffected event text.
func TestReplaceModelInSSEBody(t *testing.T) {
	tests := []struct {
		name     string // response wire fixture label
		body     string // raw response JSON or SSE
		from     string // source model match value
		to       string // replacement model label value
		expected string // expected complete output text
	}{
		{
			name:     "多行 SSE body 替换",
			body:     "data: {\"model\":\"gpt-4o\",\"choices\":[]}\n\ndata: {\"model\":\"gpt-4o\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n",
			from:     "gpt-4o",
			to:       "alias",
			expected: "data: {\"model\":\"alias\",\"choices\":[]}\n\ndata: {\"model\":\"alias\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\ndata: [DONE]\n",
		},
		{
			name:     "无需替换的 body",
			body:     "data: {\"model\":\"gpt-3.5-turbo\"}\n\ndata: [DONE]\n",
			from:     "gpt-4o",
			to:       "alias",
			expected: "data: {\"model\":\"gpt-3.5-turbo\"}\n\ndata: [DONE]\n",
		},
		{
			name:     "混合 event 和 data 行",
			body:     "event: message\ndata: {\"model\":\"gpt-4o\"}\n\n",
			from:     "gpt-4o",
			to:       "alias",
			expected: "event: message\ndata: {\"model\":\"alias\"}\n\n",
		},
		{
			name:     "空 body",
			body:     "",
			from:     "gpt-4o",
			to:       "alias",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ReplaceModelInSSEBody(tt.body, tt.from, tt.to)
			require.Equal(t, tt.expected, got)
		})
	}
}

// TestParseSSEUsage_SelectiveParsing feeds progressive usage and several
// terminal kinds into one aggregate. It checks positive progressive
// replacement, retained cache counts, terminal counts, and compatible Chat
// aliases at each asserted step.
func TestParseSSEUsage_SelectiveParsing(t *testing.T) {
	usage := &ForwardUsage{InputTokens: 9, OutputTokens: 8, CacheReadInputTokens: 7}

	ParseSSEUsageBytes([]byte(`{"type":"response.in_progress","response":{"usage":{"input_tokens":1,"output_tokens":2}}}`), usage)
	require.Equal(t, 1, usage.InputTokens)
	require.Equal(t, 2, usage.OutputTokens)
	require.Equal(t, 7, usage.CacheReadInputTokens)

	ParseSSEUsageBytes([]byte(`{"type":"response.completed","response":{"usage":{"input_tokens":3,"output_tokens":5,"input_tokens_details":{"cached_tokens":2}}}}`), usage)
	require.Equal(t, 3, usage.InputTokens)
	require.Equal(t, 5, usage.OutputTokens)
	require.Equal(t, 2, usage.CacheReadInputTokens)

	ParseSSEUsageBytes([]byte(`{"type":"response.done","response":{"usage":{"input_tokens":13,"output_tokens":15,"input_tokens_details":{"cached_tokens":4}}}}`), usage)
	require.Equal(t, 13, usage.InputTokens)
	require.Equal(t, 15, usage.OutputTokens)
	require.Equal(t, 4, usage.CacheReadInputTokens)

	// A failed terminal can still contain consumed-token observations. The
	// aggregate update remains independent of whether the response was delivered successfully.
	ParseSSEUsageBytes([]byte(`{"type":"response.failed","response":{"usage":{"input_tokens":17,"output_tokens":19,"input_tokens_details":{"cached_tokens":6}}}}`), usage)
	require.Equal(t, 17, usage.InputTokens)
	require.Equal(t, 19, usage.OutputTokens)
	require.Equal(t, 6, usage.CacheReadInputTokens)

	ParseSSEUsageBytes([]byte(`{"type":"response.completed","response":{"usage":{"prompt_tokens":21,"completion_tokens":8,"prompt_tokens_details":{"cached_tokens":6}}}}`), usage)
	require.Equal(t, 21, usage.InputTokens)
	require.Equal(t, 8, usage.OutputTokens)
	require.Equal(t, 6, usage.CacheReadInputTokens)
}

// TestParseSSEUsage_NonTerminalUsageMergesNonZeroFields combines nonterminal
// observations with explicit zero fields and later positive counters. The
// final four asserted buckets retain earlier positives or take the later
// positive values.
func TestParseSSEUsage_NonTerminalUsageMergesNonZeroFields(t *testing.T) {
	usage := &ForwardUsage{}

	ParseSSEUsageBytes([]byte(`{"type":"response.in_progress","usage":{"input_tokens":17,"output_tokens":1,"input_tokens_details":{"cached_tokens":4}}}`), usage)
	ParseSSEUsageBytes([]byte(`{"type":"response.output_text.done","usage":{"input_tokens":0,"output_tokens":5,"input_tokens_details":{"cached_tokens":0,"cache_write_tokens":3}}}`), usage)

	require.Equal(t, 17, usage.InputTokens)
	require.Equal(t, 5, usage.OutputTokens)
	require.Equal(t, 4, usage.CacheReadInputTokens)
	require.Equal(t, 3, usage.CacheCreationInputTokens)
}

// TestParseSSEUsage_TerminalUsageReplacesFallback follows progressive cache
// usage with a populated terminal object lacking cache detail. Terminal
// input/output counts replace the aggregate and its earlier cache count
// becomes zero.
func TestParseSSEUsage_TerminalUsageReplacesFallback(t *testing.T) {
	usage := &ForwardUsage{}

	ParseSSEUsageBytes([]byte(`{"type":"response.output_text.done","usage":{"input_tokens":17,"output_tokens":5,"input_tokens_details":{"cached_tokens":4}}}`), usage)
	ParseSSEUsageBytes([]byte(`{"type":"response.completed","response":{"usage":{"input_tokens":19,"output_tokens":7}}}`), usage)

	require.Equal(t, 19, usage.InputTokens)
	require.Equal(t, 7, usage.OutputTokens)
	require.Zero(t, usage.CacheReadInputTokens)
}

// TestParseSSEUsage_TerminalWithoutUsageKeepsFallback follows progressive
// counts with a usage-free terminal and whitespace-padded done sentinel. Both
// original input and output counts remain available after those observations.
func TestParseSSEUsage_TerminalWithoutUsageKeepsFallback(t *testing.T) {
	usage := &ForwardUsage{}

	ParseSSEUsageBytes([]byte(`{"type":"response.in_progress","usage":{"input_tokens":17,"output_tokens":5}}`), usage)
	ParseSSEUsageBytes([]byte(`{"type":"response.completed","response":{"id":"resp_1"}}`), usage)
	ParseSSEUsageBytes([]byte("  [DONE]\n"), usage)

	require.Equal(t, 17, usage.InputTokens)
	require.Equal(t, 5, usage.OutputTokens)
}

// TestEffectiveOpenAISSEEventTypePrefersPayload compares payload type with
// conflicting or fallback frame labels. It checks selected labels and JSON
// equality for type injection into a payload with a blank type.
func TestEffectiveOpenAISSEEventTypePrefersPayload(t *testing.T) {
	t.Parallel()

	require.Equal(t, "response.failed", EffectiveOpenAISSEEventType([]byte(`{"type":"response.failed"}`), "error"))
	require.Equal(t, "error", EffectiveOpenAISSEEventType([]byte(`{"error":{"message":"failed"}}`), " error "))
	require.JSONEq(t, `{"type":"error","error":{"message":"failed"}}`, OpenAICompatPayloadWithEventType(`{"type":"","error":{"message":"failed"}}`, "error"))
}

// TestExtractOpenAISSETerminalEventUsesFinalAuthoritativeTerminal places an
// error before a completed response in one buffered body. Extraction must
// select the later completion type and its response identity.
func TestExtractOpenAISSETerminalEventUsesFinalAuthoritativeTerminal(t *testing.T) {
	t.Parallel()

	body := "data: {\"type\":\"error\",\"error\":{\"message\":\"recovering\"}}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"status\":\"completed\"}}\n\n"
	eventType, payload, ok := ExtractOpenAISSETerminalEvent(body)
	require.True(t, ok)
	require.Equal(t, "response.completed", eventType)
	require.Equal(t, "resp_1", gjson.GetBytes(payload, "response.id").String())
}

// TestParseSSEUsageEffectiveTerminalRules supplies separate labels for
// progressive, absent, zero, and positive terminal usage. Complete aggregate
// equality checks retained earlier counts and replacement by the positive
// terminal object.
func TestParseSSEUsageEffectiveTerminalRules(t *testing.T) {
	t.Parallel()

	usage := &ForwardUsage{}
	ParseSSEUsageBytesWithType([]byte(`{"usage":{"input_tokens":17,"output_tokens":5,"input_tokens_details":{"cached_tokens":3}}}`), "response.in_progress", usage)
	ParseSSEUsageBytesWithType([]byte(`{"response":{"id":"resp_1"}}`), "response.completed", usage)
	require.Equal(t, ForwardUsage{InputTokens: 17, OutputTokens: 5, CacheReadInputTokens: 3}, *usage)

	ParseSSEUsageBytesWithType([]byte(`{"response":{"usage":{"input_tokens":0,"output_tokens":0,"input_tokens_details":{"cached_tokens":0}}}}`), "response.completed", usage)
	require.Equal(t, ForwardUsage{InputTokens: 17, OutputTokens: 5, CacheReadInputTokens: 3}, *usage)

	ParseSSEUsageBytesWithType([]byte(`{"response":{"usage":{"input_tokens":2,"output_tokens":0,"input_tokens_details":{"cached_tokens":0}}}}`), "response.completed", usage)
	require.Equal(t, ForwardUsage{InputTokens: 2}, *usage)
}

// BenchmarkParseSSEUsageNoUsageDelta measures repeated observation of a text
// delta containing no usage object. Payload construction precedes timing, and
// allocation reporting covers calls using the same aggregate.
func BenchmarkParseSSEUsageNoUsageDelta(b *testing.B) {
	usage := &ForwardUsage{}
	payload := []byte(`{"type":"response.output_text.delta","delta":"hello"}`)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		ParseSSEUsageBytesWithType(payload, "response.output_text.delta", usage)
	}
}

// TestForEachOpenAISSEFrameDataTypeOverridesEventField gives one frame a
// declared in-progress label and a completed payload type. The callback must
// observe only the completed type selected from data.
func TestForEachOpenAISSEFrameDataTypeOverridesEventField(t *testing.T) {
	t.Parallel()

	var types []string
	ForEachOpenAISSEFrame(strings.Join([]string{
		"event: response.in_progress",
		`data: {"type":"response.completed","response":{"id":"resp_1"}}`,
		"",
	}, "\n"), func(eventType string, _ []byte) {
		types = append(types, eventType)
	})
	require.Equal(t, []string{"response.completed"}, types)
}

// benchmarkOpenAIUsageJSONBytes creates a fresh canonical response fixture
// with input, output, and cached-input counts.
func benchmarkOpenAIUsageJSONBytes() []byte {
	return []byte(`{"id":"resp_bench","object":"response","model":"gpt-5.3-codex","usage":{"input_tokens":3210,"output_tokens":987,"input_tokens_details":{"cached_tokens":456}}}`)
}

// legacyExtractOpenAIUsageFromJSONBytes decodes the benchmark's canonical
// input/output/cache fields using encoding/json. A decoding error returns zero
// usage and false; valid input without those fields can return zero usage with
// true, including JSON null.
func legacyExtractOpenAIUsageFromJSONBytes(body []byte) (ForwardUsage, bool) {
	var response struct {
		// Usage retains the benchmark's canonical object fields; omitted values
		// remain zero because this typed fixture decoder performs no presence tracking.
		Usage struct {
			InputTokens  int `json:"input_tokens"`  // canonical input token count
			OutputTokens int `json:"output_tokens"` // canonical output token count
			// InputTokenDetails contributes cached input only, leaving other
			// categories outside the benchmark's small typed decoding shape.
			InputTokenDetails struct {
				CachedTokens int `json:"cached_tokens"` // canonical cached-input token count
			} `json:"input_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return ForwardUsage{}, false
	}
	return ForwardUsage{
		InputTokens:          response.Usage.InputTokens,
		OutputTokens:         response.Usage.OutputTokens,
		CacheReadInputTokens: response.Usage.InputTokenDetails.CachedTokens,
	}, true
}
