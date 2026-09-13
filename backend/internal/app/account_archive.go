package app

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/app/legacybridge"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"log/slog"
	"time"
)

// provideAccountArchive 显式组合文件用例，代理、账号、探测和隐私使用唯一生产实例。
func provideAccountArchive(admin *account.Admin, proxies *egress.ProxyTransfer, privacy *account.PrivacyService, settings *service.SettingService, probes *account.GrokImportProbeScheduler, grok *service.GrokQuotaService, tasks *lifecycle.Tasks) *account.Archive {
	options := account.ArchiveOptions{Now: time.Now, Info: slog.Info, Error: slog.Error, Debug: slog.Debug, DecodeIDToken: accountprovider.DecodeArchiveIDToken, Background: tasks.Go, ForcePrivacy: privacy.ForceAntigravityPrivacy, Defaults: legacybridge.AccountArchiveDefaults{Source: settings}.Read,
		Probe: func(snapshot account.AccountSnapshot) {
			probes.Schedule(legacybridge.AccountImportQuotaProbe{Source: grok}, &snapshot)
		},
	}
	return account.NewArchive(admin, proxies, options)
}
