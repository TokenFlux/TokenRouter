package moderation

import (
	"strconv"
	"strings"
	"testing"
	"unsafe"

	"github.com/stretchr/testify/require"
)

// TestContentModerationAuditInputAppliesSourceLimitsAndToggles applies
// independent user and tool limits to mixed text and images. It checks retained
// source positions, shorter text, tool-image exclusion, and total image
// exclusion under their respective switches.
func TestContentModerationAuditInputAppliesSourceLimitsAndToggles(t *testing.T) {
	input := ContentModerationInput{
		Text: "abcdef\n123456",
		Items: []ContentModerationInputItem{
			{Index: 0, Source: ContentModerationSourceUser, Type: ContentModerationItemTypeText, Text: "abcdef"},
			{Index: 1, Source: ContentModerationSourceTool, Type: ContentModerationItemTypeText, Text: "123456"},
			{Index: 2, Source: ContentModerationSourceUser, Type: ContentModerationItemTypeImage, ImageRef: "https://example.com/user.png"},
			{Index: 3, Source: ContentModerationSourceTool, Type: ContentModerationItemTypeImage, ImageRef: "https://example.com/tool.png"},
		},
		Images: []string{"https://example.com/user.png", "https://example.com/tool.png"},
		ImageItems: []ContentModerationImage{
			{SourceIndex: 2, Source: ContentModerationSourceUser, Reference: "https://example.com/user.png"},
			{SourceIndex: 3, Source: ContentModerationSourceTool, Reference: "https://example.com/tool.png"},
		},
	}
	cfg := defaultContentModerationConfig()
	cfg.AuditUserTextMaxChars = 3
	cfg.AuditToolOutputMaxChars = 2

	audit := contentModerationAuditInput(input, cfg)

	require.Equal(t, "abc\n12", audit.Text)
	require.Equal(t, input.Images, audit.Images)
	require.Equal(t, []int{2, 3}, []int{audit.ImageItems[0].SourceIndex, audit.ImageItems[1].SourceIndex})

	cfg.AuditToolOutputs = false
	audit = contentModerationAuditInput(input, cfg)
	require.Equal(t, "abc", audit.Text)
	require.Equal(t, []string{"https://example.com/user.png"}, audit.Images)

	cfg.AuditImages = false
	audit = contentModerationAuditInput(input, cfg)
	require.Empty(t, audit.Images)
	require.Empty(t, audit.ImageItems)
}

// TestExtractContentModerationInput_AnthropicAgentToolLoopExtractsToolResult
// supplies an assistant tool call followed by a user-carried tool result. It
// checks that the selected text is the tool reply, its source is tool, and its
// image list is empty.
func TestExtractContentModerationInput_AnthropicAgentToolLoopExtractsToolResult(t *testing.T) {
	body := []byte(`{
		"messages": [
			{"role":"user","content":"调用一下天气工具"},
			{"role":"assistant","content":[{"type":"tool_use","id":"tool_1","name":"weather","input":{}}]},
			{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool_1","content":"晴 25 度"}]}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolAnthropicMessages, body)

	require.Equal(t, "晴 25 度", input.Text)
	require.Empty(t, input.Images)
	require.Equal(t, ContentModerationSourceTool, input.Source)
}

// TestExtractContentModerationInput_AnthropicFirstTurnExtractsUser supplies a
// first-turn Anthropic user message with no assistant separator. It checks that
// the user text is returned as the aggregate moderation input.
func TestExtractContentModerationInput_AnthropicFirstTurnExtractsUser(t *testing.T) {
	body := []byte(`{
		"messages": [
			{"role":"user","content":"Q1"}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolAnthropicMessages, body)

	require.Equal(t, "Q1", input.Text)
}

// TestExtractContentModerationInput_AnthropicMultiTurnExtractsLatestUser
// supplies an earlier user and assistant exchange followed by another user
// message. It checks that the aggregate text contains the later turn alone.
func TestExtractContentModerationInput_AnthropicMultiTurnExtractsLatestUser(t *testing.T) {
	body := []byte(`{
		"messages": [
			{"role":"user","content":"Q1"},
			{"role":"assistant","content":"A1"},
			{"role":"user","content":"Q2"}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolAnthropicMessages, body)

	require.Equal(t, "Q2", input.Text)
}

// TestExtractContentModerationInput_AnthropicStreamResendExtractsResend
// supplies a partial assistant response followed by a user resend message. It
// checks that the later resend text becomes the selected aggregate input.
func TestExtractContentModerationInput_AnthropicStreamResendExtractsResend(t *testing.T) {
	body := []byte(`{
		"messages": [
			{"role":"user","content":"原问题"},
			{"role":"assistant","content":"部分回答……"},
			{"role":"user","content":"重发"}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolAnthropicMessages, body)

	require.Equal(t, "重发", input.Text)
}

// TestExtractContentModerationInput_OpenAIChatAgentToolLoopExtractsToolResult
// supplies a Chat assistant tool call and a subsequent tool reply. It checks
// the reply text, tool source label, and empty image list.
func TestExtractContentModerationInput_OpenAIChatAgentToolLoopExtractsToolResult(t *testing.T) {
	body := []byte(`{
		"messages": [
			{"role":"system","content":"sys"},
			{"role":"user","content":"列出我的订单"},
			{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"orders","arguments":"{}"}}]},
			{"role":"tool","tool_call_id":"call_1","content":"[]"}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolOpenAIChat, body)

	require.Equal(t, "[]", input.Text)
	require.Empty(t, input.Images)
	require.Equal(t, ContentModerationSourceTool, input.Source)
}

// TestExtractContentModerationInput_OpenAIChatMultiTurnExtractsLatestUser
// supplies Chat history with a completed assistant turn and later user message.
// It checks that the selected aggregate text comes from that later user
// message.
func TestExtractContentModerationInput_OpenAIChatMultiTurnExtractsLatestUser(t *testing.T) {
	body := []byte(`{
		"messages": [
			{"role":"user","content":"Q1"},
			{"role":"assistant","content":"A1"},
			{"role":"user","content":"Q2"}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolOpenAIChat, body)

	require.Equal(t, "Q2", input.Text)
}

// TestExtractContentModerationInput_GeminiAgentToolLoopExtractsFunctionResponse
// supplies a model function call followed by its function response. It checks
// that the response object remains JSON text and receives the tool source label
// with no images.
func TestExtractContentModerationInput_GeminiAgentToolLoopExtractsFunctionResponse(t *testing.T) {
	body := []byte(`{
		"contents": [
			{"role":"user","parts":[{"text":"查询天气"}]},
			{"role":"model","parts":[{"functionCall":{"name":"weather","args":{}}}]},
			{"role":"user","parts":[{"functionResponse":{"name":"weather","response":{"temp":25}}}]}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolGemini, body)

	require.JSONEq(t, `{"temp":25}`, input.Text)
	require.Empty(t, input.Images)
	require.Equal(t, ContentModerationSourceTool, input.Source)
}

// TestExtractContentModerationInput_GeminiFirstTurnExtractsUser supplies one
// Gemini user part before any model reply. It checks that its Unicode text
// becomes the aggregate current-turn moderation input.
func TestExtractContentModerationInput_GeminiFirstTurnExtractsUser(t *testing.T) {
	body := []byte(`{
		"contents": [
			{"role":"user","parts":[{"text":"你好"}]}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolGemini, body)

	require.Equal(t, "你好", input.Text)
}

// TestExtractContentModerationInput_GeminiMultiTurnExtractsLatestUser supplies
// a Gemini model reply between two user turns. It checks that the selected
// aggregate text comes from the user entry after that model separator.
func TestExtractContentModerationInput_GeminiMultiTurnExtractsLatestUser(t *testing.T) {
	body := []byte(`{
		"contents": [
			{"role":"user","parts":[{"text":"Q1"}]},
			{"role":"model","parts":[{"text":"A1"}]},
			{"role":"user","parts":[{"text":"Q2"}]}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolGemini, body)

	require.Equal(t, "Q2", input.Text)
}

// TestExtractContentModerationInput_ResponsesAgentToolLoopExtractsToolOutput
// supplies Responses history ending in a function-call output. It checks that
// the output text is selected with tool source and an empty image list.
func TestExtractContentModerationInput_ResponsesAgentToolLoopExtractsToolOutput(t *testing.T) {
	body := []byte(`{
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"运行测试"}]},
			{"type":"function_call","call_id":"call_1","name":"run_tests","arguments":"{}"},
			{"type":"function_call_output","call_id":"call_1","output":"all passed"}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolOpenAIResponses, body)

	require.Equal(t, "all passed", input.Text)
	require.Empty(t, input.Images)
	require.Equal(t, ContentModerationSourceTool, input.Source)
}

// TestExtractContentModerationPromptExcerpt_ResponsesUsesCurrentToolOutput
// supplies an earlier credential-bearing user message and later tool output. It
// checks that both aggregate text and Cyber excerpt contain the current tool
// result.
func TestExtractContentModerationPromptExcerpt_ResponsesUsesCurrentToolOutput(t *testing.T) {
	body := []byte(`{
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"检查这段高风险请求 sk-proj-1234567890abcdef"}]},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"我先看一下"}]},
			{"type":"function_call","call_id":"call_1","name":"run_tests","arguments":"{}"},
			{"type":"function_call_output","call_id":"call_1","output":"all passed"}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolOpenAIResponses, body)
	excerpt := ExtractContentModerationPromptExcerptFromInput(ExtractContentModerationInput(ContentModerationProtocolOpenAIResponses, body))

	require.Equal(t, "all passed", input.Text)
	require.Equal(t, "all passed", excerpt)
}

// TestExtractContentModerationInput_ResponsesLastUserMessageExtracted supplies
// a completed Responses assistant turn followed by another user message. It
// checks that the aggregate moderation text is the user text after that
// separator.
func TestExtractContentModerationInput_ResponsesLastUserMessageExtracted(t *testing.T) {
	body := []byte(`{
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"first"}]},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"answer"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"latest"}]}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolOpenAIResponses, body)

	require.Equal(t, "latest", input.Text)
}

// TestExtractContentModerationInput_ResponsesLastIsAssistantSkipped supplies
// Responses input whose final item is an assistant message. It checks that no
// later text or image remains eligible for current-turn moderation.
func TestExtractContentModerationInput_ResponsesLastIsAssistantSkipped(t *testing.T) {
	body := []byte(`{
		"input":[
			{"type":"message","role":"user","content":[{"type":"input_text","text":"q1"}]},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"a1"}]}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolOpenAIResponses, body)

	require.Empty(t, input.Text)
	require.Empty(t, input.Images)
}

// TestExtractContentModerationInput_ChatCollectsParallelToolResultsAndDeduplicatesImages
// supplies parallel Chat tool replies sharing one image reference. It checks
// retained structured text, excluded earlier request text, one image reference,
// and the tool source label.
func TestExtractContentModerationInput_ChatCollectsParallelToolResultsAndDeduplicatesImages(t *testing.T) {
	body := []byte(`{
		"messages":[
			{"role":"user","content":"old request"},
			{"role":"assistant","tool_calls":[{"id":"one"},{"id":"two"}]},
			{"role":"tool","tool_call_id":"one","content":{"result":"first","image_url":"https://example.com/result.png"}},
			{"role":"tool","tool_call_id":"two","content":[{"type":"text","text":"second"},{"type":"image_url","image_url":{"url":"https://example.com/result.png"}}]}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolOpenAIChat, body)

	require.Contains(t, input.Text, `"result":"first"`)
	require.Contains(t, input.Text, `"text":"second"`)
	require.NotContains(t, input.Text, "old request")
	require.Equal(t, []string{"https://example.com/result.png"}, input.Images)
	require.Equal(t, ContentModerationSourceTool, input.Source)
}

// TestExtractContentModerationInput_ResponsesCollectsAllSupportedToolOutputs
// supplies five supported Responses tool-output variants after a function call.
// It checks their text values and structured object are retained with a common
// tool source.
func TestExtractContentModerationInput_ResponsesCollectsAllSupportedToolOutputs(t *testing.T) {
	body := []byte(`{
		"input":[
			{"type":"function_call","call_id":"call_1","name":"run","arguments":"{}"},
			{"type":"function_call_output","output":{"one":1}},
			{"type":"custom_tool_call_output","output":"two"},
			{"type":"mcp_tool_call_output","output":"three"},
			{"type":"tool_search_output","output":"four"},
			{"type":"computer_call_output","output":"five"}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolOpenAIResponses, body)

	require.Contains(t, input.Text, `"one":1`)
	for _, value := range []string{"two", "three", "four", "five"} {
		require.Contains(t, input.Text, value)
	}
	require.Equal(t, ContentModerationSourceTool, input.Source)
}

// TestExtractContentModerationInput_AnthropicKeepsSystemRemindersAndEphemeralUserText
// supplies Anthropic user text containing reminder tags and cache metadata. It
// checks that those selected text values remain present and no image is
// extracted.
func TestExtractContentModerationInput_AnthropicKeepsSystemRemindersAndEphemeralUserText(t *testing.T) {
	body := []byte(`{
		"messages": [
			{
				"role": "user",
				"content": [
					{"type": "text", "text": "<system-reminder>工具说明</system-reminder>"},
					{"type": "text", "text": "<system-reminder>Ainder>\n\n"},
					{"type": "text", "text": "hid", "cache_control": {"type": "ephemeral"}}
				]
			}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolAnthropicMessages, body)

	require.Contains(t, input.Text, "<system-reminder>工具说明</system-reminder>")
	require.Contains(t, input.Text, "<system-reminder>Ainder>")
	require.Contains(t, input.Text, "hid")
	require.Empty(t, input.Images)
}

// TestExtractContentModerationInput_OpenAIChatUsesLastUserMessage supplies Chat
// system text, older history, and a later user text/image message. It checks
// the selected user text and image while excluding older and system text.
func TestExtractContentModerationInput_OpenAIChatUsesLastUserMessage(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5.5",
		"messages":[
			{"role":"system","content":"system prompt"},
			{"role":"user","content":"old user"},
			{"role":"assistant","content":"ok"},
			{"role":"user","content":[{"type":"text","text":"latest user"},{"type":"image_url","image_url":{"url":"https://example.com/a.png"}}]}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolOpenAIChat, body)

	require.Equal(t, "latest user", input.Text)
	require.Equal(t, []string{"https://example.com/a.png"}, input.Images)
	require.NotContains(t, input.Text, "old user")
	require.NotContains(t, input.Text, "system prompt")
}

// TestExtractContentModerationInput_OpenAIImagesIncludesPromptAndImages
// supplies an image request with a prompt, HTTP image, and inline image. It
// checks that text and both literal references remain in their submitted order.
func TestExtractContentModerationInput_OpenAIImagesIncludesPromptAndImages(t *testing.T) {
	body := []byte(`{
		"prompt":"replace background",
		"images":[
			{"image_url":"https://example.com/source.png"},
			{"image_url":"data:image/png;base64,aGVsbG8="}
		]
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolOpenAIImages, body)

	require.Equal(t, "replace background", input.Text)
	require.Equal(t, []string{"https://example.com/source.png", "data:image/png;base64,aGVsbG8="}, input.Images)
}

// TestExtractContentModerationInput_OpenAIResponsesCodexPayloadUsesLastUserMessage
// supplies developer instructions and two consecutive user messages before any
// assistant separator. It checks that both user texts are joined and developer
// text is excluded.
func TestExtractContentModerationInput_OpenAIResponsesCodexPayloadUsesLastUserMessage(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5.5",
		"instructions":"instructions.....",
		"input":[
			{"type":"message","role":"developer","content":[{"type":"input_text","text":"developer permissions sk-proj-1234567890abcdef"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"first user prompt"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"last user prompt"}]}
		],
		"prompt_cache_key":"cache-key"
	}`)

	input := ExtractContentModerationInput(ContentModerationProtocolOpenAIResponses, body)

	require.Equal(t, "first user prompt\nlast user prompt", input.Text)
	require.Empty(t, input.Images)
	require.NotContains(t, input.Text, "developer permissions")
}

// TestExtractContentModerationInput_ClonesStringsWithoutPinningBody extracts
// text and image references from a user turn backed by a standalone byte slice.
// Extracted strings must own independent storage rather than aliasing the
// caller's request buffer.
func TestExtractContentModerationInput_ClonesStringsWithoutPinningBody(t *testing.T) {
	body := []byte(`{"messages":[{"role":"user","content":[{"type":"text","text":"isolated text"},{"type":"image_url","image_url":{"url":"https://example.com/pic.png"}}]}]}`)
	bodyStart := uintptr(unsafe.Pointer(unsafe.SliceData(body)))
	bodyEnd := bodyStart + uintptr(len(body))

	extracted := ExtractContentModerationInput(ContentModerationProtocolOpenAIChat, body)
	require.Equal(t, "isolated text", extracted.Text)
	require.Equal(t, []string{"https://example.com/pic.png"}, extracted.Images)

	textPtr := uintptr(unsafe.Pointer(unsafe.StringData(extracted.Items[0].Text)))
	imgPtr := uintptr(unsafe.Pointer(unsafe.StringData(extracted.Images[0])))
	require.False(t, textPtr >= bodyStart && textPtr < bodyEnd, "extracted text must not alias body buffer")
	require.False(t, imgPtr >= bodyStart && imgPtr < bodyEnd, "extracted image ref must not alias body buffer")
}

// BenchmarkExtractContentModerationInput_LargeHistory extracts the trailing
// user turn from 1 MiB, 5 MiB, 20 MiB, and 32 MiB Anthropic conversation
// histories. Sub-benchmarks report byte throughput and confirm bounded per-turn
// heap allocations independent of history length.
func BenchmarkExtractContentModerationInput_LargeHistory(b *testing.B) {
	for _, sizeMiB := range []int{1, 5, 20, 32} {
		payload := buildModerationHistoryFixture(sizeMiB << 20)
		b.Run(strconv.Itoa(sizeMiB)+"MiB", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(payload)))
			b.ResetTimer()
			for range b.N {
				_ = ExtractContentModerationInput(ContentModerationProtocolAnthropicMessages, payload)
			}
		})
	}
}

// buildModerationHistoryFixture constructs an Anthropic Messages body of at
// least targetBytes ending with one current-turn user message after history.
func buildModerationHistoryFixture(targetBytes int) []byte {
	out := make([]byte, 0, targetBytes+4096)
	out = append(out, `{"model":"claude-opus-4-6","messages":[`...)
	chunk := strings.Repeat("historical conversation turn text excluded from current audit ", 32)
	turn := 0
	for len(out) < targetBytes {
		if turn > 0 {
			out = append(out, ',')
		}
		out = append(out, `{"role":"user","content":"`...)
		out = append(out, chunk...)
		out = append(out, `"},{"role":"assistant","content":"`...)
		out = append(out, chunk...)
		out = append(out, `"}`...)
		turn++
	}
	out = append(out, `,{"role":"user","content":"final turn prompt to audit"}]}`...)
	return out
}

// TestContentModerationNormalizePreservesFullOriginalText normalizes text with
// surrounding spaces and an embedded newline. It checks that aggregate text and
// the synthesized review item both retain the full supplied spelling.
func TestContentModerationNormalizePreservesFullOriginalText(t *testing.T) {
	input := ContentModerationInput{Text: "  first line\nsecond line  "}

	input.Normalize()

	require.Equal(t, "  first line\nsecond line  ", input.Text)
	require.Equal(t, input.Text, input.Items[0].Text)
}

// TestContentModerationInput_NormalizeKeepsAndBuildsAllImages normalizes
// aggregate text with two inline image references and no review items. It
// checks retained image order and the text-plus-two-image parts produced for
// API input.
func TestContentModerationInput_NormalizeKeepsAndBuildsAllImages(t *testing.T) {
	images := []string{
		"data:image/png;base64,Zmlyc3Q=",
		"data:image/png;base64,c2Vjb25k",
	}
	input := ContentModerationInput{
		Text:   "check image",
		Images: append([]string(nil), images...),
	}
	input.Normalize()

	require.Equal(t, images, input.Images)

	parts, ok := input.ModerationInput().([]moderationAPIInputPart)
	require.True(t, ok)
	require.Len(t, parts, 3)
	require.Equal(t, "text", parts[0].Type)
	require.Equal(t, "image_url", parts[1].Type)
	require.NotNil(t, parts[1].ImageURL)
	require.Equal(t, images[0], parts[1].ImageURL.URL)
	require.Equal(t, images[1], parts[2].ImageURL.URL)
}
