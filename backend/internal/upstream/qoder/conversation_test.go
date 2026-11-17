package qoder

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// Conversation fixtures cover expiry and accepted-state rollback using explicit
// versioned snapshots. The concurrent fixture interleaves many commits, while
// focused rollback cases check deletion, restoration, and rejection of an older
// accepted snapshot after a later compatible commit.

// TestQoderConversationStoreExpiresState commits history with a short TTL and
// waits past its expiry. The next plan must replay with system text and its
// supplied message instead of reusing saved state.
func TestQoderConversationStoreExpiresState(t *testing.T) {
	store := NewQoderConversationStore(5 * time.Millisecond)
	messages := []QoderMessage{{Role: "user", Text: "hello"}}

	plan := store.Plan("key", "", nil, messages)
	require.NotNil(t, plan)
	plan.Commit()

	// Wait beyond the short stored TTL so the next planning call exercises
	// expiration lookup and full replay rather than depending on an unexpired
	// snapshot.
	time.Sleep(10 * time.Millisecond)

	next := store.Plan("key", "", nil, messages)
	require.False(t, next.Reused)
	require.True(t, next.IncludeSystem)
	require.Len(t, next.MessagesToSend, 1)
}

// TestQoderConversationStoreConcurrent interleaves fifty workers committing one
// conversation's fingerprints. It checks final session identity and positive
// version; selected rollback calls lack AcceptedState and exercise the early
// guard.
func TestQoderConversationStoreConcurrent(t *testing.T) {
	store := NewQoderConversationStore(5 * time.Minute)

	const numGoroutines = 50
	const numOpsPerGoroutine = 100

	// Every worker uses the same session and fingerprint sequence, making their
	// publications compatible so version advancement can be observed despite
	// interleaved map reads.
	key := "test_conversation_key"
	sessionID := "test_session_id"

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	// Workers capture snapshots and publish compatible history concurrently. The
	// selected rollback calls have no AcceptedState, exercising guard handling
	// during commit traffic.
	for i := range numGoroutines {
		go func(workerID int) {
			defer wg.Done()
			for j := range numOpsPerGoroutine {
				// Capture the state visible to this operation.
				store.Mu.Lock()
				currentState := store.Items[key]
				store.Mu.Unlock()

				// PreviousState is the restoration target if this plan is rolled back.
				plan := &QoderConversationPlan{
					Store:             store,
					Key:               key,
					SessionID:         sessionID,
					SystemFingerprint: "system_v1",
					ToolsFingerprint:  "tools_v1",
					PreviousState:     CloneQoderConversationState(currentState),
				}

				// Each commit increments the version.
				fingerprints := []string{"msg_1", "msg_2", "msg_3"}
				plan.CommitFingerprints(fingerprints)

				// These plans have no accepted snapshot despite their flag. The early guard
				// must leave competing publications available instead of restoring a
				// captured previous state.
				if workerID%2 == 0 && j%10 == 0 {
					plan.AcceptedCommitted = true
					plan.RollbackAccepted()
				}
			}
		}(i)
	}

	wg.Wait()

	// Concurrent commits leave a valid versioned conversation.
	store.Mu.Lock()
	finalState := store.Items[key]
	store.Mu.Unlock()
	if finalState == nil {
		t.Error("expected final state to exist")
		return
	}
	if finalState.SessionID != sessionID {
		t.Errorf("expected sessionID=%s, got=%s", sessionID, finalState.SessionID)
	}
	if finalState.Version <= 0 {
		t.Errorf("expected version > 0, got=%d", finalState.Version)
	}
}

// TestQoderConversationRollbackVersionControl retains an accepted version-one
// snapshot before a competing version-two commit. Rollback must leave version
// two and both committed message fingerprints available.
func TestQoderConversationRollbackVersionControl(t *testing.T) {
	store := NewQoderConversationStore(5 * time.Minute)
	key := "test_key"
	sessionID := "session_1"

	// Establish version 1.
	plan1 := &QoderConversationPlan{
		Store:             store,
		Key:               key,
		SessionID:         sessionID,
		SystemFingerprint: "sys_v1",
		ToolsFingerprint:  "tools_v1",
	}
	plan1.CommitFingerprints([]string{"msg_1"})

	store.Mu.Lock()
	state1 := store.Items[key]
	store.Mu.Unlock()
	if state1 == nil || state1.Version != 1 {
		t.Fatalf("expected version=1, got=%v", state1)
	}

	// Record the state accepted by plan1 for the rollback comparison.
	plan1.PreviousState = CloneQoderConversationState(state1)
	plan1.AcceptedState = CloneQoderConversationState(state1)
	plan1.AcceptedCommitted = true

	// A competing plan advances the conversation to version 2.
	plan2 := &QoderConversationPlan{
		Store:             store,
		Key:               key,
		SessionID:         sessionID,
		SystemFingerprint: "sys_v1",
		ToolsFingerprint:  "tools_v1",
	}
	plan2.CommitFingerprints([]string{"msg_1", "msg_2"})

	store.Mu.Lock()
	state2 := store.Items[key]
	store.Mu.Unlock()
	if state2 == nil || state2.Version != 2 {
		t.Fatalf("expected version=2, got=%v", state2)
	}

	// plan1 still owns an accepted version-one snapshot. The later publication
	// changes rollback equality, so restoring that older snapshot would discard
	// completed history.
	plan1.RollbackAccepted()

	store.Mu.Lock()
	stateFinal := store.Items[key]
	store.Mu.Unlock()
	if stateFinal == nil {
		t.Fatal("expected state to exist after rollback")
		return
	}
	if stateFinal.Version != 2 {
		t.Errorf("rollback should be rejected, expected version=2, got=%d", stateFinal.Version)
	}
	if len(stateFinal.MessageFingerprints) != 2 {
		t.Errorf("rollback should not modify state, expected 2 messages, got=%d", len(stateFinal.MessageFingerprints))
	}
}

// TestQoderConversationRollbackAcceptedDeletesOwnNewState accepts a new
// conversation with no previous snapshot. Rollback must delete that
// still-current accepted state after its initial version is observed.
func TestQoderConversationRollbackAcceptedDeletesOwnNewState(t *testing.T) {
	store := NewQoderConversationStore(5 * time.Minute)
	plan := store.Plan("rollback_new_state", "system", nil, []QoderMessage{{Role: "user", Text: "hello"}})

	plan.CommitAccepted()

	store.Mu.Lock()
	accepted := CloneQoderConversationState(store.Items[plan.Key])
	store.Mu.Unlock()
	if accepted == nil || accepted.Version != 1 {
		t.Fatalf("expected accepted version=1, got=%v", accepted)
	}

	plan.RollbackAccepted()

	store.Mu.Lock()
	final := store.Items[plan.Key]
	store.Mu.Unlock()
	if final != nil {
		t.Fatalf("expected rollback to delete own new accepted state, got=%v", final)
	}
}

// TestQoderConversationRollbackAcceptedRestoresPreviousState accepts a reused
// session after history and usage were committed. Rollback must restore a state
// equal to the previous versioned snapshot, including its usage.
func TestQoderConversationRollbackAcceptedRestoresPreviousState(t *testing.T) {
	store := NewQoderConversationStore(5 * time.Minute)
	key := "rollback_previous_state"
	system := "system"
	firstMessages := []QoderMessage{{Role: "user", Text: "first"}}
	initialPlan := store.Plan(key, system, nil, firstMessages)
	initialPlan.Commit(upstream.TokenUsage{InputTokens: 10, OutputTokens: 2})

	store.Mu.Lock()
	previous := CloneQoderConversationState(store.Items[key])
	store.Mu.Unlock()
	if previous == nil || previous.Version != 1 || !previous.HasUsage {
		t.Fatalf("unexpected previous state: %#v", previous)
	}

	nextMessages := []QoderMessage{
		{Role: "user", Text: "first"},
		{Role: "assistant", Text: "answer"},
		{Role: "user", Text: "next"},
	}
	plan := store.Plan(key, system, nil, nextMessages)
	if !plan.Reused {
		t.Fatal("expected plan to reuse previous conversation")
	}

	plan.CommitAccepted()

	store.Mu.Lock()
	accepted := CloneQoderConversationState(store.Items[key])
	store.Mu.Unlock()
	if accepted == nil || accepted.Version != 2 {
		t.Fatalf("expected accepted version=2, got=%#v", accepted)
	}

	plan.RollbackAccepted()

	store.Mu.Lock()
	final := CloneQoderConversationState(store.Items[key])
	store.Mu.Unlock()
	if !QoderConversationStateEqual(final, previous) {
		t.Fatalf("expected rollback to restore previous state\nprevious=%#v\nfinal=%#v", previous, final)
	}
}

// TestQoderConversationRollbackAcceptedDoesNotClobberConcurrentCommit accepts
// one plan and then completes a compatible competing plan. Rollback of the older
// acceptance must retain the complete version-three snapshot.
func TestQoderConversationRollbackAcceptedDoesNotClobberConcurrentCommit(t *testing.T) {
	store := NewQoderConversationStore(5 * time.Minute)
	key := "rollback_concurrent_commit"
	system := "system"
	firstMessages := []QoderMessage{{Role: "user", Text: "first"}}
	initialPlan := store.Plan(key, system, nil, firstMessages)
	initialPlan.Commit()

	plan := store.Plan(key, system, nil, []QoderMessage{
		{Role: "user", Text: "first"},
		{Role: "assistant", Text: "answer"},
		{Role: "user", Text: "next"},
	})
	plan.CommitAccepted()

	concurrentPlan := store.Plan(key, system, nil, []QoderMessage{
		{Role: "user", Text: "first"},
		{Role: "assistant", Text: "answer"},
		{Role: "user", Text: "next"},
	})
	concurrentPlan.Commit()

	store.Mu.Lock()
	concurrentState := CloneQoderConversationState(store.Items[key])
	store.Mu.Unlock()
	if concurrentState == nil || concurrentState.Version != 3 {
		t.Fatalf("expected concurrent version=3, got=%#v", concurrentState)
	}

	plan.RollbackAccepted()

	store.Mu.Lock()
	final := CloneQoderConversationState(store.Items[key])
	store.Mu.Unlock()
	if !QoderConversationStateEqual(final, concurrentState) {
		t.Fatalf("rollback clobbered concurrent commit\nconcurrent=%#v\nfinal=%#v", concurrentState, final)
	}
}
