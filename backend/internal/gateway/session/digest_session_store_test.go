package session

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/pkg/querycache"
	protocolgemini "github.com/TokenFlux/TokenRouter/internal/protocol/gemini"
)

// TestDigestSessionStore_SaveAndFind stores one exact digest binding within a group
// and source namespace. Lookup returns the saved session UUID and provider with
// the presence flag set, confirming the binding is usable.
func TestDigestSessionStore_SaveAndFind(t *testing.T) {
	store := NewDigestSessionStore()

	store.Save(1, "prefix", "s:a1-u:b2-m:c3", "uuid-1", 100, "")

	uuid, providerID, _, found := store.Find(1, "prefix", "s:a1-u:b2-m:c3")
	require.True(t, found)
	assert.Equal(t, "uuid-1", uuid)
	assert.Equal(t, int64(100), providerID)
}

// TestDigestSessionStore_PrefixMatch stores an earlier conversation and queries a longer chain.
// The shorter stored prefix supplies the same UUID, provider, and matched chain.
func TestDigestSessionStore_PrefixMatch(t *testing.T) {
	store := NewDigestSessionStore()

	// Store the earlier conversation prefix.
	store.Save(1, "prefix", "u:a-m:b", "uuid-short", 10, "")

	// A later turn resolves to the stored shorter prefix.
	uuid, providerID, matchedChain, found := store.Find(1, "prefix", "u:a-m:b-u:c-m:d")
	require.True(t, found)
	assert.Equal(t, "uuid-short", uuid)
	assert.Equal(t, int64(10), providerID)
	assert.Equal(t, "u:a-m:b", matchedChain)
}

// TestDigestSessionStore_LongestPrefixMatch stores three increasingly long digest prefixes.
// Lookup selects the longest matching one, then uses a shorter binding when the
// next component differs.
func TestDigestSessionStore_LongestPrefixMatch(t *testing.T) {
	store := NewDigestSessionStore()

	store.Save(1, "prefix", "u:a", "uuid-1", 1, "")
	store.Save(1, "prefix", "u:a-m:b", "uuid-2", 2, "")
	store.Save(1, "prefix", "u:a-m:b-u:c", "uuid-3", 3, "")

	// Truncation selects the longest stored prefix, "u:a-m:b-u:c".
	uuid, providerID, _, found := store.Find(1, "prefix", "u:a-m:b-u:c-m:d-u:e")
	require.True(t, found)
	assert.Equal(t, "uuid-3", uuid)
	assert.Equal(t, int64(3), providerID)

	// A different third item matches the shorter "u:a-m:b" prefix.
	uuid, providerID, _, found = store.Find(1, "prefix", "u:a-m:b-u:x")
	require.True(t, found)
	assert.Equal(t, "uuid-2", uuid)
	assert.Equal(t, int64(2), providerID)
}

// TestDigestSessionStore_SaveDeletesOldChain replaces an earlier conversation
// key with its longer chain.
// The old chain stops matching and the replacement retains its UUID and provider.
func TestDigestSessionStore_SaveDeletesOldChain(t *testing.T) {
	store := NewDigestSessionStore()

	// Store the first turn.
	store.Save(1, "prefix", "u:a-m:b", "uuid-1", 100, "")

	// Extend the same session and supply the prior chain for replacement.
	store.Save(1, "prefix", "u:a-m:b-u:c-m:d", "uuid-1", 100, "u:a-m:b")

	// The prior chain must be removed.
	_, _, _, found := store.Find(1, "prefix", "u:a-m:b")
	assert.False(t, found, "old chain should be deleted")

	// The replacement chain remains available.
	uuid, providerID, _, found := store.Find(1, "prefix", "u:a-m:b-u:c-m:d")
	require.True(t, found)
	assert.Equal(t, "uuid-1", uuid)
	assert.Equal(t, int64(100), providerID)
}

// TestDigestSessionStore_DifferentSessionsNoInterference stores different
// first-user digests sharing a system component.
// Each extended conversation recovers its own UUID and provider from the distinct stored prefix.
func TestDigestSessionStore_DifferentSessionsNoInterference(t *testing.T) {
	store := NewDigestSessionStore()

	// Shared system instructions must not merge different user conversations.
	store.Save(1, "prefix", "s:sys-u:user1", "uuid-1", 100, "")
	store.Save(1, "prefix", "s:sys-u:user2", "uuid-2", 200, "")

	uuid, providerID, _, found := store.Find(1, "prefix", "s:sys-u:user1-m:reply1")
	require.True(t, found)
	assert.Equal(t, "uuid-1", uuid)
	assert.Equal(t, int64(100), providerID)

	uuid, providerID, _, found = store.Find(1, "prefix", "s:sys-u:user2-m:reply2")
	require.True(t, found)
	assert.Equal(t, "uuid-2", uuid)
	assert.Equal(t, int64(200), providerID)
}

// TestDigestSessionStore_NoMatch queries a digest unrelated to the stored chain.
// It returns found=false after the supplied candidate and its shorter prefixes fail to match.
func TestDigestSessionStore_NoMatch(t *testing.T) {
	store := NewDigestSessionStore()

	store.Save(1, "prefix", "u:a-m:b", "uuid-1", 100, "")

	// An unrelated chain has no matching prefix.
	_, _, _, found := store.Find(1, "prefix", "u:x-m:y")
	assert.False(t, found)
}

// TestDigestSessionStore_DifferentPrefixHash stores a chain under one source
// prefix and reads another.
// The identical chain returns no match when its request-source namespace differs.
func TestDigestSessionStore_DifferentPrefixHash(t *testing.T) {
	store := NewDigestSessionStore()

	store.Save(1, "prefix1", "u:a-m:b", "uuid-1", 100, "")

	// Prefix hashes isolate otherwise identical chains.
	_, _, _, found := store.Find(1, "prefix2", "u:a-m:b")
	assert.False(t, found)
}

// TestDigestSessionStore_DifferentGroupID stores a chain for one group and reads another.
// The identical source hash and chain return no match in that other group namespace.
func TestDigestSessionStore_DifferentGroupID(t *testing.T) {
	store := NewDigestSessionStore()

	store.Save(1, "prefix", "u:a-m:b", "uuid-1", 100, "")

	// Groups isolate otherwise identical chains.
	_, _, _, found := store.Find(2, "prefix", "u:a-m:b")
	assert.False(t, found)
}

// TestDigestSessionStore_EmptyDigestChain supplies an empty chain to saving and lookup.
// The ignored save leaves no match for the empty lookup, even with a supplied UUID and provider.
func TestDigestSessionStore_EmptyDigestChain(t *testing.T) {
	store := NewDigestSessionStore()

	// Empty chains are not stored.
	store.Save(1, "prefix", "", "uuid-1", 100, "")
	_, _, _, found := store.Find(1, "prefix", "")
	assert.False(t, found)
}

// TestDigestSessionStore_TTLExpiration uses a short-lived TTL map to check
// read-time expiration. The binding matches immediately and stops matching
// after the test waits past its configured deadline.
func TestDigestSessionStore_TTLExpiration(t *testing.T) {
	store := &DigestSessionStore{
		cache: querycache.NewTTLMap[*sessionEntry](100 * time.Millisecond),
	}

	store.Save(1, "prefix", "u:a-m:b", "uuid-1", 100, "")

	// The binding is available before expiry.
	_, _, _, found := store.Find(1, "prefix", "u:a-m:b")
	require.True(t, found)

	// Wait two configured TTLs so the subsequent lookup observes and evicts the
	// expired binding without relying on a background cleanup goroutine.
	time.Sleep(200 * time.Millisecond)

	// Expired bindings must not match.
	_, _, _, found = store.Find(1, "prefix", "u:a-m:b")
	assert.False(t, found)
}

// TestDigestSessionStore_ConcurrentSafety runs overlapping save and lookup
// loops under five source prefixes.
// All worker loops join; returned lookup values are discarded, so this test
// does not assert concurrent replacement ordering.
func TestDigestSessionStore_ConcurrentSafety(t *testing.T) {
	store := NewDigestSessionStore()

	var wg sync.WaitGroup
	// Fifty workers provide overlapping cache access for this fixture workload.
	const goroutines = 50
	// Each worker adds one hundred distinct chains to exercise repeated access.
	const operations = 100

	// Join all workers before returning so concurrent cache operations finish
	// within this test; their lookup values are intentionally not asserted here.
	wg.Add(goroutines)
	for g := range goroutines {
		go func(id int) {
			defer wg.Done()
			prefix := fmt.Sprintf("prefix-%d", id%5)
			for i := range operations {
				chain := fmt.Sprintf("u:%d-m:%d", id, i)
				uuid := fmt.Sprintf("uuid-%d-%d", id, i)
				store.Save(1, prefix, chain, uuid, int64(id), "")
				store.Find(1, prefix, chain)
			}
		}(g)
	}
	wg.Wait()
}

// TestDigestSessionStore_MultipleSessions stores three chains with distinct UUID/provider bindings.
// Exact lookups recover each pair and an extension of the second chain reuses its binding.
func TestDigestSessionStore_MultipleSessions(t *testing.T) {
	store := NewDigestSessionStore()

	sessions := []struct {
		chain      string // stored conversation digest chain
		uuid       string // expected session UUID value
		providerID int64  // expected bound provider identifier
	}{
		{"u:session1", "uuid-1", 1},
		{"u:session2-m:reply2", "uuid-2", 2},
		{"u:session3-m:reply3-u:msg3", "uuid-3", 3},
	}

	for _, sess := range sessions {
		store.Save(1, "prefix", sess.chain, sess.uuid, sess.providerID, "")
	}

	// Each chain retains its own session binding.
	for _, sess := range sessions {
		uuid, providerID, _, found := store.Find(1, "prefix", sess.chain)
		require.True(t, found, "should find session: %s", sess.chain)
		assert.Equal(t, sess.uuid, uuid)
		assert.Equal(t, sess.providerID, providerID)
	}

	// A later turn reuses the existing conversation.
	uuid, providerID, _, found := store.Find(1, "prefix", "u:session2-m:reply2-u:newmsg")
	require.True(t, found)
	assert.Equal(t, "uuid-2", uuid)
	assert.Equal(t, int64(2), providerID)
}

// TestDigestSessionStore_Performance1000Sessions populates a thousand sessions
// and times repeated extension lookups.
// Every lookup must match; elapsed timing is logged and has no acceptance threshold.
func TestDigestSessionStore_Performance1000Sessions(t *testing.T) {
	store := NewDigestSessionStore()

	// Populate the cache before measuring lookups.
	for i := range 1000 {
		chain := fmt.Sprintf("s:sys-u:user%d-m:reply%d", i, i)
		store.Save(1, "prefix", chain, fmt.Sprintf("uuid-%d", i), int64(i), "")
	}

	// Measure repeated prefix lookups across the populated cache.
	start := time.Now()
	// Ten thousand lookups produce a repeatable timing workload without a speed threshold.
	const lookups = 10000
	for i := range lookups {
		idx := i % 1000
		chain := fmt.Sprintf("s:sys-u:user%d-m:reply%d-u:newmsg", idx, idx)
		_, _, _, found := store.Find(1, "prefix", chain)
		assert.True(t, found)
	}
	elapsed := time.Since(start)
	t.Logf("%d lookups in %v (%.0f ns/op)", lookups, elapsed, float64(elapsed.Nanoseconds())/lookups)
}

// TestDigestSessionStore_FindReturnsMatchedChain queries an exact chain and a longer extension.
// Both results return the stored matched chain for a caller to pass to replacement saving.
func TestDigestSessionStore_FindReturnsMatchedChain(t *testing.T) {
	store := NewDigestSessionStore()

	store.Save(1, "prefix", "u:a-m:b-u:c", "uuid-1", 100, "")

	// Exact match.
	_, _, matchedChain, found := store.Find(1, "prefix", "u:a-m:b-u:c")
	require.True(t, found)
	assert.Equal(t, "u:a-m:b-u:c", matchedChain)

	// Prefix match after truncation.
	_, _, matchedChain, found = store.Find(1, "prefix", "u:a-m:b-u:c-m:d-u:e")
	require.True(t, found)
	assert.Equal(t, "u:a-m:b-u:c", matchedChain)
}

// TestDigestSessionStore_CacheItemCountStable extends one hundred conversations
// sequentially across ten rounds.
// Passing each matched chain to Save leaves no more than one hundred physical
// entries in this workload.
func TestDigestSessionStore_CacheItemCountStable(t *testing.T) {
	store := NewDigestSessionStore()

	// Simulate 100 conversations with ten turns each.
	// Passing oldDigestChain replaces the previous key for each conversation.
	for conv := range 100 {
		var prevMatchedChain string
		for round := range 10 {
			chain := fmt.Sprintf("s:sys-u:user%d", conv)
			for r := range round {
				chain += fmt.Sprintf("-m:a%d-u:q%d", r, r+1)
			}
			uuid := fmt.Sprintf("uuid-conv%d", conv)

			_, _, matched, _ := store.Find(1, "prefix", chain)
			store.Save(1, "prefix", chain, uuid, int64(conv), matched)
			prevMatchedChain = matched
			_ = prevMatchedChain
		}
	}

	// One key per conversation bounds this sequential workload at 100 entries.
	// Saving later turns must not accumulate a key per turn.
	itemCount := store.cache.ItemCount()
	assert.LessOrEqual(t, itemCount, 100, "cache should have at most 100 items (1 per conversation), got %d", itemCount)
	t.Logf("Cache item count after 100 conversations × 10 rounds: %d", itemCount)
}

// TestDigestSessionStore_TTLPreventsUnboundedGrowth writes five hundred new
// sessions into a short-TTL store and invokes ExpireRuntimeCaches after the
// deadline passes. Sweeping removes every unread expired entry physically.
func TestDigestSessionStore_TTLPreventsUnboundedGrowth(t *testing.T) {
	// A short TTL checks physical sweep cleanup after a burst of new sessions.
	store := &DigestSessionStore{
		cache: querycache.NewTTLMap[*sessionEntry](100 * time.Millisecond),
	}

	// All 500 writes start new sessions, so no prior chain can be removed.
	for i := range 500 {
		chain := fmt.Sprintf("u:user%d", i)
		store.Save(1, "prefix", chain, fmt.Sprintf("uuid-%d", i), int64(i), "")
	}

	assert.Equal(t, 500, store.cache.ItemCount())

	// Wait two configured TTLs before triggering the runtime sweep that production
	// schedules on its timing wheel to reclaim unread expired entries.
	time.Sleep(200 * time.Millisecond)
	store.ExpireRuntimeCaches()

	assert.Equal(t, 0, store.cache.ItemCount(), "all items should be expired and cleaned up")
}

// TestDigestSessionStore_SaveSameChainNoDelete saves an identical current and old chain.
// The replayed binding remains present with its UUID and provider because
// replacement cleanup skips equal chains.
func TestDigestSessionStore_SaveSameChainNoDelete(t *testing.T) {
	store := NewDigestSessionStore()

	// Store the initial chain.
	store.Save(1, "prefix", "u:a-m:b", "uuid-1", 100, "")

	// Replaying the same chain must not delete the freshly written binding.
	store.Save(1, "prefix", "u:a-m:b", "uuid-1", 100, "u:a-m:b")

	// The replayed binding remains available.
	uuid, providerID, _, found := store.Find(1, "prefix", "u:a-m:b")
	require.True(t, found)
	assert.Equal(t, "uuid-1", uuid)
	assert.Equal(t, int64(100), providerID)
}

// TestGeminiSessionContinuousConversation builds three growing Gemini transcripts.
// The second and third turns recover the first turn's session and provider through
// stored prefixes while matched-chain replacement advances the saved conversation.
func TestGeminiSessionContinuousConversation(t *testing.T) {
	store := NewDigestSessionStore()
	groupID := int64(1)
	prefixHash := "test_prefix_hash"
	sessionUUID := "session-uuid-12345"
	providerID := int64(100)

	// First conversation turn.
	req1 := &protocolgemini.GeminiRequest{
		SystemInstruction: &protocolgemini.GeminiContent{
			Parts: []protocolgemini.GeminiPart{{Text: "You are a helpful assistant"}},
		},
		Contents: []protocolgemini.GeminiContent{
			{Role: "user", Parts: []protocolgemini.GeminiPart{{Text: "Hello, what's your name?"}}},
		},
	}
	chain1 := BuildGeminiDigestChain(req1)
	t.Logf("Round 1 chain: %s", chain1)

	// The first turn has no existing session.
	_, _, _, found := store.Find(groupID, prefixHash, chain1)
	if found {
		t.Error("Round 1: should not find existing session")
	}

	// The first save has no old chain to replace.
	store.Save(groupID, prefixHash, chain1, sessionUUID, providerID, "")

	// The second turn includes the first exchange.
	req2 := &protocolgemini.GeminiRequest{
		SystemInstruction: &protocolgemini.GeminiContent{
			Parts: []protocolgemini.GeminiPart{{Text: "You are a helpful assistant"}},
		},
		Contents: []protocolgemini.GeminiContent{
			{Role: "user", Parts: []protocolgemini.GeminiPart{{Text: "Hello, what's your name?"}}},
			{Role: "model", Parts: []protocolgemini.GeminiPart{{Text: "I'm Claude, nice to meet you!"}}},
			{Role: "user", Parts: []protocolgemini.GeminiPart{{Text: "What can you do?"}}},
		},
	}
	chain2 := BuildGeminiDigestChain(req2)
	t.Logf("Round 2 chain: %s", chain2)

	// The first-turn prefix resolves the second turn to the same session.
	foundUUID, foundAccID, matchedChain, found := store.Find(groupID, prefixHash, chain2)
	if !found {
		t.Error("Round 2: should find session via prefix matching")
	}
	if foundUUID != sessionUUID {
		t.Errorf("Round 2: expected UUID %s, got %s", sessionUUID, foundUUID)
	}
	if foundAccID != providerID {
		t.Errorf("Round 2: expected providerID %d, got %d", providerID, foundAccID)
	}

	// Supply Find's matchedChain to replace the first-turn key.
	store.Save(groupID, prefixHash, chain2, sessionUUID, providerID, matchedChain)

	// The third turn extends the same conversation.
	req3 := &protocolgemini.GeminiRequest{
		SystemInstruction: &protocolgemini.GeminiContent{
			Parts: []protocolgemini.GeminiPart{{Text: "You are a helpful assistant"}},
		},
		Contents: []protocolgemini.GeminiContent{
			{Role: "user", Parts: []protocolgemini.GeminiPart{{Text: "Hello, what's your name?"}}},
			{Role: "model", Parts: []protocolgemini.GeminiPart{{Text: "I'm Claude, nice to meet you!"}}},
			{Role: "user", Parts: []protocolgemini.GeminiPart{{Text: "What can you do?"}}},
			{Role: "model", Parts: []protocolgemini.GeminiPart{{Text: "I can help with coding, writing, and more!"}}},
			{Role: "user", Parts: []protocolgemini.GeminiPart{{Text: "Great, help me write some Go code"}}},
		},
	}
	chain3 := BuildGeminiDigestChain(req3)
	t.Logf("Round 3 chain: %s", chain3)

	// The second-turn prefix resolves the third turn to the same session.
	foundUUID, foundAccID, _, found = store.Find(groupID, prefixHash, chain3)
	if !found {
		t.Error("Round 3: should find session via prefix matching")
	}
	if foundUUID != sessionUUID {
		t.Errorf("Round 3: expected UUID %s, got %s", sessionUUID, foundUUID)
	}
	if foundAccID != providerID {
		t.Errorf("Round 3: expected providerID %d, got %d", providerID, foundAccID)
	}
}

// TestGeminiSessionDifferentConversations stores one Gemini user prompt and queries
// another under the same source namespace. The unrelated prompt returns no match,
// so its digest does not reuse the first session.
func TestGeminiSessionDifferentConversations(t *testing.T) {
	store := NewDigestSessionStore()
	groupID := int64(1)
	prefixHash := "test_prefix_hash"

	// First conversation.
	req1 := &protocolgemini.GeminiRequest{
		Contents: []protocolgemini.GeminiContent{
			{Role: "user", Parts: []protocolgemini.GeminiPart{{Text: "Tell me about Go programming"}}},
		},
	}
	chain1 := BuildGeminiDigestChain(req1)
	store.Save(groupID, prefixHash, chain1, "session-1", 100, "")

	// A different user prompt starts a separate conversation.
	req2 := &protocolgemini.GeminiRequest{
		Contents: []protocolgemini.GeminiContent{
			{Role: "user", Parts: []protocolgemini.GeminiPart{{Text: "What's the weather today?"}}},
		},
	}
	chain2 := BuildGeminiDigestChain(req2)

	// Unrelated conversations must not match.
	_, _, _, found := store.Find(groupID, prefixHash, chain2)
	if found {
		t.Error("Different conversations should not match")
	}
}

// TestGeminiSessionPrefixMatchingOrder stores three conversation prefixes under
// different providers. A longer request returns provider three from the longest
// stored prefix, making the lookup priority observable.
func TestGeminiSessionPrefixMatchingOrder(t *testing.T) {
	store := NewDigestSessionStore()
	groupID := int64(1)
	prefixHash := "test_prefix_hash"

	// Bind successive prefixes to different providers to expose matching priority.
	store.Save(groupID, prefixHash, "s:sys-u:q1", "session-round1", 1, "")
	store.Save(groupID, prefixHash, "s:sys-u:q1-m:a1", "session-round2", 2, "")
	store.Save(groupID, prefixHash, "s:sys-u:q1-m:a1-u:q2", "session-round3", 3, "")

	// The longest stored prefix belongs to provider 3.
	_, accID, _, found := store.Find(groupID, prefixHash, "s:sys-u:q1-m:a1-u:q2-m:a2")
	if !found {
		t.Error("Should find session")
	}
	if accID != 3 {
		t.Errorf("Should match longest prefix (provider 3), got provider %d", accID)
	}
}
