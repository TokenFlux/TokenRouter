package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/egress"
)

// grokImportQuotaProbe 只把旧额度服务的观测投影给账号导入探测器。
type grokImportQuotaProbe struct{ source *account.GrokQuotaService }

func (p grokImportQuotaProbe) QueryQuota(ctx context.Context, id int64) (*account.GrokImportProbeResult, error) {
	value, err := p.source.QueryQuota(ctx, id)
	if value == nil {
		return nil, err
	}
	return &account.GrokImportProbeResult{Model: value.Model, StatusCode: value.StatusCode, HeadersObserved: value.HeadersObserved}, err
}

// provideAccountArchive 显式组合文件用例，代理、账号、探测和隐私使用唯一生产实例。
func provideAccountArchive(admin *account.Admin, proxies *egress.ProxyTransfer, privacy *account.PrivacyService, settings *account.RuntimeSettings, probes *account.GrokImportProbeScheduler, grok *account.GrokQuotaService, tasks *lifecycle.Tasks) *account.Archive {
	options := account.ArchiveOptions{Now: time.Now, Info: slog.Info, Error: slog.Error, Debug: slog.Debug, DecodeIDToken: accountprovider.DecodeArchiveIDToken, Background: tasks.Go, ForcePrivacy: privacy.ForceAntigravityPrivacy, Defaults: settings.GetOpenAIOAuthImportDefaults,
		Probe: func(snapshot account.AccountSnapshot) {
			probes.Schedule(grokImportQuotaProbe{source: grok}, &snapshot)
		},
	}
	return account.NewArchive(admin, proxies, options)
}
