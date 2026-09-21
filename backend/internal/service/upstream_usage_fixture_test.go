package service

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	httpclient "github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
)

// 仅为尚待迁移的 CN 监控契约投影原内存仓储，查询算法与共享状态由原生模块承担。
type usageFixtureReader struct{ source AccountRepository }

// CN 与账号探测的旧夹具仍接收完整配置；只恢复原显式测试值。
func testUpstreamUsageConfig() *config.Config {
	return &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false, AllowInsecureHTTP: true}}}
}

func (r usageFixtureReader) GetByID(ctx context.Context, id int64) (*account.Record, error) {
	value, err := r.source.GetByID(ctx, id)
	return AccountRecordView(value), err
}

func NewUpstreamUsageService(repo AccountRepository, transport httpclient.UpstreamTransport, cfg *config.Config, tls *egressprovider.TLSProfiles) *account.UpstreamUsageService {
	options := accountprovider.UsageHTTPOptions{Available: repo != nil && transport != nil}
	if cfg != nil {
		value := cfg.Security.URLAllowlist
		options.Policy = egress.UsageURLPolicy{Configured: true, Enabled: value.Enabled, AllowInsecureHTTP: value.AllowInsecureHTTP, AllowPrivateHosts: value.AllowPrivateHosts, UpstreamHosts: value.UpstreamHosts}
	}
	if transport != nil {
		options.Do = transport.DoWithTLS
	}
	if tls != nil {
		options.ResolveTLS = tls.ResolveRequestTLS
	}
	return account.NewUpstreamUsageService(usageFixtureReader{repo}, accountprovider.NewUpstreamUsageHTTPExecution(options), account.UpstreamUsageOptions{Now: time.Now})
}

func EffectiveUpstreamUsageConfig(value *Account) (account.UpstreamUsageQueryConfig, error) {
	return account.EffectiveUpstreamUsageConfig(protocolRecord(value))
}

func cnUpstreamUsageAdapterName(value *Account) string {
	return account.CNUpstreamUsageAdapterName(protocolRecord(value))
}

func upstreamUsageContextFingerprint(value *Account, config account.UpstreamUsageQueryConfig) string {
	record := AccountRecordView(value)
	return account.UpstreamUsageContextFingerprint(record, config, accountprovider.UpstreamUsageBaseURL(record))
}
