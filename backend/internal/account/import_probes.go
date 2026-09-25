// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	"context"
	"errors"
	"sync"
	"time"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

const (
	grokImportProbeTimeout    = 25 * time.Second
	grokImportProbeQueueLimit = 64
)

// GrokImportProbeResult 只投影记录所需的脱敏观测，不包含凭据或完整额度报文。
type GrokImportProbeResult struct {
	Model           string
	StatusCode      int
	HeadersObserved bool
}
type GrokImportProbeOptions struct {
	Concurrency              int
	Timeout                  time.Duration
	Debug, Info, Warn, Error func(string, ...any)
}

type GrokImportProber interface {
	QueryQuota(ctx context.Context, accountID int64) (*GrokImportProbeResult, error)
}
type grokImportProbeTask struct {
	prober    GrokImportProber
	accountID int64
}
type GrokImportProbeScheduler struct {
	options     GrokImportProbeOptions
	stopped     bool
	activity    operationActivity
	mu          sync.Mutex
	queue       []grokImportProbeTask
	pending     map[int64]struct{}
	inFlight    map[int64]struct{}
	concurrency int
	workers     int
	maxWorkers  int
	timeout     time.Duration
}

func NewGrokImportProbeScheduler(options GrokImportProbeOptions) *GrokImportProbeScheduler {
	concurrency, timeout := options.Concurrency, options.Timeout
	if options.Debug == nil {
		options.Debug = func(string, ...any) {}
	}
	if options.Info == nil {
		options.Info = func(string, ...any) {}
	}
	if options.Warn == nil {
		options.Warn = func(string, ...any) {}
	}
	if options.Error == nil {
		options.Error = func(string, ...any) {}
	}
	if concurrency <= 0 {
		concurrency = 1
	}
	if timeout <= 0 {
		timeout = grokImportProbeTimeout
	}
	return &GrokImportProbeScheduler{options: options,
		concurrency: concurrency,
		timeout:     timeout,
		pending:     make(map[int64]struct{}),
		inFlight:    make(map[int64]struct{}),
	}
}
func (s *GrokImportProbeScheduler) Schedule(prober GrokImportProber, account *AccountSnapshot) {
	if s == nil || prober == nil || account == nil || account.ID <= 0 {
		return
	}
	if account.Platform != PlatformGrok || account.Type != AccountTypeOAuth {
		return
	}

	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	if _, exists := s.pending[account.ID]; exists {
		s.mu.Unlock()
		return
	}
	if _, exists := s.inFlight[account.ID]; exists {
		s.mu.Unlock()
		return
	}
	if len(s.queue) >= grokImportProbeQueueLimit {
		s.mu.Unlock()
		s.options.Debug("grok_import_active_probe_dropped", "account_id", account.ID, "reason", "queue_full")
		return
	}
	s.queue = append(s.queue, grokImportProbeTask{prober: prober, accountID: account.ID})
	s.pending[account.ID] = struct{}{}
	if s.workers < s.concurrency {
		s.workers++
		if s.workers > s.maxWorkers {
			s.maxWorkers = s.workers
		}
		ctx, done, err := s.activity.begin(context.Background(), ErrImportProbeStopped)
		if err != nil {
			s.workers--
			s.mu.Unlock()
			return
		}
		go func() { defer done(); s.worker(ctx) }()
	}
	s.mu.Unlock()
}
func (s *GrokImportProbeScheduler) worker(ctx context.Context) {
	for {
		task, ok := s.nextTask()
		if !ok {
			return
		}
		s.run(ctx, task.prober, task.accountID)
		s.finish(task.accountID)
	}
}
func (s *GrokImportProbeScheduler) nextTask() (grokImportProbeTask, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped || len(s.queue) == 0 {
		s.workers--
		return grokImportProbeTask{}, false
	}
	task := s.queue[0]
	s.queue[0] = grokImportProbeTask{}
	s.queue = s.queue[1:]
	if len(s.queue) == 0 {
		s.queue = nil
	}
	delete(s.pending, task.accountID)
	s.inFlight[task.accountID] = struct{}{}
	return task, true
}
func (s *GrokImportProbeScheduler) finish(accountID int64) {
	s.mu.Lock()
	delete(s.inFlight, accountID)
	s.mu.Unlock()
}
func (s *GrokImportProbeScheduler) run(parent context.Context, prober GrokImportProber, accountID int64) {
	if parent.Err() != nil {
		return
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			s.options.Error(
				"grok_import_active_probe_panic",
				"account_id", accountID,
				"recovery_type", panicType(recovered),
			)
		}
	}()

	// 排队时间不计入超时，确保每个导入账号都会执行探测；该超时只限制实际的上游请求。
	ctx, cancel := context.WithTimeout(parent, s.timeout)
	defer cancel()
	result, err := prober.QueryQuota(ctx, accountID)
	if err != nil {
		s.options.Warn(
			"grok_import_active_probe_failed",
			"account_id", accountID,
			"status", int(infraerrors.FromError(err).Code),
			"reason", infraerrors.Reason(err),
		)
		return
	}
	if result == nil {
		s.options.Warn(
			"grok_import_active_probe_failed",
			"account_id", accountID,
			"reason", "empty_result",
		)
		return
	}

	s.options.Info(
		"grok_import_active_probe_completed",
		"account_id", accountID,
		"model", result.Model,
		"status", result.StatusCode,
		"headers_observed", result.HeadersObserved,
	)
}
func panicType(value any) string {
	switch value.(type) {
	case string:
		return "string"
	case error:
		return "error"
	default:
		return "unknown"
	}
}

var ErrImportProbeStopped = errors.New("account import probes are stopped")

// StopContext 取消未领取的尽力探测，取消并等待在途 worker；不把取消队列报告为已探测成功。
func (s *GrokImportProbeScheduler) StopContext(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	s.stopped = true
	s.queue = nil
	clear(s.pending)
	s.mu.Unlock()
	return s.activity.stop(ctx, "account import probes")
}
