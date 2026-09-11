package service

import (
	"context"
	"log"
	"sync"
	"time"
)

// DeferredService provides deferred batch update functionality
type DeferredService struct {
	accountRepo AccountRepository
	timingWheel *TimingWheelService
	interval    time.Duration

	lastUsedUpdates sync.Map
	flushMu         sync.Mutex
	startOnce       sync.Once
	stopOnce        sync.Once
	lifecycleMu     sync.Mutex
	stopped         bool
	stopErr         error
}

// NewDeferredService creates a new DeferredService instance
func NewDeferredService(accountRepo AccountRepository, timingWheel *TimingWheelService, interval time.Duration) *DeferredService {
	return &DeferredService{
		accountRepo: accountRepo,
		timingWheel: timingWheel,
		interval:    interval,
	}
}

// Start starts the deferred service
// Start 只登记一次周期写回。
func (s *DeferredService) Start() {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.stopped {
		return
	}
	s.startOnce.Do(func() { s.timingWheel.ScheduleRecurring("deferred:last_used", s.interval, s.flushLastUsed) })
}

// Stop 等待周期写回结束后执行最后一次 flush。
func (s *DeferredService) Stop() error {
	s.stopOnce.Do(func() {
		s.lifecycleMu.Lock()
		s.stopped = true
		s.lifecycleMu.Unlock()
		s.timingWheel.CancelAndWait("deferred:last_used")
		s.stopErr = s.flushLastUsedErr()
		log.Printf("[DeferredService] Service stopped")
	})
	return s.stopErr
}

func (s *DeferredService) ScheduleLastUsedUpdate(accountID int64) {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.stopped {
		return
	}
	s.lastUsedUpdates.Store(accountID, time.Now())
}

func (s *DeferredService) flushLastUsed() {
	_ = s.flushLastUsedErr()
}

// 周期失败仍按原语义保留待写数据；最终写回则将失败交给生命周期报告。
func (s *DeferredService) flushLastUsedErr() error {
	s.flushMu.Lock()
	defer s.flushMu.Unlock()
	updates := make(map[int64]time.Time)
	s.lastUsedUpdates.Range(func(key, value any) bool {
		id, ok := key.(int64)
		if !ok {
			return true
		}
		ts, ok := value.(time.Time)
		if !ok {
			return true
		}
		updates[id] = ts
		s.lastUsedUpdates.Delete(key)
		return true
	})

	if len(updates) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := s.accountRepo.BatchUpdateLastUsed(ctx, updates); err != nil {
		log.Printf("[DeferredService] BatchUpdateLastUsed failed (%d accounts): %v", len(updates), err)
		for id, ts := range updates {
			s.lastUsedUpdates.Store(id, ts)
		}
		return err
	} else {
		log.Printf("[DeferredService] BatchUpdateLastUsed flushed %d accounts", len(updates))
	}
	return nil
}
