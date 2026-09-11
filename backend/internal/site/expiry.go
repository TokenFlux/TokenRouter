package site

import (
	"context"
	"log"
	"sync"
	"time"
)

const announcementExpiryUpdateTimeout = 5 * time.Second

// AnnouncementExpiryService 定期将已经到达结束时间的展示中公告归档。
type AnnouncementExpiryService struct {
	lifecycleMu      sync.Mutex
	started          bool
	stopped          bool
	announcementRepo AnnouncementRepository
	interval         time.Duration
	stopCh           chan struct{}
	stopOnce         sync.Once
	wg               sync.WaitGroup
}

// NewAnnouncementExpiryService 创建公告到期归档服务。
func NewAnnouncementExpiryService(announcementRepo AnnouncementRepository, interval time.Duration) *AnnouncementExpiryService {
	return &AnnouncementExpiryService{
		announcementRepo: announcementRepo,
		interval:         interval,
		stopCh:           make(chan struct{}),
	}
}

// Start 启动公告到期归档任务，并在启动后立即执行一次。
func (s *AnnouncementExpiryService) Start() {
	if s == nil || s.announcementRepo == nil || s.interval <= 0 {
		return
	}
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.started || s.stopped {
		return
	}
	s.started = true

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(s.interval)
		defer ticker.Stop()

		s.runOnce()
		for {
			select {
			case <-ticker.C:
				s.runOnce()
			case <-s.stopCh:
				return
			}
		}
	}()
}

// Stop 停止公告到期归档任务并等待当前扫描结束。
func (s *AnnouncementExpiryService) Stop() {
	if s == nil {
		return
	}
	s.lifecycleMu.Lock()
	s.stopped = true

	s.stopOnce.Do(func() {
		close(s.stopCh)
	})
	s.lifecycleMu.Unlock()
	s.wg.Wait()
}

func (s *AnnouncementExpiryService) runOnce() {
	ctx, cancel := context.WithTimeout(context.Background(), announcementExpiryUpdateTimeout)
	defer cancel()

	updated, err := s.announcementRepo.ArchiveExpired(ctx, time.Now())
	if err != nil {
		log.Printf("[AnnouncementExpiry] archive expired announcements failed: %v", err)
		return
	}
	if updated > 0 {
		log.Printf("[AnnouncementExpiry] archived %d expired announcements", updated)
	}
}
