package provider

import (
	"context"
	"errors"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
)

// QoderRequestRefresh 复用唯一账号刷新协调器及会话缓存，拥有请求失败后的回读和失效顺序。
// 它不启动后台任务，也不创建额外锁或缓存。
type QoderRequestRefresh struct {
	Store        account.RefreshRepository
	Tokens       *QoderTokenProvider
	Coordinator  *account.OAuthRefreshAPI
	NewRefresher func() *QoderTokenRefresher
	Transport    QoderTransport
	Profiles     *egressprovider.TLSProfiles
}

func (s *QoderRequestRefresh) RefreshAccountSession(ctx context.Context, value *account.Record) (*account.Record, error) {
	if s == nil {
		return nil, errors.New("qoder gateway service is not configured")
	}
	if value == nil {
		return nil, errors.New("account is nil")
	}
	if s.Store == nil {
		return nil, errors.New("qoder account repository is not configured")
	}
	refresherFactory := s.NewRefresher
	if refresherFactory == nil {
		refresherFactory = func() *QoderTokenRefresher {
			return NewQoderTokenRefresher(QoderRefreshOptions{Transport: s.Transport, Profiles: s.Profiles})
		}
	}
	refresher := refresherFactory()
	if refresher == nil {
		return nil, errors.New("qoder token refresher is nil")
	}
	refreshAPI := s.Coordinator
	if refreshAPI == nil {
		return nil, errors.New("qoder refresh API is not configured")
	}
	failedCredentialsHash := account.QoderRefreshCredentialsHash(value.Credentials)
	executor := qoderFailedRefreshExecutor{
		QoderTokenRefresher: refresher,
		failedCredentials:   failedCredentialsHash,
	}
	result, err := refreshAPI.RefreshIfNeeded(ctx, value, executor, 15*time.Minute)
	if err != nil {
		return nil, err
	}

	// 如果另一个 worker 正在刷新（LockHeld=true），等待 DB 中出现已轮换凭证。
	// 不能只 sleep 后返回当前账号：锁持有者可能尚未写回新 token，
	// handler 随后会用同一份 stale credentials 立即重试并再次 401。
	if result != nil && result.LockHeld {
		return s.waitForQoderLockedRefresh(ctx, value, failedCredentialsHash)
	}

	if result != nil && result.Account != nil {
		if s.Tokens != nil {
			s.Tokens.InvalidateAccount(result.Account)
		}
		return result.Account, nil
	}
	if s.Store != nil {
		if fresh, err := s.Store.GetByID(ctx, value.ID); err == nil && fresh != nil {
			if s.Tokens != nil {
				s.Tokens.InvalidateAccount(fresh)
			}
			return fresh, nil
		}
	}
	if s.Tokens != nil && (result == nil || result.Refreshed) {
		s.Tokens.Invalidate(value.ID)
	}
	return value, nil
}

func (s *QoderRequestRefresh) waitForQoderLockedRefresh(ctx context.Context, value *account.Record, failedCredentialsHash string) (*account.Record, error) {
	if s == nil || s.Store == nil || value == nil {
		return nil, account.ErrQoderRefreshInProgress
	}
	var result *account.Record
	err := account.WaitForQoderRefresh(ctx, func(readCtx context.Context) (bool, error) {
		fresh, err := s.Store.GetByID(readCtx, value.ID)
		if err != nil {
			return false, err
		}
		if fresh == nil {
			return false, nil
		}
		if account.QoderRefreshCredentialsHash(fresh.Credentials) != failedCredentialsHash {
			if s.Tokens != nil {
				s.Tokens.InvalidateAccount(fresh)
			}
			result = fresh
			return true, nil
		}
		return false, nil
	})
	return result, err
}

type qoderFailedRefreshExecutor struct {
	*QoderTokenRefresher
	failedCredentials string
}

func (e qoderFailedRefreshExecutor) NeedsRefresh(value *account.Record, ttl time.Duration) bool {
	if e.QoderTokenRefresher == nil {
		return false
	}
	return account.NeedsRefreshQoderAfterFailure(value, e.failedCredentials, ttl)
}
