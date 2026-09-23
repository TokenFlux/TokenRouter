// 并发测试替身仅供接口合同使用，不接入生产装配。
package testkit

import (
	"context"
	"sync"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

type ConcurrencySequence struct {
	Mu sync.Mutex

	AccountSeq []bool
	UserSeq    []bool

	AccountAcquireCalls int
	UserAcquireCalls    int
	AccountReleaseCalls int
	UserReleaseCalls    int
	WaitAllowed         bool
	WaitIncrementCalls  int
	WaitDecrementCalls  int
	WaitMaxWait         int
	WaitIncrementHook   func()
	APIKeyTrackCalls    int
	APIKeyReleaseCalls  int
	APIKeyTrackIDs      []int64
}

func (s *ConcurrencySequence) AcquireAccountSlot(ctx context.Context, accountID int64, maxConcurrency int, requestID string) (bool, error) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.AccountAcquireCalls++
	if len(s.AccountSeq) == 0 {
		return false, nil
	}
	v := s.AccountSeq[0]
	s.AccountSeq = s.AccountSeq[1:]
	return v, nil
}

func (s *ConcurrencySequence) ReleaseAccountSlot(ctx context.Context, accountID int64, requestID string) error {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.AccountReleaseCalls++
	return nil
}

func (s *ConcurrencySequence) GetAccountConcurrency(ctx context.Context, accountID int64) (int, error) {
	return 0, nil
}

func (s *ConcurrencySequence) GetAccountConcurrencyBatch(ctx context.Context, accountIDs []int64) (map[int64]int, error) {
	out := make(map[int64]int, len(accountIDs))
	for _, accountID := range accountIDs {
		out[accountID] = 0
	}
	return out, nil
}

func (s *ConcurrencySequence) IncrementAccountWaitCount(ctx context.Context, accountID int64, maxWait int) (bool, error) {
	return true, nil
}

func (s *ConcurrencySequence) DecrementAccountWaitCount(ctx context.Context, accountID int64) error {
	return nil
}

func (s *ConcurrencySequence) GetAccountWaitingCount(ctx context.Context, accountID int64) (int, error) {
	return 0, nil
}

func (s *ConcurrencySequence) AcquireUserSlot(ctx context.Context, userID int64, maxConcurrency int, requestID string) (bool, error) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.UserAcquireCalls++
	if len(s.UserSeq) == 0 {
		return false, nil
	}
	v := s.UserSeq[0]
	s.UserSeq = s.UserSeq[1:]
	return v, nil
}

func (s *ConcurrencySequence) ReleaseUserSlot(ctx context.Context, userID int64, requestID string) error {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.UserReleaseCalls++
	return nil
}

func (s *ConcurrencySequence) GetUserConcurrency(ctx context.Context, userID int64) (int, error) {
	return 0, nil
}

func (s *ConcurrencySequence) TrackAPIKeySlot(ctx context.Context, apiKeyID int64, requestID string) error {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.APIKeyTrackCalls++
	s.APIKeyTrackIDs = append(s.APIKeyTrackIDs, apiKeyID)
	return nil
}

func (s *ConcurrencySequence) ReleaseAPIKeySlot(ctx context.Context, apiKeyID int64, requestID string) error {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.APIKeyReleaseCalls++
	return nil
}

func (s *ConcurrencySequence) GetAPIKeyConcurrencyBatch(ctx context.Context, apiKeyIDs []int64) (map[int64]int, error) {
	out := make(map[int64]int, len(apiKeyIDs))
	for _, apiKeyID := range apiKeyIDs {
		out[apiKeyID] = 0
	}
	return out, nil
}

func (s *ConcurrencySequence) IncrementWaitCount(ctx context.Context, userID int64, maxWait int) (bool, error) {
	s.Mu.Lock()
	s.WaitIncrementCalls++
	s.WaitMaxWait = maxWait
	waitAllowed := s.WaitAllowed
	hook := s.WaitIncrementHook
	s.Mu.Unlock()

	if hook != nil {
		hook()
	}
	if !waitAllowed {
		return false, nil
	}
	return true, nil
}

func (s *ConcurrencySequence) DecrementWaitCount(ctx context.Context, userID int64) error {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	s.WaitDecrementCalls++
	return nil
}

func (s *ConcurrencySequence) GetAccountsLoadBatch(ctx context.Context, accounts []scheduler.AccountWithConcurrency) (map[int64]*scheduler.AccountLoadInfo, error) {
	out := make(map[int64]*scheduler.AccountLoadInfo, len(accounts))
	for _, acc := range accounts {
		out[acc.ID] = &scheduler.AccountLoadInfo{AccountID: acc.ID}
	}
	return out, nil
}

func (s *ConcurrencySequence) GetUsersLoadBatch(ctx context.Context, users []scheduler.UserWithConcurrency) (map[int64]*scheduler.UserLoadInfo, error) {
	out := make(map[int64]*scheduler.UserLoadInfo, len(users))
	for _, user := range users {
		out[user.ID] = &scheduler.UserLoadInfo{UserID: user.ID}
	}
	return out, nil
}

func (s *ConcurrencySequence) CleanupExpiredAccountSlots(ctx context.Context, accountID int64) error {
	return nil
}

func (s *ConcurrencySequence) CleanupExpiredAccountSlotKeys(ctx context.Context) error {
	return nil
}

func (s *ConcurrencySequence) CleanupStaleProcessSlots(ctx context.Context, activeRequestPrefix string) error {
	return nil
}

type ConcurrencySequenceWithError struct {
	ConcurrencySequence
	Err error
}

func (s *ConcurrencySequenceWithError) AcquireAccountSlot(ctx context.Context, accountID int64, maxConcurrency int, requestID string) (bool, error) {
	return false, s.Err
}
