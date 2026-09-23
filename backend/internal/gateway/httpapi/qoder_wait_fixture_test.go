package httpapi

import (
	"context"
	"sync"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// 此文件只有既有并发存储端口的测试计数，不实现等待、计费或租约规则。
type helperConcurrencyCacheStub struct {
	mu sync.Mutex

	accountSeq []bool
	userSeq    []bool

	accountAcquireCalls int
	userAcquireCalls    int
	accountReleaseCalls int
	userReleaseCalls    int
	waitAllowed         bool
	waitIncrementCalls  int
	waitDecrementCalls  int
	waitMaxWait         int
	waitIncrementHook   func()
	apiKeyTrackCalls    int
	apiKeyReleaseCalls  int
	apiKeyTrackIDs      []int64
}

func (s *helperConcurrencyCacheStub) AcquireAccountSlot(ctx context.Context, accountID int64, maxConcurrency int, requestID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accountAcquireCalls++
	if len(s.accountSeq) == 0 {
		return false, nil
	}
	v := s.accountSeq[0]
	s.accountSeq = s.accountSeq[1:]
	return v, nil
}

func (s *helperConcurrencyCacheStub) ReleaseAccountSlot(ctx context.Context, accountID int64, requestID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.accountReleaseCalls++
	return nil
}

func (s *helperConcurrencyCacheStub) GetAccountConcurrency(ctx context.Context, accountID int64) (int, error) {
	return 0, nil
}

func (s *helperConcurrencyCacheStub) GetAccountConcurrencyBatch(ctx context.Context, accountIDs []int64) (map[int64]int, error) {
	out := make(map[int64]int, len(accountIDs))
	for _, accountID := range accountIDs {
		out[accountID] = 0
	}
	return out, nil
}

func (s *helperConcurrencyCacheStub) IncrementAccountWaitCount(ctx context.Context, accountID int64, maxWait int) (bool, error) {
	return true, nil
}

func (s *helperConcurrencyCacheStub) DecrementAccountWaitCount(ctx context.Context, accountID int64) error {
	return nil
}

func (s *helperConcurrencyCacheStub) GetAccountWaitingCount(ctx context.Context, accountID int64) (int, error) {
	return 0, nil
}

func (s *helperConcurrencyCacheStub) AcquireUserSlot(ctx context.Context, userID int64, maxConcurrency int, requestID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.userAcquireCalls++
	if len(s.userSeq) == 0 {
		return false, nil
	}
	v := s.userSeq[0]
	s.userSeq = s.userSeq[1:]
	return v, nil
}

func (s *helperConcurrencyCacheStub) ReleaseUserSlot(ctx context.Context, userID int64, requestID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.userReleaseCalls++
	return nil
}

func (s *helperConcurrencyCacheStub) GetUserConcurrency(ctx context.Context, userID int64) (int, error) {
	return 0, nil
}

func (s *helperConcurrencyCacheStub) TrackAPIKeySlot(ctx context.Context, apiKeyID int64, requestID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.apiKeyTrackCalls++
	s.apiKeyTrackIDs = append(s.apiKeyTrackIDs, apiKeyID)
	return nil
}

func (s *helperConcurrencyCacheStub) ReleaseAPIKeySlot(ctx context.Context, apiKeyID int64, requestID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.apiKeyReleaseCalls++
	return nil
}

func (s *helperConcurrencyCacheStub) GetAPIKeyConcurrencyBatch(ctx context.Context, apiKeyIDs []int64) (map[int64]int, error) {
	out := make(map[int64]int, len(apiKeyIDs))
	for _, apiKeyID := range apiKeyIDs {
		out[apiKeyID] = 0
	}
	return out, nil
}

func (s *helperConcurrencyCacheStub) IncrementWaitCount(ctx context.Context, userID int64, maxWait int) (bool, error) {
	s.mu.Lock()
	s.waitIncrementCalls++
	s.waitMaxWait = maxWait
	waitAllowed := s.waitAllowed
	hook := s.waitIncrementHook
	s.mu.Unlock()

	if hook != nil {
		hook()
	}
	if !waitAllowed {
		return false, nil
	}
	return true, nil
}

func (s *helperConcurrencyCacheStub) DecrementWaitCount(ctx context.Context, userID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.waitDecrementCalls++
	return nil
}

func (s *helperConcurrencyCacheStub) GetAccountsLoadBatch(ctx context.Context, accounts []scheduler.AccountWithConcurrency) (map[int64]*scheduler.AccountLoadInfo, error) {
	out := make(map[int64]*scheduler.AccountLoadInfo, len(accounts))
	for _, acc := range accounts {
		out[acc.ID] = &scheduler.AccountLoadInfo{AccountID: acc.ID}
	}
	return out, nil
}

func (s *helperConcurrencyCacheStub) GetUsersLoadBatch(ctx context.Context, users []scheduler.UserWithConcurrency) (map[int64]*scheduler.UserLoadInfo, error) {
	out := make(map[int64]*scheduler.UserLoadInfo, len(users))
	for _, user := range users {
		out[user.ID] = &scheduler.UserLoadInfo{UserID: user.ID}
	}
	return out, nil
}

func (s *helperConcurrencyCacheStub) CleanupExpiredAccountSlots(ctx context.Context, accountID int64) error {
	return nil
}

func (s *helperConcurrencyCacheStub) CleanupExpiredAccountSlotKeys(ctx context.Context) error {
	return nil
}

func (s *helperConcurrencyCacheStub) CleanupStaleProcessSlots(ctx context.Context, activeRequestPrefix string) error {
	return nil
}
