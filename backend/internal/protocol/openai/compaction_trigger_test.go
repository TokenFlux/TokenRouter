package openai

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// TestHasCompactionTriggerInInput_DetectsCompactSignal places a trigger after
// a message in a streaming request. Array inspection must recognize the trigger
// without requiring it to be the sole input item.
func TestHasCompactionTriggerInInput_DetectsCompactSignal(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5.5",
		"stream":true,
		"input":[
			{"type":"message","role":"user","content":"hello"},
			{"type":"compaction_trigger"}
		]
	}`)
	require.True(t, HasCompactionTriggerInInput(body))
}

// TestHasCompactionTriggerInInput_NoTrigger supplies an ordinary message array.
// Detection must return false because the array carries conversation content
// without an item whose type is compaction_trigger.
func TestHasCompactionTriggerInInput_NoTrigger(t *testing.T) {
	body := []byte(`{
		"model":"gpt-5.5",
		"input":[
			{"type":"message","role":"user","content":"hello"}
		]
	}`)
	require.False(t, HasCompactionTriggerInInput(body))
}

// TestHasCompactionTriggerInInput_EmptyInput supplies an explicitly empty input
// array beside a model. Detection must return false because no array item can
// provide the compaction-trigger signal.
func TestHasCompactionTriggerInInput_EmptyInput(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","input":[]}`)
	require.False(t, HasCompactionTriggerInInput(body))
}

// TestHasCompactionTriggerInInput_NoInputField supplies a request with a model
// and no input field. Detection must return false when the request has no input
// array to inspect for a trigger.
func TestHasCompactionTriggerInInput_NoInputField(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5"}`)
	require.False(t, HasCompactionTriggerInInput(body))
}

// TestHasCompactionTriggerInInput_EmptyBody supplies nil and allocated empty
// body slices. Both inputs must return false so empty requests cannot supply
// the compaction-trigger signal to the caller.
func TestHasCompactionTriggerInInput_EmptyBody(t *testing.T) {
	require.False(t, HasCompactionTriggerInInput(nil))
	require.False(t, HasCompactionTriggerInInput([]byte{}))
}

// TestHasCompactionTriggerInInput_StringInput places the trigger label in a
// plain input string. Detection must return false because the signal requires
// a typed item inside an input array.
func TestHasCompactionTriggerInInput_StringInput(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","input":"compaction_trigger"}`)
	require.False(t, HasCompactionTriggerInInput(body))
}

// TestHasCompactionTriggerInInput_CompactTriggerOnly supplies an array containing
// just the trigger object. Detection must return true even when no conversation
// message accompanies the compaction signal.
func TestHasCompactionTriggerInInput_CompactTriggerOnly(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","input":[{"type":"compaction_trigger"}]}`)
	require.True(t, HasCompactionTriggerInInput(body))
}

// TestNormalizeCompactionTriggerInputOrder_MovesAndCollapsesTriggers surrounds
// a message with two triggers. Normalization must report a change and retain
// one message followed by one final trigger in the asserted two-item output.
func TestNormalizeCompactionTriggerInputOrder_MovesAndCollapsesTriggers(t *testing.T) {
	body := []byte(`{"model":"gpt-5.5","input":[{"type":"compaction_trigger"},{"type":"message","role":"user","content":"tail"},{"type":"compaction_trigger"}]}`)
	normalized, changed, err := NormalizeCompactionTriggerInputOrder(body)
	require.NoError(t, err)
	require.True(t, changed)
	items := gjson.GetBytes(normalized, "input").Array()
	require.Len(t, items, 2)
	require.Equal(t, "message", items[0].Get("type").String())
	require.Equal(t, "compaction_trigger", items[1].Get("type").String())
}

// TestNormalizeCompactionTriggerInputOrder_AlreadyFinalPreservesBytes supplies
// one trigger after a message. Normalization must report no change and return
// the supplied bytes because the input already has the required final position.
func TestNormalizeCompactionTriggerInputOrder_AlreadyFinalPreservesBytes(t *testing.T) {
	body := []byte(`{"input":[{"type":"message"},{"type":"compaction_trigger"}]}`)
	normalized, changed, err := NormalizeCompactionTriggerInputOrder(body)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, string(body), string(normalized))
}

// TestNormalizeCompactionTriggerInputOrder_PreservesHistoryAndLargeNumbers moves
// a trigger before message and tool-result history with a large integer field.
// The item types must retain their order, the integer spelling must remain exact,
// and a second pass must return unchanged bytes.
func TestNormalizeCompactionTriggerInputOrder_PreservesHistoryAndLargeNumbers(t *testing.T) {
	body := []byte(`{"input":[{"type":"compaction_trigger"},{"type":"message","id":"msg_1","content":"visible"},{"type":"function_call_output","call_id":"call_1","output":"result","sequence":9007199254740993}]}`)

	normalized, changed, err := NormalizeCompactionTriggerInputOrder(body)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "message", gjson.GetBytes(normalized, "input.0.type").String())
	require.Equal(t, "function_call_output", gjson.GetBytes(normalized, "input.1.type").String())
	require.Equal(t, "9007199254740993", gjson.GetBytes(normalized, "input.1.sequence").Raw)
	require.Equal(t, "compaction_trigger", gjson.GetBytes(normalized, "input.2.type").String())

	second, changedAgain, err := NormalizeCompactionTriggerInputOrder(normalized)
	require.NoError(t, err)
	require.False(t, changedAgain)
	require.Equal(t, normalized, second)
}

// TestNormalizeCompactionTriggerInputOrder_ZeroAllocFastPaths checks allocation
// counts when input carries no trigger or one already-final trigger. Both common
// cases must return the original slice without allocating decoded maps.
func TestNormalizeCompactionTriggerInputOrder_ZeroAllocFastPaths(t *testing.T) {
	noTrigger := []byte(`{"model":"gpt-5.5","input":[{"type":"message","role":"user","content":"` + string(make([]byte, 4096)) + `"}]}`)
	for i := range noTrigger {
		if noTrigger[i] == 0 {
			noTrigger[i] = 'a'
		}
	}
	require.Zero(t, testing.AllocsPerRun(20, func() {
		_, changed, err := NormalizeCompactionTriggerInputOrder(noTrigger)
		if err != nil || changed {
			t.Fatal("unexpected compaction trigger change")
		}
	}))

	alreadyFinal := []byte(`{"model":"gpt-5.5","input":[{"type":"message","role":"user","content":"ok"},{"type":"compaction_trigger"}]}`)
	require.Zero(t, testing.AllocsPerRun(20, func() {
		_, changed, err := NormalizeCompactionTriggerInputOrder(alreadyFinal)
		if err != nil || changed {
			t.Fatal("unexpected compaction trigger reorder")
		}
	}))
}
