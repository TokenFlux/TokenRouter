package provider_test

import (
	"context"
	"time"

	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
)

// cnQueryFixtureOptions 只包含查询目标许可和监控预算，不加载应用配置。
type cnQueryFixtureOptions struct {
	Policy  egress.UsageURLPolicy
	Monitor acctcore.CNMonitorOptions
}

func newCNQueryFixtureOptions() *cnQueryFixtureOptions {
	return &cnQueryFixtureOptions{Policy: egress.UsageURLPolicy{Configured: true, AllowInsecureHTTP: true}}
}
func newCNUsageFixture(repo acctcore.UpstreamUsageReader, transport httpclient.UpstreamTransport, cfg *cnQueryFixtureOptions, tls *egressprovider.TLSProfiles) *acctcore.UpstreamUsageService {
	options := accountprovider.UsageHTTPOptions{Available: repo != nil && transport != nil}
	if cfg != nil {
		options.Policy = cfg.Policy
	}
	if transport != nil {
		options.Do = transport.DoWithTLS
	}
	if tls != nil {
		options.ResolveTLS = tls.ResolveRequestTLS
	}
	return acctcore.NewUpstreamUsageService(repo, accountprovider.NewUpstreamUsageHTTPExecution(options), acctcore.UpstreamUsageOptions{Now: time.Now})
}
func newCNMonitorFixture(repo acctcore.CNMonitorStore, queries *acctcore.UpstreamUsageService, cfg *cnQueryFixtureOptions, configure ...func(*acctcore.CNMonitorOptions)) *acctcore.CNUsageMonitor {
	options := acctcore.CNMonitorOptions{Now: time.Now, InstanceID: "fixture-owner", RoundTimeout: time.Second, ProbeTimeout: time.Second, BalanceThreshold: 0.5}
	if cfg != nil {
		options.Enabled = cfg.Monitor.Enabled
		options.Interval = cfg.Monitor.Interval
		options.Concurrency = cfg.Monitor.Concurrency
		options.BalanceThreshold = cfg.Monitor.BalanceThreshold
		options.HostPolicy = egress.MonitorHostPolicy{Enabled: cfg.Policy.Enabled, AllowInsecureHTTP: cfg.Policy.AllowInsecureHTTP, AllowPrivate: cfg.Policy.AllowPrivateHosts, AllowedHosts: cfg.Policy.UpstreamHosts}
	}
	for _, apply := range configure {
		apply(&options)
	}
	return acctcore.NewCNUsageMonitor(repo, queries, options)
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
