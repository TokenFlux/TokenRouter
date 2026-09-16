package service

import (
	"context"
	"errors"
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
				store.Mu.Lock()
				currentState := store.Items[key]
				store.Mu.Unlock()

				// 创建新 plan，设置 previousState
				plan := &qoderConversationPlan{
					Store:             store,
					Key:               key,
					SessionID:         sessionID,
					SystemFingerprint: "system_v1",
					ToolsFingerprint:  "tools_v1",
					PreviousState:     cloneQoderConversationState(currentState),
				}

				// 提交新的 fingerprints
				fingerprints := []string{"msg_1", "msg_2", "msg_3"}
				plan.CommitFingerprints(fingerprints)

				// 模拟偶尔的 rollback 场景
				if workerID%2 == 0 && j%10 == 0 {
					plan.AcceptedCommitted = true
					plan.RollbackAccepted()
				}
			}
		}(i)
	}

	wg.Wait()

	// 验证最终状态一致性
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

// TestQoderTokenProviderConcurrent 验证 token provider 在并发访问下的缓存行为
func TestQoderTokenProviderConcurrent(t *testing.T) {
	provider := &QoderTokenProvider{Core: &qoderSessionState{Sessions: make(map[int64]qoderSessionCacheEntry)}}

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
					// 与显式 Invalidate 竞争的旧世代构建必须被丢弃，这是预期的安全结果。
					if errors.Is(err, errQoderSessionBuildInvalidated) {
						successCount.Store(workerID*1000+j, true)
						continue
					}
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

	// 保存 previousState 用于后续 rollback
	plan1.PreviousState = cloneQoderConversationState(state1)
	plan1.AcceptedState = cloneQoderConversationState(state1)
	plan1.AcceptedCommitted = true

	// 另一个 plan 提交新状态：version 2
	plan2 := &qoderConversationPlan{
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

	// plan1 尝试 rollback（基于 version 1 的 previousState）
	// 应该被拒绝，因为当前 version 已经是 2
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

func TestQoderConversationRollbackAcceptedDeletesOwnNewState(t *testing.T) {
	store := newQoderConversationStore(5 * time.Minute)
	plan := store.Plan(
		"rollback_new_state",
		"system",
		nil,
		[]qoderMessage{{Role: "user", Text: "hello"}},
	)

	plan.CommitAccepted()

	store.Mu.Lock()
	accepted := cloneQoderConversationState(store.Items[plan.Key])
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

func TestQoderConversationRollbackAcceptedRestoresPreviousState(t *testing.T) {
	store := newQoderConversationStore(5 * time.Minute)
	key := "rollback_previous_state"
	system := "system"
	firstMessages := []qoderMessage{{Role: "user", Text: "first"}}
	initialPlan := store.Plan(key, system, nil, firstMessages)
	initialPlan.Commit(ClaudeUsage{InputTokens: 10, OutputTokens: 2})

	store.Mu.Lock()
	previous := cloneQoderConversationState(store.Items[key])
	store.Mu.Unlock()
	if previous == nil || previous.Version != 1 || !previous.HasUsage {
		t.Fatalf("unexpected previous state: %#v", previous)
	}

	nextMessages := []qoderMessage{
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
	accepted := cloneQoderConversationState(store.Items[key])
	store.Mu.Unlock()
	if accepted == nil || accepted.Version != 2 {
		t.Fatalf("expected accepted version=2, got=%#v", accepted)
	}

	plan.RollbackAccepted()

	store.Mu.Lock()
	final := cloneQoderConversationState(store.Items[key])
	store.Mu.Unlock()
	if !qoderConversationStateEqual(final, previous) {
		t.Fatalf("expected rollback to restore previous state\nprevious=%#v\nfinal=%#v", previous, final)
	}
}

func TestQoderConversationRollbackAcceptedDoesNotClobberConcurrentCommit(t *testing.T) {
	store := newQoderConversationStore(5 * time.Minute)
	key := "rollback_concurrent_commit"
	system := "system"
	firstMessages := []qoderMessage{{Role: "user", Text: "first"}}
	initialPlan := store.Plan(key, system, nil, firstMessages)
	initialPlan.Commit()

	plan := store.Plan(key, system, nil, []qoderMessage{
		{Role: "user", Text: "first"},
		{Role: "assistant", Text: "answer"},
		{Role: "user", Text: "next"},
	})
	plan.CommitAccepted()

	concurrentPlan := store.Plan(key, system, nil, []qoderMessage{
		{Role: "user", Text: "first"},
		{Role: "assistant", Text: "answer"},
		{Role: "user", Text: "next"},
	})
	concurrentPlan.Commit()

	store.Mu.Lock()
	concurrentState := cloneQoderConversationState(store.Items[key])
	store.Mu.Unlock()
	if concurrentState == nil || concurrentState.Version != 3 {
		t.Fatalf("expected concurrent version=3, got=%#v", concurrentState)
	}

	plan.RollbackAccepted()

	store.Mu.Lock()
	final := cloneQoderConversationState(store.Items[key])
	store.Mu.Unlock()
	if !qoderConversationStateEqual(final, concurrentState) {
		t.Fatalf("rollback clobbered concurrent commit\nconcurrent=%#v\nfinal=%#v", concurrentState, final)
	}
}

// TestQoderTokenProviderInvalidateRace 验证 GetSession 和 Invalidate 的竞态安全
func TestQoderTokenProviderInvalidateRace(t *testing.T) {
	provider := &QoderTokenProvider{Core: &qoderSessionState{Sessions: make(map[int64]qoderSessionCacheEntry)}}

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
