package qoder

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestResponseHistoryRestoresTaskAndParallelCalls verifies output-only
// continuation against an immutable saved response. The original user task,
// two assistant calls, and both tool results remain ordered and independently owned.
func TestResponseHistoryRestoresTaskAndParallelCalls(t *testing.T) {
	store := NewQoderConversationStore(time.Hour)
	metadata := RequestMetadata{APIKeyID: 7}
	request := QoderPayloadRequest{ResponseID: "resp_one", System: "write code", Tools: []any{map[string]any{"name": "echo"}}, Messages: []QoderMessage{{Role: "user", Text: "build concat"}}}
	output := json.RawMessage(`[{"type":"function_call","call_id":"left","name":"echo","arguments":"{\"value\":\"left\"}"},{"type":"function_call","call_id":"right","name":"echo","arguments":"{\"value\":\"right\"}"}]`)
	store.SaveResponseHistory(metadata, request, output)
	request.Messages[0].Text = "mutated input"
	qoderFixtureValue[map[string]any](t, request.Tools[0])["name"] = "mutated tool"
	continuation, err := ParseQoderResponsesPayload([]byte(`{"model":"qfmodel","previous_response_id":"resp_one","input":[{"type":"function_call_output","call_id":"left","output":"left"},{"type":"function_call_output","call_id":"right","output":"right"}]}`))
	require.NoError(t, err)
	restored, err := store.RestoreResponseHistory(metadata, continuation)
	require.NoError(t, err)
	require.False(t, restored.RequiresPreviousResponse)
	require.Len(t, restored.Messages, 4)
	require.Equal(t, "build concat", restored.Messages[0].Text)
	require.Equal(t, "assistant", restored.Messages[1].Role)
	require.Len(t, NormalizeQoderToolCalls(restored.Messages[1].Raw["tool_calls"]), 2)
	require.Equal(t, "left", restored.Messages[2].ToolCallID)
	require.Equal(t, "right", restored.Messages[3].ToolCallID)
	require.Equal(t, "echo", qoderFixtureValue[map[string]any](t, restored.Tools[0])["name"])
	restored.Messages[0].Text = "another mutation"
	again, err := store.RestoreResponseHistory(metadata, continuation)
	require.NoError(t, err)
	require.Equal(t, "build concat", again.Messages[0].Text)
}

// TestResponseHistoryScopeAndExpiry prevents another API key from loading
// saved conversation data. Expiry removes retained bytes, and an incomplete
// continuation receives an explicit error instead of losing its previous task.
func TestResponseHistoryScopeAndExpiry(t *testing.T) {
	store := NewQoderConversationStore(time.Hour)
	metadata := RequestMetadata{APIKeyID: 1}
	store.SaveResponseHistory(metadata, QoderPayloadRequest{ResponseID: "resp_one", Messages: []QoderMessage{{Role: "user", Text: "task"}}}, json.RawMessage(`[]`))
	request := QoderPayloadRequest{PreviousResponseID: "resp_one", Messages: []QoderMessage{{Role: "user", Text: "continue"}}}
	_, err := store.RestoreResponseHistory(RequestMetadata{APIKeyID: 2}, request)
	require.ErrorContains(t, err, "previous Qoder response is unavailable")
	store.Mu.Lock()
	store.pruneResponseHistoriesLocked(time.Now().Add(2 * time.Hour))
	store.Mu.Unlock()
	require.Zero(t, store.responseHistoryBytes)
	_, err = store.RestoreResponseHistory(metadata, request)
	require.Error(t, err)
}

// TestResponseHistoryBoundedRetention evicts oldest immutable responses when
// the entry count reaches its limit. Oversized context is not retained, so
// successful inference cannot create an unbounded in-memory transcript cache.
func TestResponseHistoryBoundedRetention(t *testing.T) {
	store := NewQoderConversationStore(time.Hour)
	for index := range qoderResponseHistoryLimit + 1 {
		store.SaveResponseHistory(RequestMetadata{}, QoderPayloadRequest{ResponseID: fmt.Sprintf("resp_%d", index), Messages: []QoderMessage{{Role: "user", Text: "task"}}}, json.RawMessage(`[]`))
	}
	require.Len(t, store.responseHistories, qoderResponseHistoryLimit)
	_, err := store.RestoreResponseHistory(RequestMetadata{}, QoderPayloadRequest{PreviousResponseID: "resp_0"})
	require.Error(t, err)
	before := store.responseHistoryBytes
	store.SaveResponseHistory(RequestMetadata{}, QoderPayloadRequest{ResponseID: "too_large", System: strings.Repeat("x", qoderResponseHistoryEntryBytes)}, json.RawMessage(`[]`))
	require.Equal(t, before, store.responseHistoryBytes)
	require.LessOrEqual(t, store.responseHistoryBytes, qoderResponseHistoryTotalBytes)
}

// TestResponseHistoryFullReplayDoesNotDuplicatePrefix recognizes a client
// resending its original task and assistant history. The supplied transcript
// remains authoritative, while a shorter new input is appended to saved output.
func TestResponseHistoryFullReplayDoesNotDuplicatePrefix(t *testing.T) {
	store := NewQoderConversationStore(time.Hour)
	metadata := RequestMetadata{APIKeyID: 5}
	first := QoderPayloadRequest{ResponseID: "resp_one", Messages: []QoderMessage{{Role: "user", Text: "first"}}}
	store.SaveResponseHistory(metadata, first, json.RawMessage(`[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"saved"}]}]`))
	full := QoderPayloadRequest{PreviousResponseID: "resp_one", Messages: []QoderMessage{{Role: "user", Text: "first"}, {Role: "assistant", Text: "supplied"}, {Role: "user", Text: "next"}}}
	restored, err := store.RestoreResponseHistory(metadata, full)
	require.NoError(t, err)
	require.Len(t, restored.Messages, 3)
	require.Equal(t, "supplied", restored.Messages[1].Text)
	short := QoderPayloadRequest{PreviousResponseID: "resp_one", Messages: []QoderMessage{{Role: "user", Text: "next"}}}
	restored, err = store.RestoreResponseHistory(metadata, short)
	require.NoError(t, err)
	require.Len(t, restored.Messages, 3)
	require.Equal(t, "saved", restored.Messages[1].Text)
}
