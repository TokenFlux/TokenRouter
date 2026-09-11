package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
)

// Task type constants
const (
	TaskTypeVerifyCode    = "verify_code"
	TaskTypePasswordReset = "password_reset"
)

// EmailTask 邮件发送任务
type EmailTask struct {
	Email    string
	SiteName string
	TaskType string // "verify_code" or "password_reset"
	ResetURL string // Only used for password_reset task type
	Locale   string // Optional Accept-Language locale hint
}

// EmailQueueService 异步邮件队列服务
type EmailQueueService struct {
	emailService *EmailService
	taskChan     chan EmailTask
	wg           sync.WaitGroup
	stopChan     chan struct{}
	workers      int
	lifecycleMu  sync.RWMutex
	started      bool
	stopped      bool
}

// NewEmailQueueService 创建邮件队列服务
func NewEmailQueueService(emailService *EmailService, workers int) *EmailQueueService {
	if workers <= 0 {
		workers = 3 // 默认3个工作协程
	}

	service := &EmailQueueService{
		emailService: emailService,
		taskChan:     make(chan EmailTask, 100), // 缓冲100个任务
		stopChan:     make(chan struct{}),
		workers:      workers,
	}

	// 启动工作协程

	return service
}

// start 启动工作协程
func (s *EmailQueueService) Start() {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.started || s.stopped {
		return
	}
	s.started = true
	for i := 0; i < s.workers; i++ {
		s.wg.Add(1)
		go s.worker(i)
	}
	logger.LegacyPrintf("service.email_queue", "[EmailQueue] Started %d workers", s.workers)
}

// worker 工作协程
func (s *EmailQueueService) worker(id int) {
	defer s.wg.Done()

	for task := range s.taskChan {
		s.processTask(id, task)
	}

}

// processTask 处理任务
func (s *EmailQueueService) processTask(workerID int, task EmailTask) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	switch task.TaskType {
	case TaskTypeVerifyCode:
		if err := s.emailService.SendVerifyCode(ctx, task.Email, task.SiteName, task.Locale); err != nil {
			logger.LegacyPrintf("service.email_queue", "[EmailQueue] Worker %d failed to send verify code to %s: %v", workerID, task.Email, err)
		} else {
			logger.LegacyPrintf("service.email_queue", "[EmailQueue] Worker %d sent verify code to %s", workerID, task.Email)
		}
	case TaskTypePasswordReset:
		if err := s.emailService.SendPasswordResetEmailWithCooldown(ctx, task.Email, task.SiteName, task.ResetURL, task.Locale); err != nil {
			logger.LegacyPrintf("service.email_queue", "[EmailQueue] Worker %d failed to send password reset to %s: %v", workerID, task.Email, err)
		} else {
			logger.LegacyPrintf("service.email_queue", "[EmailQueue] Worker %d sent password reset to %s", workerID, task.Email)
		}
	default:
		logger.LegacyPrintf("service.email_queue", "[EmailQueue] Worker %d unknown task type: %s", workerID, task.TaskType)
	}
}

// EnqueueVerifyCode 将验证码发送任务加入队列
func (s *EmailQueueService) EnqueueVerifyCode(email, siteName string, locale ...string) error {
	s.lifecycleMu.RLock()
	defer s.lifecycleMu.RUnlock()
	if s.stopped {
		return fmt.Errorf("email queue is stopped")
	}

	task := EmailTask{
		Email:    email,
		SiteName: siteName,
		TaskType: TaskTypeVerifyCode,
		Locale:   firstEmailLocale(locale),
	}

	select {
	case s.taskChan <- task:
		logger.LegacyPrintf("service.email_queue", "[EmailQueue] Enqueued verify code task for %s", email)
		return nil
	default:
		return fmt.Errorf("email queue is full")
	}
}

// EnqueuePasswordReset 将密码重置邮件任务加入队列
func (s *EmailQueueService) EnqueuePasswordReset(email, siteName, resetURL string, locale ...string) error {
	s.lifecycleMu.RLock()
	defer s.lifecycleMu.RUnlock()
	if s.stopped {
		return fmt.Errorf("email queue is stopped")
	}

	task := EmailTask{
		Email:    email,
		SiteName: siteName,
		TaskType: TaskTypePasswordReset,
		ResetURL: resetURL,
		Locale:   firstEmailLocale(locale),
	}

	select {
	case s.taskChan <- task:
		logger.LegacyPrintf("service.email_queue", "[EmailQueue] Enqueued password reset task for %s", email)
		return nil
	default:
		return fmt.Errorf("email queue is full")
	}
}

// Stop 停止队列服务
// Stop 先封闭接收，再等待已有邮件任务处理完成。
func (s *EmailQueueService) Stop() {
	s.lifecycleMu.Lock()
	if !s.stopped {
		s.stopped = true
		close(s.taskChan)
		close(s.stopChan)
	}
	s.lifecycleMu.Unlock()
	s.wg.Wait()
	logger.LegacyPrintf("service.email_queue", "%s", "[EmailQueue] All workers stopped")
}
