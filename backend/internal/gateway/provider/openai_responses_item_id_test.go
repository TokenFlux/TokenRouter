package provider

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// TestOpenAIResponsesInputItemIDPrefixUsesObservedOutputContracts checks
// supported call, message, reasoning, output, and unknown item prefixes. The
// upstream predicate strips listed invalid IDs while retaining valid and
// opaque representations for those type cases.
func TestOpenAIResponsesInputItemIDPrefixUsesObservedOutputContracts(t *testing.T) {
	tests := []struct {
		itemType string // replay input item type
		id       string // incoming replay item identity
		strip    bool   // expected item identity removal
	}{
		{itemType: "message", id: "msg_123", strip: false},
		{itemType: "message", id: "item_123", strip: true},
		{itemType: "reasoning", id: "rs_123", strip: false},
		{itemType: "reasoning", id: "item_123", strip: true},
		{itemType: "function_call", id: "fc_123", strip: false},
		{itemType: "function_call", id: "call_123", strip: true},
		{itemType: "tool_call", id: "fc_123", strip: false},
		{itemType: "local_shell_call", id: "fc_123", strip: false},
		{itemType: "mcp_tool_call", id: "fc_123", strip: false},
		{itemType: "custom_tool_call", id: "ctc_123", strip: false},
		{itemType: "custom_tool_call", id: "fc_123", strip: true},
		{itemType: "tool_search_call", id: "tsc_123", strip: false},
		{itemType: "tool_search_call", id: "fc_123", strip: true},
		{itemType: "web_search_call", id: "ws_123", strip: false},
		{itemType: "web_search_call", id: "item_123", strip: true},
		{itemType: "custom_tool_call_output", id: "fc_123", strip: false},
		{itemType: "custom_tool_call_output", id: "ctco_123", strip: true},
		// Keep IDs on output types with no observed upstream prefix rejection.
		{itemType: "function_call_output", id: "fco_123", strip: false},
		{itemType: "tool_search_output", id: "tso_123", strip: false},
		{itemType: "mcp_tool_call_output", id: "mcpo_123", strip: false},
		{itemType: "future_item", id: "item_123", strip: false},
	}
	for _, tt := range tests {
		t.Run(tt.itemType+"/"+tt.id, func(t *testing.T) {
			require.Equal(t, tt.strip, openai.ShouldStripOpenAIResponsesInputItemID(tt.itemType, tt.id))
		})
	}
}

// TestSanitizeOpenAIResponsesInputItemIDsDoesNotCascadeAcrossIDNamespaces
// mixes invalid call item IDs with paired outputs and opaque references
// bearing equal strings. Cleanup removes rejected IDs while retaining all
// items and their call/reference relationships.
func TestSanitizeOpenAIResponsesInputItemIDsDoesNotCascadeAcrossIDNamespaces(t *testing.T) {
	body := []byte(`{"input":[
		{"type":"function_call","id":"item_bad_call","call_id":"call_valid","name":"lookup","arguments":"{}"},
		{"type":"function_call_output","call_id":"call_valid","output":"preserve paired output"},
		{"type":"function_call_output","call_id":"item_bad_call","output":"preserve opaque output"},
		{"type":"item_reference","id":"item_bad_call"},
		{"type":"item_reference","id":"remote_valid"},
		{"type":"custom_tool_call","id":"ctc_valid","call_id":"ctco_bad_output","name":"apply_patch","input":"patch"},
		{"type":"custom_tool_call_output","id":"ctco_bad_output","call_id":"ctco_bad_output","output":"preserve by call_id"}
	]}`)

	sanitized, changed, err := SanitizeOpenAIResponsesInputItemIDs(body)

	require.NoError(t, err)
	require.True(t, changed)
	items := gjson.GetBytes(sanitized, "input").Array()
	require.Len(t, items, 7)
	require.False(t, items[0].Get("id").Exists())
	require.Equal(t, "call_valid", items[0].Get("call_id").String())
	require.Equal(t, "preserve paired output", items[1].Get("output").String())
	require.Equal(t, "preserve opaque output", items[2].Get("output").String())
	require.Equal(t, "item_bad_call", items[3].Get("id").String())
	require.Equal(t, "remote_valid", items[4].Get("id").String())
	require.Equal(t, "ctc_valid", items[5].Get("id").String())
	require.False(t, items[6].Get("id").Exists())
	require.Equal(t, "ctco_bad_output", items[6].Get("call_id").String())
}

// TestSanitizeOpenAIResponsesInputItemIDsLeavesUnrelatedReferencesUntouched
// supplies a previous-response ID and an opaque item reference needing no
// edits. Sanitation reports false and returns the exact supplied bytes.
func TestSanitizeOpenAIResponsesInputItemIDsLeavesUnrelatedReferencesUntouched(t *testing.T) {
	body := []byte(`{"previous_response_id":"resp_1","input":[{"type":"item_reference","id":"remote_item"}]}`)

	sanitized, changed, err := SanitizeOpenAIResponsesInputItemIDs(body)

	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, body, sanitized)
}

// TestSanitizeOpenAIResponsesInputItemIDsPreservesReferenceToDuplicateRetainedID
// gives a function and custom call the same type-specific ID text. The invalid
// function ID is removed while the valid custom ID and opaque reference
// survive.
func TestSanitizeOpenAIResponsesInputItemIDsPreservesReferenceToDuplicateRetainedID(t *testing.T) {
	body := []byte(`{"input":[{"type":"function_call","id":"ctc_shared","call_id":"call_1"},{"type":"custom_tool_call","id":"ctc_shared","call_id":"call_2"},{"type":"item_reference","id":"ctc_shared"}]}`)

	sanitized, changed, err := SanitizeOpenAIResponsesInputItemIDs(body)

	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, gjson.GetBytes(sanitized, "input.0.id").Exists())
	require.Equal(t, "ctc_shared", gjson.GetBytes(sanitized, "input.1.id").String())
	require.Equal(t, "ctc_shared", gjson.GetBytes(sanitized, "input.2.id").String())
}

// TestSanitizeOpenAIResponsesInputItemIDsPreservesOpaqueOutputsAndReferences
// mixes invalid call identity with opaque outputs and references across
// namespaces. Every item stays in place, valid values survive, and a second
// sanitation returns the same bytes with no change.
func TestSanitizeOpenAIResponsesInputItemIDsPreservesOpaqueOutputsAndReferences(t *testing.T) {
	body := []byte(`{"input":[
		{"type":"function_call","id":"item_shared","call_id":"call_real"},
		{"type":"function_call_output","id":"item_shared","call_id":"item_shared","output":"dangling"},
		{"type":"item_reference","id":"item_shared"},
		{"type":"function_call_output","id":"kept_output","call_id":"call_real","output":"kept"},
		{"type":"item_reference","id":"kept_output"}
	]}`)

	sanitized, changed, err := SanitizeOpenAIResponsesInputItemIDs(body)

	require.NoError(t, err)
	require.True(t, changed)
	require.Len(t, gjson.GetBytes(sanitized, "input").Array(), 5)
	require.False(t, gjson.GetBytes(sanitized, "input.0.id").Exists())
	require.Equal(t, "dangling", gjson.GetBytes(sanitized, "input.1.output").String())
	require.Equal(t, "item_shared", gjson.GetBytes(sanitized, "input.2.id").String())
	require.Equal(t, "kept_output", gjson.GetBytes(sanitized, "input.3.id").String())
	require.Equal(t, "kept_output", gjson.GetBytes(sanitized, "input.4.id").String())

	second, changedAgain, err := SanitizeOpenAIResponsesInputItemIDs(sanitized)
	require.NoError(t, err)
	require.False(t, changedAgain)
	require.Equal(t, sanitized, second)
}

// TestSanitizeOpenAIResponsesInputItemIDsStripsEmptyKnownIDsOnly supplies
// empty IDs on a known message and unknown future item type. The known invalid
// ID is removed while the future item's field remains present.
func TestSanitizeOpenAIResponsesInputItemIDsStripsEmptyKnownIDsOnly(t *testing.T) {
	body := []byte(`{"input":[{"type":"message","id":"","content":"hello"},{"type":"future_item","id":""}]}`)

	sanitized, changed, err := SanitizeOpenAIResponsesInputItemIDs(body)

	require.NoError(t, err)
	require.True(t, changed)
	require.False(t, gjson.GetBytes(sanitized, "input.0.id").Exists())
	require.True(t, gjson.GetBytes(sanitized, "input.1.id").Exists())
}

// TestSanitizeOpenAIResponsesInputItemIDsStripsOnlyNonPairCallIDs mixes
// message/reasoning/image call_ids with paired function, custom, search, and
// shell history. Cleanup removes the non-pair fields while retaining every
// paired call_id.
func TestSanitizeOpenAIResponsesInputItemIDsStripsOnlyNonPairCallIDs(t *testing.T) {
	body := []byte(`{"input":[
		{"type":"message","call_id":"remove_message","content":"hi"},
		{"type":"reasoning","call_id":"remove_reasoning","id":"rs_keep","encrypted_content":"cipher","summary":[]},
		{"type":"image_generation_call","call_id":"remove_image","id":"ig_keep","status":"completed"},
		{"type":"function_call","call_id":"keep_function","name":"lookup","arguments":"{}"},
		{"type":"function_call_output","call_id":"keep_function","output":"ok"},
		{"type":"custom_tool_call","call_id":"keep_custom","name":"patch","input":"x"},
		{"type":"custom_tool_call_output","call_id":"keep_custom","output":"ok"},
		{"type":"tool_search_call","call_id":"keep_search","arguments":"{}"},
		{"type":"tool_search_output","call_id":"keep_search","output":"ok"},
		{"type":"local_shell_call","call_id":"keep_shell","name":"shell","arguments":"{}"}
	]}`)

	sanitized, changed, err := SanitizeOpenAIResponsesInputItemIDs(body)
	require.NoError(t, err)
	require.True(t, changed)
	for i := range 3 {
		require.False(t, gjson.GetBytes(sanitized, "input."+strconv.Itoa(i)+".call_id").Exists())
	}
	for i := 3; i < 10; i++ {
		require.True(t, gjson.GetBytes(sanitized, "input."+strconv.Itoa(i)+".call_id").Exists())
	}
}

// TestSanitizeOpenAIResponsesInputItemIDs_ZeroAllocCleanHistory supplies a
// multi-turn history whose message and call identifiers already meet Responses
// rules. Clean payloads must return the original byte slice unchanged with zero
// heap allocations.
func TestSanitizeOpenAIResponsesInputItemIDs_ZeroAllocCleanHistory(t *testing.T) {
	clean := buildCleanResponsesCompatibilityFixture(64 * 1024)
	allocs := testing.AllocsPerRun(20, func() {
		out, changed, err := SanitizeOpenAIResponsesInputItemIDs(clean)
		if err != nil || changed || len(out) != len(clean) {
			t.Fatal("unexpected item ID sanitation on clean history")
		}
	})
	require.Zero(t, allocs)
}

// BenchmarkSanitizeOpenAIResponsesInputItemIDs_CleanLargeHistory runs item-ID
// screening across 1 MiB, 5 MiB, 20 MiB, and 32 MiB clean Responses histories.
// Each sub-benchmark reports byte throughput and confirms that clean inputs
// avoid heap allocations.
func BenchmarkSanitizeOpenAIResponsesInputItemIDs_CleanLargeHistory(b *testing.B) {
	for _, sizeMiB := range []int{1, 5, 20, 32} {
		payload := buildCleanResponsesCompatibilityFixture(sizeMiB << 20)
		b.Run(strconv.Itoa(sizeMiB)+"MiB", func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(payload)))
			b.ResetTimer()
			for range b.N {
				_, _, _ = SanitizeOpenAIResponsesInputItemIDs(payload)
			}
		})
	}
}

// buildCleanResponsesCompatibilityFixture constructs a valid Responses JSON
// payload of at least targetBytes with valid IDs, tools, and paired calls.
func buildCleanResponsesCompatibilityFixture(targetBytes int) []byte {
	out := make([]byte, 0, targetBytes+8192)
	out = append(out, `{"model":"gpt-5.4","store":false,"stream":true,"tools":[{"type":"function","name":"read_file","parameters":{"type":"object","properties":{"path":{"type":"string"}}}}],"input":[`...)
	chunk := strings.Repeat("func handle(ctx context.Context) error { return nil } ", 36)
	turn := 0
	for len(out) < targetBytes {
		if turn > 0 {
			out = append(out, ',')
		}
		out = append(out, `{"type":"message","id":"msg_`...)
		out = append(out, strconv.Itoa(turn)...)
		out = append(out, `","role":"user","content":[{"type":"input_text","text":"`...)
		out = append(out, chunk...)
		out = append(out, `"}]},{"type":"reasoning","encrypted_content":"enc_`...)
		out = append(out, strconv.Itoa(turn)...)
		out = append(out, `","summary":[]},{"type":"function_call","id":"fc_`...)
		out = append(out, strconv.Itoa(turn)...)
		out = append(out, `","call_id":"call_`...)
		out = append(out, strconv.Itoa(turn)...)
		out = append(out, `","name":"read_file","arguments":"{\"path\":\"main.go\"}"},{"type":"function_call_output","call_id":"call_`...)
		out = append(out, strconv.Itoa(turn)...)
		out = append(out, `","output":"`...)
		out = append(out, chunk...)
		out = append(out, `"}`...)
		turn++
	}
	out = append(out, `]}`...)
	return out
}
