package session

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestGeminiToolCallsScopeAndExpiry verifies that replay can be repeated by
// the same caller and destination. Changing scope or expiring the original
// record must make the lookup miss.
func TestGeminiToolCallsScopeAndExpiry(t *testing.T) {
	var calls GeminiToolCalls
	scope := GeminiToolScope{APIKeyID: 1, ProviderID: 2, Model: "gemini"}
	calls.Remember(scope, "client-call", "original-id", "signature")
	for range 2 {
		id, signature, ok := calls.Lookup(scope, "client-call")
		require.True(t, ok)
		require.Equal(t, "original-id", id)
		require.Equal(t, "signature", signature)
	}
	other := scope
	other.APIKeyID++
	_, _, ok := calls.Lookup(other, "client-call")
	require.False(t, ok)

	entry := calls.order.Front()
	expired, ok := entry.Value.(geminiToolEntry)
	require.True(t, ok)
	expired.expires = time.Now().Add(-time.Second)
	entry.Value = expired
	_, _, ok = calls.Lookup(scope, "client-call")
	require.False(t, ok)
	require.Zero(t, calls.bytes)
	require.Empty(t, calls.entries)
}

// TestGeminiToolCallsEvictsOldestWithinBudget fills the metadata budget with
// two large signatures. The newer call stays available, and an unsigned
// call is recorded as a hit even when its original fields are empty.
func TestGeminiToolCallsEvictsOldestWithinBudget(t *testing.T) {
	var calls GeminiToolCalls
	scope := GeminiToolScope{APIKeyID: 1, ProviderID: 2, Model: "gemini"}
	// A shared fixture string still counts for each entry because two
	// distinct upstream responses would hold separate copies.
	signature := strings.Repeat("x", geminiToolCallMaxBytes/2)
	calls.Remember(scope, "first", "first", signature)
	calls.Remember(scope, "second", "second", signature)
	_, _, ok := calls.Lookup(scope, "first")
	require.False(t, ok)
	id, _, ok := calls.Lookup(scope, "second")
	require.True(t, ok)
	require.Equal(t, "second", id)

	calls.Remember(scope, "unsigned", "", "")
	id, opaque, ok := calls.Lookup(scope, "unsigned")
	require.True(t, ok)
	require.Empty(t, id)
	require.Empty(t, opaque)
}
