package service

import (
	"context"
	"sync"
	"testing"
	"time"
)

// TestQoderConversationStoreConcurrent 验证 conversation store 在并发访问下的正确性
func TestQoderConversationStoreConcurrent(t *testing.T) {
	store := newQoderConversationStore(5 * time.Minute)

	const numGoroutines = 50
	const numOpsPerGoroutine = 100

	// 使用相同的 key 和 sessionID，但不同的 fingerprints 来触发并发更新
	key := "test_conversation_key"
	sessionID := "test_session_id"

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	// 启动多个 goroutine 并发读写 conversation store
	for i := 0; i < numGoroutines; i++ {
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < numOpsPerGoroutine; j++ {
				// 每次操作都读取当前状态（模拟真实使用场景）
				store.mu.Lock()
				currentState := store.items[key]
				store.mu.Unlock()

				// 创建新 plan，设置 previousState
				plan := &qoderConversationPlan{
					store:             store,
					key:               key,
					sessionID:         sessionID,
					systemFingerprint: "system_v1",
					toolsFingerprint:  "tools_v1",
					previousState:     cloneQoderConversationState(currentState),
				}

				// 提交新的 fingerprints
				fingerprints := []string{"msg_1", "msg_2", "msg_3"}
				plan.commitFingerprints(fingerprints)

				// 模拟偶尔的 rollback 场景
				if workerID%2 == 0 && j%10 == 0 {
					plan.acceptedCommitted = true
					plan.rollbackAccepted()
				}
			}
		}(i)
	}

	wg.Wait()

	// 验证最终状态一致性
	store.mu.Lock()
	finalState := store.items[key]
	store.mu.Unlock()
	if finalState == nil {
		t.Error("expected final state to exist")
		return
	}
	if finalState.sessionID != sessionID {
		t.Errorf("expected sessionID=%s, got=%s", sessionID, finalState.sessionID)
	}
	if finalState.version <= 0 {
		t.Errorf("expected version > 0, got=%d", finalState.version)
	}
}

// TestQoderTokenProviderConcurrent 验证 token provider 在并发访问下的缓存行为
func TestQoderTokenProviderConcurrent(t *testing.T) {
	provider := &QoderTokenProvider{
		sessions: make(map[int64]qoderSessionCacheEntry),
	}

	// 创建测试 account
	account := &Account{
		ID:       12345,
		Platform: PlatformQoder,
		Type:     AccountTypeCosy,
		Credentials: map[string]any{
			"security_oauth_token": "test_oauth_token",
			"machine_id":           "test_machine_id",
			"uid":                  "test_uid",
		},
	}

	const numGoroutines = 50
	const numRequestsPerGoroutine = 20

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	ctx := context.Background()
	successCount := sync.Map{}

	// 并发请求 session
	for i := 0; i < numGoroutines; i++ {
		go func(workerID int) {
			defer wg.Done()
			for j := 0; j < numRequestsPerGoroutine; j++ {
				session, err := provider.GetSession(ctx, account)
				if err != nil {
					t.Logf("worker %d request %d failed: %v", workerID, j, err)
					continue
				}
				if session == nil {
					t.Errorf("worker %d request %d: got nil session", workerID, j)
					continue
				}
				successCount.Store(workerID*1000+j, true)

				// 模拟 invalidate 场景
				if workerID%3 == 0 && j%5 == 0 {
					provider.Invalidate(account.ID)
				}
			}
		}(i)
	}

	wg.Wait()

	// 统计成功率
	count := 0
	successCount.Range(func(key, value any) bool {
		count++
		return true
	})

	expectedTotal := numGoroutines * numRequestsPerGoroutine
	successRate := float64(count) / float64(expectedTotal)
	t.Logf("Success rate: %d/%d (%.2f%%)", count, expectedTotal, successRate*100)

	if successRate < 0.8 {
		t.Errorf("success rate too low: %.2f%%, expected >= 80%%", successRate*100)
	}
}

// TestQoderConversationRollbackVersionControl 验证 rollback 版本控制防止并发覆盖
func TestQoderConversationRollbackVersionControl(t *testing.T) {
	store := newQoderConversationStore(5 * time.Minute)
	key := "test_key"
	sessionID := "session_1"

	// 初始状态：version 1
	plan1 := &qoderConversationPlan{
		store:             store,
		key:               key,
		sessionID:         sessionID,
		systemFingerprint: "sys_v1",
		toolsFingerprint:  "tools_v1",
	}
	plan1.commitFingerprints([]string{"msg_1"})

	store.mu.Lock()
	state1 := store.items[key]
	store.mu.Unlock()
	if state1 == nil || state1.version != 1 {
		t.Fatalf("expected version=1, got=%v", state1)
	}

	// 保存 previousState 用于后续 rollback
	plan1.previousState = cloneQoderConversationState(state1)
	plan1.acceptedState = cloneQoderConversationState(state1)
	plan1.acceptedCommitted = true

	// 另一个 plan 提交新状态：version 2
	plan2 := &qoderConversationPlan{
		store:             store,
		key:               key,
		sessionID:         sessionID,
		systemFingerprint: "sys_v1",
		toolsFingerprint:  "tools_v1",
	}
	plan2.commitFingerprints([]string{"msg_1", "msg_2"})

	store.mu.Lock()
	state2 := store.items[key]
	store.mu.Unlock()
	if state2 == nil || state2.version != 2 {
		t.Fatalf("expected version=2, got=%v", state2)
	}

	// plan1 尝试 rollback（基于 version 1 的 previousState）
	// 应该被拒绝，因为当前 version 已经是 2
	plan1.rollbackAccepted()

	store.mu.Lock()
	stateFinal := store.items[key]
	store.mu.Unlock()
	if stateFinal == nil {
		t.Fatal("expected state to exist after rollback")
		return
	}
	if stateFinal.version != 2 {
		t.Errorf("rollback should be rejected, expected version=2, got=%d", stateFinal.version)
	}
	if len(stateFinal.messageFingerprints) != 2 {
		t.Errorf("rollback should not modify state, expected 2 messages, got=%d", len(stateFinal.messageFingerprints))
	}
}

func TestQoderConversationRollbackAcceptedDeletesOwnNewState(t *testing.T) {
	store := newQoderConversationStore(5 * time.Minute)
	plan := store.plan(
		"rollback_new_state",
		"system",
		nil,
		[]qoderMessage{{Role: "user", Text: "hello"}},
	)

	plan.commitAccepted()

	store.mu.Lock()
	accepted := cloneQoderConversationState(store.items[plan.key])
	store.mu.Unlock()
	if accepted == nil || accepted.version != 1 {
		t.Fatalf("expected accepted version=1, got=%v", accepted)
	}

	plan.rollbackAccepted()

	store.mu.Lock()
	final := store.items[plan.key]
	store.mu.Unlock()
	if final != nil {
		t.Fatalf("expected rollback to delete own new accepted state, got=%v", final)
	}
}

func TestQoderConversationRollbackAcceptedRestoresPreviousState(t *testing.T) {
	store := newQoderConversationStore(5 * time.Minute)
	key := "rollback_previous_state"
	system := "system"
	firstMessages := []qoderMessage{{Role: "user", Text: "first"}}
	initialPlan := store.plan(key, system, nil, firstMessages)
	initialPlan.commit(ClaudeUsage{InputTokens: 10, OutputTokens: 2})

	store.mu.Lock()
	previous := cloneQoderConversationState(store.items[key])
	store.mu.Unlock()
	if previous == nil || previous.version != 1 || !previous.hasUsage {
		t.Fatalf("unexpected previous state: %#v", previous)
	}

	nextMessages := []qoderMessage{
		{Role: "user", Text: "first"},
		{Role: "assistant", Text: "answer"},
		{Role: "user", Text: "next"},
	}
	plan := store.plan(key, system, nil, nextMessages)
	if !plan.reused {
		t.Fatal("expected plan to reuse previous conversation")
	}

	plan.commitAccepted()

	store.mu.Lock()
	accepted := cloneQoderConversationState(store.items[key])
	store.mu.Unlock()
	if accepted == nil || accepted.version != 2 {
		t.Fatalf("expected accepted version=2, got=%#v", accepted)
	}

	plan.rollbackAccepted()

	store.mu.Lock()
	final := cloneQoderConversationState(store.items[key])
	store.mu.Unlock()
	if !qoderConversationStateEqual(final, previous) {
		t.Fatalf("expected rollback to restore previous state\nprevious=%#v\nfinal=%#v", previous, final)
	}
}

func TestQoderConversationRollbackAcceptedDoesNotClobberConcurrentCommit(t *testing.T) {
	store := newQoderConversationStore(5 * time.Minute)
	key := "rollback_concurrent_commit"
	system := "system"
	firstMessages := []qoderMessage{{Role: "user", Text: "first"}}
	initialPlan := store.plan(key, system, nil, firstMessages)
	initialPlan.commit()

	plan := store.plan(key, system, nil, []qoderMessage{
		{Role: "user", Text: "first"},
		{Role: "assistant", Text: "answer"},
		{Role: "user", Text: "next"},
	})
	plan.commitAccepted()

	concurrentPlan := store.plan(key, system, nil, []qoderMessage{
		{Role: "user", Text: "first"},
		{Role: "assistant", Text: "answer"},
		{Role: "user", Text: "next"},
	})
	concurrentPlan.commit()

	store.mu.Lock()
	concurrentState := cloneQoderConversationState(store.items[key])
	store.mu.Unlock()
	if concurrentState == nil || concurrentState.version != 3 {
		t.Fatalf("expected concurrent version=3, got=%#v", concurrentState)
	}

	plan.rollbackAccepted()

	store.mu.Lock()
	final := cloneQoderConversationState(store.items[key])
	store.mu.Unlock()
	if !qoderConversationStateEqual(final, concurrentState) {
		t.Fatalf("rollback clobbered concurrent commit\nconcurrent=%#v\nfinal=%#v", concurrentState, final)
	}
}

// TestQoderTokenProviderInvalidateRace 验证 GetSession 和 Invalidate 的竞态安全
func TestQoderTokenProviderInvalidateRace(t *testing.T) {
	provider := &QoderTokenProvider{
		sessions: make(map[int64]qoderSessionCacheEntry),
	}

	account := &Account{
		ID:       999,
		Platform: PlatformQoder,
		Type:     AccountTypeCosy,
		Credentials: map[string]any{
			"security_oauth_token": "token_999",
			"machine_id":           "machine_999",
			"aid":                  "aid_999",
		},
	}

	ctx := context.Background()
	const numIterations = 1000

	// 启动两个 goroutine：一个不断 GetSession，一个不断 Invalidate
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		for i := 0; i < numIterations; i++ {
			_, _ = provider.GetSession(ctx, account)
		}
	}()

	go func() {
		defer wg.Done()
		for i := 0; i < numIterations; i++ {
			provider.Invalidate(account.ID)
		}
	}()

	wg.Wait()

	// 不应该 panic 或死锁
	t.Log("GetSession/Invalidate race test completed without panic")
}
