package session

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpenAIWSStateStoreInvalidEncryptedContentLineage(t *testing.T) {
	t.Parallel()

	t.Run("mark_get_roundtrip_with_merge", func(t *testing.T) {
		t.Parallel()
		store := NewOpenAIWSStateStore(nil)
		require.False(t, store.HasAnySessionInvalidEncryptedContent())
		require.Nil(t, store.GetSessionInvalidEncryptedContentDigests(1, "session-a"))

		store.MarkSessionInvalidEncryptedContent(1, "session-a", []string{"d1", "d2"}, time.Minute)
		store.MarkSessionInvalidEncryptedContent(1, "session-a", []string{"d2", "d3"}, time.Minute)
		require.True(t, store.HasAnySessionInvalidEncryptedContent())

		digests := store.GetSessionInvalidEncryptedContentDigests(1, "session-a")
		require.Len(t, digests, 3)
		for _, digest := range []string{"d1", "d2", "d3"} {
			require.Contains(t, digests, digest)
		}
		// 组隔离：另一组同名会话不可见。
		require.Nil(t, store.GetSessionInvalidEncryptedContentDigests(2, "session-a"))
		// 返回值是拷贝，调用方修改不得影响存储。
		digests["d4"] = struct{}{}
		require.Len(t, store.GetSessionInvalidEncryptedContentDigests(1, "session-a"), 3)
	})

	t.Run("expired_binding_not_returned", func(t *testing.T) {
		t.Parallel()
		raw := NewOpenAIWSStateStore(nil)
		raw.MarkSessionInvalidEncryptedContent(1, "session-b", []string{"d1"}, time.Minute)
		store, ok := raw.(*defaultOpenAIWSStateStore)
		require.True(t, ok)
		store.sessionInvalidEncryptedMu.Lock()
		binding := store.sessionInvalidEncrypted["1:session-b"]
		binding.expiresAt = time.Now().Add(-time.Second)
		store.sessionInvalidEncrypted["1:session-b"] = binding
		store.sessionInvalidEncryptedMu.Unlock()
		require.Nil(t, store.GetSessionInvalidEncryptedContentDigests(1, "session-b"))
	})

	t.Run("per_session_capacity_degrades_gracefully", func(t *testing.T) {
		t.Parallel()
		store := NewOpenAIWSStateStore(nil)
		oversized := make([]string, 0, openAIWSInvalidEncryptedDigestsPerSession+10)
		for i := range openAIWSInvalidEncryptedDigestsPerSession + 10 {
			oversized = append(oversized, fmt.Sprintf("digest-%d", i))
		}
		store.MarkSessionInvalidEncryptedContent(1, "session-c", oversized, time.Minute)
		digests := store.GetSessionInvalidEncryptedContentDigests(1, "session-c")
		require.Len(t, digests, openAIWSInvalidEncryptedDigestsPerSession)
	})
}
