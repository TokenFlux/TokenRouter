// 迁移后的测试夹具只提供设置与时间轮端口，不引入旧业务。
package usage

import (
	"context"
	"sync"

	"github.com/TokenFlux/TokenRouter/internal/infra/timingwheel"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	p "github.com/TokenFlux/TokenRouter/internal/settings/preaggregation"
)

const SettingKeyPreAggregationSettings = p.SettingKeyPreAggregationSettings

type PreAggregationUsageSettings = p.PreAggregationUsageSettings
type PreAggregationOpsSettings = p.PreAggregationOpsSettings
type runtimeSettingRepoStub struct {
	mu     sync.Mutex
	values map[string]string
}

func newRuntimeSettingRepoStub() *runtimeSettingRepoStub {
	return &runtimeSettingRepoStub{values: map[string]string{}}
}
func (s *runtimeSettingRepoStub) GetValue(_ context.Context, k string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.values[k]
	if !ok {
		return "", settings.ErrSettingNotFound
	}
	return v, nil
}
func (s *runtimeSettingRepoStub) Set(_ context.Context, k, v string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[k] = v
	return nil
}
func NewPreAggregationSettingsService(repo p.Repository, cfg *Options) *p.PreAggregationSettingsService {
	var options *p.Options
	if cfg != nil {
		options = &p.Options{Usage: p.UsageOptions{Enabled: cfg.DashboardAgg.Enabled, IntervalSeconds: cfg.DashboardAgg.IntervalSeconds, BackfillEnabled: cfg.DashboardAgg.BackfillEnabled, BackfillMaxDays: cfg.DashboardAgg.BackfillMaxDays}}
	}
	return p.NewPreAggregationSettingsService(repo, options)
}
func NewTimingWheelService() (*timingwheel.Wheel, error) { return timingwheel.New(), nil }
