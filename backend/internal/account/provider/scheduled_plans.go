// 本文件维护 provider 的所属能力；兼容入口复用唯一实现。
package provider

import (
	"context"
	"sync"
	time "time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	cron "github.com/robfig/cron/v3"
)

// 解析器不持有运行状态，保留原分钟/小时/日/月/星期字段集合。
var scheduledTestParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

func NextScheduledTestRun(expression string, from time.Time) (time.Time, error) {
	schedule, err := scheduledTestParser.Parse(expression)
	if err != nil {
		return time.Time{}, err
	}
	return schedule.Next(from), nil
}

// ScheduledCron 只在 Start 注册和启动原分钟 cron；Stop 永久禁止再次启动。
type ScheduledCron struct {
	mu               sync.Mutex
	cron             *cron.Cron
	started, stopped bool
	done             context.Context
}

func NewScheduledCron(location *time.Location) account.ScheduledTestSchedule {
	return &ScheduledCron{cron: cron.New(cron.WithParser(scheduledTestParser), cron.WithLocation(location))}
}
func (s *ScheduledCron) Start(ctx context.Context, run func()) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped || s.started {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := s.cron.AddFunc("* * * * *", run); err != nil {
		return err
	}
	s.started = true
	s.cron.Start()
	return nil
}
func (s *ScheduledCron) Stop(ctx context.Context) error {
	s.mu.Lock()
	if !s.stopped {
		s.stopped = true
		s.done = s.cron.Stop()
	}
	done := s.done
	s.mu.Unlock()
	select {
	case <-done.Done():
		return nil
	default:
	}
	select {
	case <-done.Done():
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
