package provider

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// 夹具仅组合真实用量核心与平台端口，不复制缓存、回源、恢复或写入算法。
type oauthUsageFixtureOptions struct {
	accountRepo          account.OAuthUsageReader
	cache                *account.OAuthUsageCache
	httpUpstream         QoderTransport
	tlsFPProfileService  *egressprovider.TLSProfiles
	qoderSessionProvider *QoderTokenProvider
}

func newOAuthUsageFixture(options oauthUsageFixtureOptions) *account.OAuthUsageService {
	sessions := options.qoderSessionProvider
	if sessions == nil {
		sessions = NewQoderTokenProvider(qoder.SessionBuilder{})
		sessions.SetHTTPUpstream(options.httpUpstream, options.tlsFPProfileService)
	}
	qoderQuery := &QoderUsage{Sessions: sessions, Transport: options.httpUpstream, Profiles: options.tlsFPProfileService}
	qoderOptions := qoderQuery.Options()
	qoderOptions.Enrich = EnrichUsageWithAccountError
	requests := &OAuthUsageTransport{Transport: options.httpUpstream, Profiles: options.tlsFPProfileService}
	return account.NewOAuthUsageService(options.accountRepo, options.cache, nil, account.OAuthUsageOptions{
		Now: time.Now, Log: log.Printf, Warn: slog.Warn,
		Qoder:  qoderOptions,
		OpenAI: account.OpenAIUsageOptions{Probe: requests.ProbeOpenAI},
		OpenAIQuotaPause: func(_ context.Context, value *account.Record, info *account.UsageInfo) {
			if value != nil && info != nil {
				info.QuotaAutoPaused, _ = account.EvaluateQuotaAutoPause(value.Platform, value.Extra, account.QuotaAutoPauseSettings{}, time.Now())
			}
		},
	})
}

// 测试存储只按 ID 读取独立记录，复用原测试数据与缺失错误。
type usageRecordFixture struct{ accounts []account.Record }

func (r usageRecordFixture) GetByID(_ context.Context, id int64) (*account.Record, error) {
	for index := range r.accounts {
		if r.accounts[index].ID == id {
			return account.CloneRecord(&r.accounts[index]), nil
		}
	}
	return nil, errors.New("account not found")
}

func (r usageRecordFixture) GetByIDs(_ context.Context, ids []int64) ([]*account.Record, error) {
	result := make([]*account.Record, 0, len(ids))
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		for index := range r.accounts {
			if r.accounts[index].ID == id {
				result = append(result, account.CloneRecord(&r.accounts[index]))
				break
			}
		}
	}
	return result, nil
}

// 原断言观察这三个写入端口，真实条件写与事务另由 PostgreSQL 契约覆盖。
func (r *accountUsageCodexProbeRepo) UpdateUsageExtraIfUnchanged(ctx context.Context, version account.UsageObservationVersion, updates map[string]any) (bool, error) {
	return true, r.UpdateExtra(ctx, version.ID, updates)
}
func (r *accountUsageCodexProbeRepo) SetUsageRateLimitIfUnchanged(ctx context.Context, version account.UsageObservationVersion, reset time.Time) (bool, error) {
	return true, r.SetRateLimited(ctx, version.ID, reset)
}
func (r *accountUsageCodexProbeRepo) ClearUsageRateLimitIfUnchanged(ctx context.Context, version account.UsageObservationVersion) (bool, error) {
	return true, r.ClearRateLimit(ctx, version.ID)
}
