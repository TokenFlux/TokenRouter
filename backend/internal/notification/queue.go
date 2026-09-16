package notification

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
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
	runCtx       context.Context
	cancel       context.CancelFunc
	done         chan struct{}
	active       atomic.Int64
	stopErr      error
	emailService TaskProcessor
	taskChan     chan EmailTask
	wg           sync.WaitGroup
	stopChan     chan struct{}
	workers      int
	lifecycleMu  sync.RWMutex
	started      bool
	stopped      bool
}

// NewEmailQueueService 创建邮件队列服务
func NewEmailQueueService(emailService TaskProcessor, workers int) *EmailQueueService {
	if workers <= 0 {
		workers = 3 // 默认3个工作协程
	}

	ctx, cancel := context.WithCancel(context.Background())
	service := &EmailQueueService{
		runCtx: ctx, cancel: cancel, done: make(chan struct{}),
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
		s.active.Add(1)
		s.processTask(id, task)
		s.active.Add(-1)
	}

}

// processTask 处理任务
func (s *EmailQueueService) processTask(workerID int, task EmailTask) {
	ctx, cancel := context.WithTimeout(s.runCtx, 30*time.Second)
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
func (s *EmailQueueService) Stop() { _ = s.StopContext(context.Background()) }

// StopContext 封闭接收并等待已有任务；预算耗尽取消 SMTP 并保留未完成报告。
func (s *EmailQueueService) StopContext(ctx context.Context) error {
	s.lifecycleMu.Lock()
	if !s.stopped {
		s.stopped = true
		close(s.taskChan)
		close(s.stopChan)
		go func() {
			s.wg.Wait()
			s.lifecycleMu.Lock()
			if len(s.taskChan) > 0 && s.stopErr == nil {
				s.stopErr = fmt.Errorf("email queue drain incomplete: %d tasks", len(s.taskChan))
			}
			s.lifecycleMu.Unlock()
			close(s.done)
		}()
	}
	done := s.done
	s.lifecycleMu.Unlock()
	select {
	case <-done:
	case <-ctx.Done():
		s.lifecycleMu.Lock()
		if s.stopErr == nil {
			s.stopErr = fmt.Errorf("email queue drain incomplete: %d tasks: %w", int64(len(s.taskChan))+s.active.Load(), ctx.Err())
		}
		s.lifecycleMu.Unlock()
		s.cancel()
	}
	s.lifecycleMu.RLock()
	defer s.lifecycleMu.RUnlock()
	return s.stopErr
}

// TaskProcessor 在原 worker 时点准备身份邮件，不把令牌提前生成到入队时。
type TaskProcessor interface {
	SendVerifyCode(context.Context, string, string, ...string) error
	SendPasswordResetEmailWithCooldown(context.Context, string, string, string, ...string) error
}

func firstEmailLocale(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return strings.TrimSpace(values[0])
}
