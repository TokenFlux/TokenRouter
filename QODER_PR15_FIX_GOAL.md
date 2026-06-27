# Qoder PR #15 Critical Fixes - Goal Prompt

## Context

PR #15 (feat/qoder-native-platform) adds native Qoder platform support to TokenRouter. A comprehensive code review by 10 parallel agents (483K tokens) identified critical concurrency safety, resource management, and architectural issues that must be fixed before merge.

**Current state:**
- Branch: `feat/qoder-native-platform`
- Base: `origin/main` (ffc7a718, 9 commits ahead)
- Last commit: b4324ef2 "fix(qoder): address PR #15 review issues"
- Files modified in last commit: 7 files (+216/-7 lines)

## Critical Issues to Fix (P0-P1)

### 1. [P0] Data Race in qoderConversationStore() Singleton

**File**: `backend/internal/service/qoder_gateway_service.go:408-417`

**Problem**: 
```go
func (s *QoderGatewayService) qoderConversationStore() *qoderConversationStore {
    if s == nil {  // ❌ Check happens BEFORE acquiring lock
        return nil
    }
    s.mu.Lock()
    defer s.mu.Unlock()
    if s.conversations == nil {
        s.conversations = &qoderConversationStore{items: make(map[string]*qoderConversationEntry)}
    }
    return s.conversations
}
```

Multiple goroutines can pass the `s == nil` check before any acquire the lock, then each create a separate store inside the lock, breaking singleton semantics.

**Fix**: Move the nil receiver check after acquiring the lock, or use atomic operations / sync.Once.

---

### 2. [P1] Resource Leak in WriteQoderOpenAIStreamResponse / WriteQoderAnthropicStreamResponse

**File**: `backend/internal/service/qoder_gateway_service.go:3182-3183, 3357-3358`

**Problem**: 
`closeQoderResponse(resp)` is only called on specific error paths but NOT on successful completion. The deferred `resp.Body.Close()` is in `streamQoderEvents` (line 4038), but if that function is never reached due to early errors, `resp.Body` leaks.

**Fix**: Add `defer closeQoderResponse(resp)` immediately after `openQoderStream()` succeeds in both functions, or ensure all return paths call it.

---

### 3. [P1] Race Condition in QoderTokenProvider.GetSession()

**File**: `backend/internal/service/qoder_token_provider.go:62-71`

**Problem**:
```go
func (p *QoderTokenProvider) GetSession(...) (*qoder.SessionContext, error) {
    // ...
    p.mu.Lock()
    cached, ok := p.sessions[account.ID]
    p.mu.Unlock()  // ❌ Lock released here
    
    if ok && cached.credentialsHash == hash {
        return cached.session, nil  // ❌ Another goroutine could invalidate between unlock and return
    }
    // ...
}
```

After releasing the lock at line 69, another goroutine could call `Invalidate()` and delete the session before it's returned, causing stale/invalid session to be used.

**Fix**: Copy the session pointer while holding the lock, or restructure to return immediately without releasing lock early.

---

### 4. [P1] Lock Release Error Silently Ignored

**File**: `backend/internal/service/oauth_refresh_api.go:103-105`

**Problem**:
```go
defer func() {
    if lockAcquired {
        _ = r.tokenCache.ReleaseRefreshLock(lockKey)  // ❌ Error discarded
    }
}()
```

If `ReleaseRefreshLock` fails (e.g., Redis connection lost), the lock remains held until TTL expiration, blocking other workers.

**Fix**: Log the error with ALERT level or add metrics to track lock release failures.

---

### 5. [P1] Scanner Buffer Overflow Silent Failure

**File**: `backend/internal/service/qoder_gateway_service.go:4006-4028`

**Problem**:
```go
func scanQoderEvents(ctx context.Context, ...) {
    scanner := bufio.NewScanner(body)
    scanner.Buffer(make([]byte, 0, 64*1024), defaultMaxLineSize)  // 64KB max
    
    for scanner.Scan() {
        // ...
    }
    
    if err := scanner.Err(); err != nil {
        events <- qoderStreamEvent{err: err}  // ❌ bufio.ErrTooLong not distinguished
    }
}
```

If an SSE event exceeds `defaultMaxLineSize`, `scanner.Err()` returns `bufio.ErrTooLong`, but this is sent via channel without distinguishing it. Large tool call events trigger silent failures.

**Fix**: Check for `errors.Is(err, bufio.ErrTooLong)` and handle explicitly with a clear error message, or increase the buffer size and document the limit.

---

### 6. [P1] Rollback Race Condition

**File**: `backend/internal/service/qoder_gateway_service.go:928-947`

**Problem**:
```go
func (p *qoderConversationPlan) rollbackAccepted() error {
    // p.previousState was set during plan creation
    // Another goroutine could have modified the store entry between plan creation and rollback
    
    s.mu.Lock()
    defer s.mu.Unlock()
    
    existing := s.items[p.conversationKey]
    // Checks at lines 937-940 attempt to detect this, but insufficient
    if existing.sessionFingerprint == p.previousState.sessionFingerprint &&
       existing.systemFingerprint == p.previousState.systemFingerprint &&
       existing.toolsFingerprint == p.previousState.toolsFingerprint {
        s.items[p.conversationKey] = p.previousState  // ❌ Could clobber valid state
    }
    // ...
}
```

If session/system/tools match but messages diverged, rollback clobbers valid state.

**Fix**: Add version numbers or timestamps to detect stale rollbacks. Consider using copy-on-write or atomic pointer swap.

---

## Medium Priority Issues (P2)

### 7. [P2] Rollback Error Handling

**File**: `backend/internal/service/qoder_gateway_service.go:154, 171, 181, 189`

**Problem**: When stream errors occur, `conversationPlan.rollbackAccepted()` is called but any error it returns is silently ignored.

**Fix**: Log rollback failures with WARN level or return composite error.

---

### 8. [P2] Cache Update Skipped on Transient Errors

**File**: `backend/internal/service/qoder_token_provider.go:93-137`

**Problem**: Multiple early returns in `buildSession()` skip cache update. Caller stores nil in cache (line 79), so transient errors (network timeout) cache nil permanently until credentials change.

**Fix**: Return without caching on transient errors, or use sentinel error types to distinguish.

---

### 9. [P2] DB Reread Failure Silent Fallback

**File**: `backend/internal/service/oauth_refresh_api.go:108-119`

**Problem**: DB reread failure logs warning but silently falls back to stale account. If significantly stale (refresh_token rotated), causes invalid_grant.

**Fix**: Return error or increment degradation metric.

---

### 10. [P2] API Call Modifies Identity Without Synchronization

**File**: `backend/internal/service/qoder_token_provider.go:145-166`

**Problem**: `populateOrganizationFromAPI()` modifies identity after being returned from cache. Unsafe if used concurrently.

**Fix**: Make identity immutable after caching, or call before caching.

---

### 11. [P2] Credentials Mutation Risk

**File**: `backend/internal/service/oauth_refresh_api.go:148-150`

**Problem**: Direct mutation of `newCredentials` map returned by `executor.Refresh()`. If executor returns shared map, mutates internal state.

**Fix**: Clone `newCredentials` before adding `_token_version`, or document contract.

---

## Test Coverage Gaps

### High Priority
1. Concurrent conversation access / race conditions (beyond single refresh race test)
2. Context cancellation during SSE streaming
3. Malformed upstream responses (non-JSON SSE, incomplete tool call events)
4. `qoder_oauth_handler_test.go`: Only 2 tests, missing Poll endpoint entirely

### Medium Priority
5. Empty/nil tool arrays in payload builders
6. Token refresh failure scenarios (beyond invalid_grant)
7. Conversation store expiration edge cases
8. Model alias resolution edge cases
9. Tool name normalization edge cases

---

## Git Workflow Requirements

### Branch Management
1. **Current branch**: `feat/qoder-native-platform` at b4324ef2
2. **Needs rebase**: origin/main is 9 commits ahead (ffc7a718)
3. **Commit strategy**:
   - Create logical commits per fix (P0-P1 issues)
   - Group P2 fixes into thematic commits
   - Keep test additions separate from implementation fixes

### Commit Guidelines
```bash
# Format: <type>(<scope>): <subject>
# Types: fix, refactor, test, chore

# P0-P1 fixes (separate commits):
fix(qoder): resolve conversation store singleton race condition
fix(qoder): prevent resource leak in stream response writers
fix(qoder): fix token provider cache race condition
fix(qoder): log lock release errors for debugging
fix(qoder): handle SSE scanner buffer overflow
fix(qoder): add versioning to conversation rollback

# P2 fixes (can group):
refactor(qoder): improve error handling in OAuth refresh
refactor(qoder): defensive checks and logging

# Tests (separate):
test(qoder): add concurrent access and edge case tests

# Final:
Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>
```

---

## Implementation Plan

### Phase 1: Critical Fixes (P0-P1)
**Goal**: Fix data races, resource leaks, silent failures

1. Fix conversation store singleton race (use sync.Once or restructure locking)
2. Fix resource leak (add defer closeQoderResponse immediately after openQoderStream)
3. Fix token provider cache race (copy session under lock)
4. Log lock release errors (add logger.LegacyPrintf ALERT)
5. Handle scanner buffer overflow (check bufio.ErrTooLong, return clear error)
6. Add versioning to conversation rollback (version field in qoderConversationEntry)

**Testing**: Run existing tests after each fix to ensure no regressions.

### Phase 2: Medium Priority Fixes (P2)
**Goal**: Improve error handling, defensive checks

7. Log rollback errors
8. Skip cache on transient buildSession errors
9. Return error on DB reread failure (or add metric)
10. Document identity immutability / call populateOrganization before caching
11. Clone credentials before mutation

**Testing**: Add targeted tests for error paths.

### Phase 3: Test Coverage
**Goal**: Fill critical gaps

12. Add concurrent conversation access tests
13. Add context cancellation during SSE streaming tests
14. Expand qoder_oauth_handler_test.go (Poll endpoint, error paths)
15. Add malformed upstream response tests

### Phase 4: Rebase and Merge Prep
**Goal**: Clean history, resolve conflicts

16. Interactive rebase to clean up commit history
17. Rebase onto latest origin/main (ffc7a718)
18. Resolve merge conflicts if any
19. Run full test suite
20. Update PR description with fixes

---

## Success Criteria

### Must Have (Blocking)
- ✅ All P0-P1 issues fixed and tested
- ✅ No new test failures introduced
- ✅ All existing Qoder tests pass
- ✅ Clean rebase onto origin/main
- ✅ Logical commit history

### Should Have
- ✅ P2 issues fixed or documented as acceptable
- ✅ Critical test coverage gaps filled
- ✅ Code review findings documented in commit messages

### Nice to Have
- ✅ All test coverage gaps filled
- ✅ Performance benchmarks for concurrent scenarios
- ✅ Documentation updates

---

## Validation Commands

```bash
# Run all Qoder tests
cd backend
go test ./internal/pkg/qoder ./internal/service ./internal/handler -run 'Qoder|qoder' -count=1 -race

# Run with race detector
go test -race ./internal/service -run TestQoderGateway

# Check for common issues
go vet ./internal/service/...

# Format check
gofmt -d backend/internal/service/qoder*.go

# Lint (if available)
golangci-lint run ./internal/service/...

# Git status
git status --short
git log --oneline -10
```

---

## Notes for Implementation

1. **Preserve existing fixes**: The last commit (b4324ef2) already fixed:
   - LockHeld waiting logic
   - NeedsRefresh checking
   - max_completion_tokens support
   - developer message preservation
   - Responses API developer/system preservation
   - Frontend model mapping compatibility
   - Migration renaming (168→169)

2. **Focus on new issues**: This goal addresses issues found in the workflow review that are separate from the already-fixed PR review issues.

3. **Test incrementally**: After each fix, run `go test ./internal/service -run TestQoder -race` to catch new races.

4. **Document tradeoffs**: If a fix has performance implications (e.g., holding lock longer), document in commit message.

5. **Maintain backward compatibility**: All fixes should preserve existing API contracts and behavior.

---

## Related Files

**Core files to modify:**
- `backend/internal/service/qoder_gateway_service.go` (4500+ lines)
- `backend/internal/service/qoder_token_provider.go`
- `backend/internal/service/qoder_token_refresher.go`
- `backend/internal/service/oauth_refresh_api.go`

**Test files to expand:**
- `backend/internal/service/qoder_gateway_service_test.go`
- `backend/internal/handler/admin/qoder_oauth_handler_test.go`

**Files to review but likely not modify:**
- `frontend/src/composables/useModelWhitelist.ts` (already fixed)
- Migration files (already renamed)

---

## Estimated Scope

- **LOC to modify**: ~200-300 lines (mostly adding checks, logging, restructuring)
- **New test code**: ~500-800 lines (concurrent tests, edge cases)
- **Commits**: 8-12 logical commits
- **Time estimate**: 4-6 hours for experienced developer
- **Risk**: Medium (touching critical concurrency code, needs careful review)

---

## Questions to Resolve

1. **Conversation store versioning**: Use timestamp, counter, or UUID for version field?
2. **Scanner buffer size**: Increase to 256KB or 1MB, or document current limit?
3. **Lock release failure handling**: Just log, or also emit metrics/alerts?
4. **Transient error detection**: Which errors should skip cache? Network timeouts only or broader set?
5. **Rebase strategy**: Interactive rebase to squash/reorder, or preserve all commits?

---

## Definition of Done

- [ ] All P0-P1 issues resolved with tests
- [ ] P2 issues resolved or documented as acceptable
- [ ] All tests pass with `-race` flag
- [ ] No new lint/vet warnings
- [ ] Clean rebase onto origin/main
- [ ] Commit messages follow conventional format
- [ ] PR description updated with fix summary
- [ ] Ready for re-review by maintainer
