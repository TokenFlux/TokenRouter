package live

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
)

const (
	liveLeaseRefreshInterval    = 20 * time.Second
	liveRedisOperationTimeout   = 3 * time.Second
	liveClosedRecordTTL         = 24 * time.Hour
	liveObserverPollInterval    = 250 * time.Millisecond
	liveObserverStoreRetryLimit = 5
)

// ProxyLiveSideband 让认证后的客户端接管控制连接；媒体始终不经过这里。
func (s *Service) ProxyLiveSideband(
	ctx context.Context,
	record *session.LiveCallRecord,
	downstream FrameConn,
) error {
	if record == nil || downstream == nil {
		return session.ErrLiveCallNotFound
	}
	store, err := s.ports.Store()
	if err != nil {
		return err
	}
	owner := uuid.NewString()
	claimed, err := store.ClaimLiveController(ctx, record.CallHash, session.LiveControllerProxy, owner)
	if err != nil {
		return err
	}
	if !claimed {
		return session.ErrLiveControllerChanged
	}

	// 归还控制权使用独立预算；请求取消不能阻止原会话恢复观察。
	releaseController := func() {
		cleanup, done := context.WithTimeout(context.Background(), liveRedisOperationTimeout)
		defer done()
		_, _ = store.ReleaseLiveController(cleanup, record.CallHash, owner)
	}
	// observer 轮询到接管状态后会关闭旧控制连接；取消不继续查询或拨号。
	if !waitLiveObserver(ctx, liveObserverPollInterval) || ctx.Err() != nil {
		releaseController()
		go s.Observe(record)
		return context.Cause(ctx)
	}
	account, err := s.ports.Target(ctx, record)
	if err != nil {
		releaseController()
		go s.Observe(record)
		return err
	}
	if err := ctx.Err(); err != nil {
		releaseController()
		go s.Observe(record)
		return context.Cause(ctx)
	}
	upstream, err := account.Dial(ctx)
	if err != nil {
		releaseController()
		go s.Observe(record)
		return err
	}
	defer func() { _ = upstream.Close() }()
	downstream.SetReadLimit(s.readLimit)

	proxyCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	errCh := make(chan error, 2)
	modelState := newLiveSidebandModelState(record)
	go func() {
		for {
			messageType, payload, readErr := downstream.ReadFrame(proxyCtx)
			if readErr != nil {
				errCh <- readErr
				return
			}
			if messageType == TextFrame {
				rewritten, clientModel, internalModels, rewriteErr := account.Rewrite(proxyCtx, payload)
				if rewriteErr != nil {
					errCh <- rewriteErr
					return
				}
				payload = rewritten
				if clientModel != "" {
					modelState.update(clientModel, internalModels...)
				}
			}
			if writeErr := upstream.WriteFrame(proxyCtx, messageType, payload); writeErr != nil {
				errCh <- writeErr
				return
			}
		}
	}()
	go func() {
		for {
			messageType, payload, readErr := upstream.ReadFrame(proxyCtx)
			if readErr != nil {
				errCh <- readErr
				return
			}
			if messageType == TextFrame {
				clientModel, internalModels := modelState.snapshot()
				payload = RestoreServerPayload(payload, clientModel, internalModels)
			}
			if writeErr := downstream.WriteFrame(proxyCtx, messageType, payload); writeErr != nil {
				errCh <- writeErr
				return
			}
			if messageType == TextFrame {
				eventType := strings.TrimSpace(gjson.GetBytes(payload, "type").String())
				if eventType == "session.closed" || eventType == "session.ended" {
					errCh <- session.ErrLiveCallNotFound
					return
				}
			}
		}
	}()

	runErr := s.RunController(proxyCtx, record, upstream, errCh)
	cancel()
	releaseController()
	if SessionEnded(runErr) || !time.Now().Before(record.ExpiresAt) {
		s.Finalize(record)
		return runErr
	}
	go s.Observe(record)
	return runErr
}

// SessionEnded 判断控制连接的退出原因是否意味着会话已终结（应 finalize：写
// usage log 并释放租约），而不是可以交给 observer 重连的临时错误。
//
// session.ErrLiveUnavailable 在控制循环里只会来自租约续租失败。RefreshLiveLease 的 Lua 在
// leaseID 被 GC 后不会重新写入，重连也拿不回并发槽 —— 若按临时错误重试，会话会以
// 约 1 秒一轮的节奏空转到 ExpiresAt，期间持着上游连接却不计入任何并发限制。
func SessionEnded(err error) bool {
	return errors.Is(err, session.ErrLiveCallNotFound) ||
		errors.Is(err, session.ErrLiveUnavailable) ||
		errors.Is(err, context.DeadlineExceeded)
}

func (s *Service) RunController(
	ctx context.Context,
	record *session.LiveCallRecord,
	upstream FrameConn,
	errCh <-chan error,
) error {
	refreshTicker := time.NewTicker(liveLeaseRefreshInterval)
	defer refreshTicker.Stop()
	maxTimer := time.NewTimer(time.Until(record.ExpiresAt))
	defer maxTimer.Stop()
	for {
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case err := <-errCh:
			return err
		case <-maxTimer.C:
			closeCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = upstream.WriteFrame(closeCtx, TextFrame, []byte(`{"type":"session.close"}`))
			cancel()
			return context.DeadlineExceeded
		case <-refreshTicker.C:
			if !s.RefreshLease(record) {
				return session.ErrLiveUnavailable
			}
		}
	}
}

func (s *Service) Observe(record *session.LiveCallRecord) {
	if record == nil {
		return
	}
	owner := uuid.NewString()
	ctx, finish, ok := s.ports.BeginObserver(owner)
	if !ok {
		return
	}
	defer finish()

	store, err := s.ports.Store()
	if err != nil {
		return
	}
	claimed, claimErr := store.ClaimLiveController(ctx, record.CallHash, session.LiveControllerObserver, owner)
	if ctx.Err() != nil {
		return
	}
	if claimErr != nil {
		// 无法确认控制权时保留会话快照，到期后幂等 finalize，避免租约和用量记录静默丢失。
		s.FinalizeAfterExpiry(ctx, record)
		return
	}
	if !claimed {
		return
	}
	storeErrStreak := 0
	for {
		if ctx.Err() != nil {
			return
		}
		latest, getErr := store.GetLiveCall(ctx, record.CallHash)
		if ctx.Err() != nil {
			return
		}
		if getErr != nil {
			if errors.Is(getErr, session.ErrLiveCallNotFound) {
				return
			}
			// Redis 抖动不表示控制权已变化；有限重试后按会话到期时间兜底 finalize。
			storeErrStreak++
			if storeErrStreak >= liveObserverStoreRetryLimit {
				s.FinalizeAfterExpiry(ctx, record)
				return
			}
			if !waitLiveObserver(ctx, s.retryInterval) {
				return
			}
			continue
		}
		storeErrStreak = 0
		record = latest
		if record.Controller != session.LiveControllerObserver {
			return
		}
		if !time.Now().Before(record.ExpiresAt) {
			s.Finalize(record)
			return
		}
		upstream, dialErr := s.dial(ctx, record)
		if ctx.Err() != nil {
			if upstream != nil {
				_ = upstream.Close()
			}
			return
		}
		if dialErr != nil {
			if !s.WaitForObserverRetry(ctx, record) {
				return
			}
			continue
		}
		runErr := s.RunObserverConnection(ctx, record, upstream)
		_ = upstream.Close()
		if ctx.Err() != nil {
			return
		}
		if errors.Is(runErr, session.ErrLiveControllerChanged) {
			return
		}
		if SessionEnded(runErr) {
			s.Finalize(record)
			return
		}
		if !s.WaitForObserverRetry(ctx, record) {
			return
		}
	}
}

func (s *Service) RunObserverConnection(parent context.Context, record *session.LiveCallRecord, upstream FrameConn) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	frameCh := make(chan []byte, 1)
	errCh := make(chan error, 1)
	go func() {
		for {
			messageType, payload, err := upstream.ReadFrame(ctx)
			if err != nil {
				select {
				case errCh <- err:
				case <-ctx.Done():
				}
				return
			}
			if messageType == TextFrame {
				select {
				case frameCh <- payload:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	refreshTicker := time.NewTicker(liveLeaseRefreshInterval)
	defer refreshTicker.Stop()
	controllerTicker := time.NewTicker(liveObserverPollInterval)
	defer controllerTicker.Stop()
	maxTimer := time.NewTimer(time.Until(record.ExpiresAt))
	defer maxTimer.Stop()
	store, _ := s.ports.Store()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case payload := <-frameCh:
			eventType := strings.TrimSpace(gjson.GetBytes(payload, "type").String())
			if eventType == "session.closed" || eventType == "session.ended" {
				return session.ErrLiveCallNotFound
			}
		case err := <-errCh:
			return err
		case <-controllerTicker.C:
			controller, err := store.GetLiveController(context.Background(), record.CallHash)
			if err != nil {
				return err
			}
			if controller != session.LiveControllerObserver {
				return session.ErrLiveControllerChanged
			}
		case <-refreshTicker.C:
			if !s.RefreshLease(record) {
				return session.ErrLiveUnavailable
			}
		case <-maxTimer.C:
			closeCtx, closeCancel := context.WithTimeout(context.Background(), 2*time.Second)
			_ = upstream.WriteFrame(closeCtx, TextFrame, []byte(`{"type":"session.close"}`))
			closeCancel()
			return context.DeadlineExceeded
		}
	}
}

func (s *Service) WaitForObserverRetry(ctx context.Context, record *session.LiveCallRecord) bool {
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
	}
	store, err := s.ports.Store()
	if err != nil {
		return false
	}
	controller, getErr := store.GetLiveController(context.Background(), record.CallHash)
	if getErr != nil && !errors.Is(getErr, session.ErrLiveCallNotFound) {
		// store 故障不等于控制权变化，交回 observer 主循环统一重试和到期兜底。
		return true
	}
	// 过期不在此处判定：返回 true 让调用方回到循环顶部的过期分支，由它 finalize
	// （写 usage log + 释放租约）。在这里直接返回 false 会让会话静默结束、不留记录。
	return getErr == nil && controller == session.LiveControllerObserver
}

// FinalizeAfterExpiry 在 observer 无法读取 store 时保留最后快照，最迟在会话到期后
// finalize；MarkLiveCallClosed 的 first 语义负责与其他恢复路径去重。
func (s *Service) FinalizeAfterExpiry(ctx context.Context, record *session.LiveCallRecord) {
	if record == nil {
		return
	}
	if wait := time.Until(record.ExpiresAt); wait > 0 {
		if !waitLiveObserver(ctx, wait) {
			return
		}
	}
	s.Finalize(record)
}

func (s *Service) RefreshLease(record *session.LiveCallRecord) bool {
	cache, err := s.ports.Leases()
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), liveRedisOperationTimeout)
	defer cancel()
	refreshed, err := cache.RefreshLiveLease(ctx, record.AccountID, record.UserID, record.APIKeyID, record.LeaseID)
	return err == nil && refreshed
}

func (s *Service) ReleaseLease(accountID, userID, apiKeyID int64, leaseID string) {
	cache, err := s.ports.Leases()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), liveRedisOperationTimeout)
	defer cancel()
	_ = cache.ReleaseLiveLease(ctx, accountID, userID, apiKeyID, leaseID)
}

func (s *Service) Finalize(record *session.LiveCallRecord) {
	if record == nil {
		return
	}
	store, err := s.ports.Store()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), liveRedisOperationTimeout)
	first, err := store.MarkLiveCallClosed(ctx, record.CallHash, liveClosedRecordTTL)
	cancel()
	if err != nil || !first {
		return
	}
	s.ReleaseLease(record.AccountID, record.UserID, record.APIKeyID, record.LeaseID)
	duration := int(time.Since(record.CreatedAt).Milliseconds())
	if duration < 0 {
		duration = 0
	}
	s.ports.RecordZeroUsage(context.Background(), record, duration)
}
func waitLiveObserver(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
