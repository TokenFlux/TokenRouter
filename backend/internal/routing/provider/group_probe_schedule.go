package provider

import (
	"context"
	"github.com/robfig/cron/v3"
	"time"
)

// GroupProbeSchedule 保持五字段 cron、指定时区和每分钟边界；构造不启动任务。
type GroupProbeSchedule struct{ cron *cron.Cron }

func NewGroupProbeSchedule(location *time.Location) *GroupProbeSchedule {
	return &GroupProbeSchedule{cron: cron.New(cron.WithParser(cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow)), cron.WithLocation(location))}
}
func (s *GroupProbeSchedule) Start(run func()) error {
	if _, err := s.cron.AddFunc("* * * * *", run); err != nil {
		return err
	}
	s.cron.Start()
	return nil
}
func (s *GroupProbeSchedule) Stop() context.Context { return s.cron.Stop() }
