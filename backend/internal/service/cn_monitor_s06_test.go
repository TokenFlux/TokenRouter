// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	errors "errors"
	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
	config "github.com/TokenFlux/TokenRouter/internal/config"
	egress "github.com/TokenFlux/TokenRouter/internal/egress"
	time "time"
)

type legacyCNMonitorFixtureStore struct {
	AccountRepository
	CNUsageMonitorSnapshotRepository
}

func (r legacyCNMonitorFixtureStore) GetByID(ctx context.Context, id int64) (*acctcore.Record, error) {
	v, err := r.AccountRepository.GetByID(ctx, id)
	return AccountRecordView(v), err
}
func (r legacyCNMonitorFixtureStore) ListByPlatform(ctx context.Context, platform string) ([]acctcore.Record, error) {
	v, err := r.AccountRepository.ListByPlatform(ctx, platform)
	return AccountRecordsView(v), err
}
func (r legacyCNMonitorFixtureStore) SetCNUsageDecisionCAS(ctx context.Context, id int64, expected, until time.Time, reason string, clear bool) (bool, error) {
	if writer, ok := r.AccountRepository.(interface {
		SetCNUsageDecisionCAS(context.Context, int64, time.Time, time.Time, string, bool) (bool, error)
	}); ok {
		return writer.SetCNUsageDecisionCAS(ctx, id, expected, until, reason, clear)
	}
	return false, errors.New("fixture lacks identity CAS")
}
func (r *cnUsageMonitorRepo) SetCNUsageDecisionCAS(ctx context.Context, id int64, expected, until time.Time, reason string, clear bool) (bool, error) {
	r.mu.Lock()
	matches := r.accounts[id] != nil && r.accounts[id].UpdatedAt.Equal(expected)
	r.mu.Unlock()
	if !matches {
		return false, nil
	}
	if clear {
		return true, r.ClearTempUnschedulable(ctx, id)
	}
	return true, r.SetTempUnschedulable(ctx, id, until, reason)
}
func newCNMonitorLegacyFixture(repo AccountRepository, usage *UpstreamUsageService, cfg *config.Config, configure ...func(*acctcore.CNMonitorOptions)) *acctcore.CNUsageMonitor {
	o := acctcore.CNMonitorOptions{Now: time.Now, InstanceID: "fixture-owner", RoundTimeout: time.Second, ProbeTimeout: time.Second, BalanceThreshold: 0.5}
	if cfg != nil {
		v := cfg.Gateway.CNProviders
		o.Enabled = v.MonitorEnabled
		o.Interval = time.Duration(v.IntervalMinutes) * time.Minute
		o.Concurrency = v.Concurrency
		o.BalanceThreshold = v.BalanceThreshold
		h := cfg.Security.URLAllowlist
		o.HostPolicy = egress.MonitorHostPolicy{Enabled: h.Enabled, AllowInsecureHTTP: h.AllowInsecureHTTP, AllowPrivate: h.AllowPrivateHosts, AllowedHosts: h.UpstreamHosts}
	}
	for _, apply := range configure {
		apply(&o)
	}
	snapshots, ok := repo.(CNUsageMonitorSnapshotRepository)
	if !ok {
		panic("fixture lacks snapshot CAS")
	}
	return acctcore.NewCNUsageMonitor(legacyCNMonitorFixtureStore{repo, snapshots}, usage.Core(), o)
}
