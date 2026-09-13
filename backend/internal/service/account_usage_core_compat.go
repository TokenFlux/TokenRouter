// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	log "log"
	slog "log/slog"
	rand "math/rand/v2"
	time "time"
)

// Core 为旧独立构造提供单一核心实例；生产装配先调用 BindCore。
func (s *AccountUsageService) Core() *accountcore.OAuthUsageService {
	s.coreOnce.Do(func() {
		if s.cache == nil {
			s.cache = NewUsageCache()
		}
		s.core = accountcore.NewOAuthUsageService(legacyOAuthUsageRepository(s.accountRepo), s.cache, s.localUsageStatistics(), s.OAuthUsageOptions())
	})
	return s.core
}
func (s *AccountUsageService) BindCore(core *accountcore.OAuthUsageService, stats *accountcore.LocalUsageStatistics) {
	s.coreOnce.Do(func() { s.core = core; s.statistics = stats })
}

// OAuthUsageOptions 仅投影供应商调用，不创建缓存或进行查询。
func (s *AccountUsageService) OAuthUsageOptions() accountcore.OAuthUsageOptions {
	return accountcore.OAuthUsageOptions{Now: time.Now, Jitter: rand.Int64N, Log: log.Printf, Warn: slog.Warn,
		OpenAI:      s.openAIUsageOptions(),
		Gemini:      s.geminiUsageOptions(),
		Antigravity: s.antigravityUsageOptions(),
		Grok:        s.grokUsageOptions(),
		Qoder:       s.qoderUsageOptions(),
		Anthropic: func(ctx context.Context, v *accountcore.Record) (*accountcore.ClaudeUsageResponse, error) {
			return s.fetchOAuthUsageRaw(ctx, AccountFromRecord(v))
		}, OpenAIQuotaPause: func(ctx context.Context, v *accountcore.Record, usage *accountcore.UsageInfo) {
			s.applyOpenAIQuotaAutoPauseState(ctx, AccountFromRecord(v), usage)
		}}
}

type legacyOAuthUsageReader struct{ source AccountRepository }

func (r legacyOAuthUsageReader) GetByID(ctx context.Context, id int64) (*accountcore.Record, error) {
	v, err := r.source.GetByID(ctx, id)
	return AccountRecordView(v), err
}
func (r legacyOAuthUsageReader) GetByIDs(ctx context.Context, ids []int64) ([]*accountcore.Record, error) {
	values, err := r.source.GetByIDs(ctx, ids)
	if values == nil {
		return nil, err
	}
	out := make([]*accountcore.Record, len(values))
	for i, v := range values {
		out[i] = AccountRecordView(v)
	}
	return out, err
}

// 旧构造的窗口参与能力必须显式提供，缺失时不进行无条件写入。
func (r legacyOAuthUsageReader) UpdateUsageSessionWindowEndIfUnchanged(ctx context.Context, v accountcore.UsageObservationVersion, observed *time.Time, end time.Time) (bool, error) {
	writer, ok := r.source.(accountcore.UsageSessionWindowWriter)
	if !ok {
		return false, accountcore.ErrUsageObservationWriterMissing
	}
	return writer.UpdateUsageSessionWindowEndIfUnchanged(ctx, v, observed, end)
}
func legacyOAuthUsageRepository(repo AccountRepository) accountcore.OAuthUsageReader {
	if repo == nil {
		return nil
	}
	reader := legacyOAuthUsageReader{repo}
	if writer, ok := repo.(accountcore.UsageRecoveryWriter); ok {
		return struct {
			accountcore.OAuthUsageReader
			accountcore.UsageRecoveryWriter
			accountcore.UsageObservationWriter
			accountcore.UsageSessionWindowWriter
		}{reader, writer, reader, reader}
	}
	return reader
}

// antigravityUsageOptions 只转交旧供应商调用和错误解析，S09 改绑。
func (s *AccountUsageService) antigravityUsageOptions() accountcore.AntigravityUsageOptions {
	return accountcore.AntigravityUsageOptions{
		CanFetch: func(v *accountcore.Record) bool {
			return s.antigravityQuotaFetcher != nil && s.antigravityQuotaFetcher.CanFetch(AccountFromRecord(v))
		},
		Fetch: func(ctx context.Context, v *accountcore.Record) (*accountcore.UsageInfo, error) {
			old := AccountFromRecord(v)
			proxy := s.antigravityQuotaFetcher.GetProxyURL(ctx, old)
			result, err := s.antigravityQuotaFetcher.FetchQuota(ctx, old, proxy)
			if result == nil {
				return nil, err
			}
			return result.UsageInfo, err
		},
		Degrade: buildAntigravityDegradedUsageAt,
		Enrich: func(info *accountcore.UsageInfo, v *accountcore.Record) {
			enrichUsageWithAccountError(info, AccountFromRecord(v))
		},
	}
}

// qoderUsageOptions 只提供原签名/会话恢复与 JSON 解析，S09 改绑。
func (s *AccountUsageService) qoderUsageOptions() accountcore.QoderUsageOptions {
	return accountcore.QoderUsageOptions{
		Fetch: func(ctx context.Context, v *accountcore.Record, now func() time.Time) (*accountcore.UsageInfo, error) {
			resp, err := s.fetchQoderQuotaUsage(ctx, AccountFromRecord(v))
			if err != nil {
				return nil, err
			}
			return buildQoderUsageInfoAt(resp, now()), nil
		},
		Degrade: func(err error, v *accountcore.Record, now time.Time) *accountcore.UsageInfo {
			return buildQoderDegradedUsageAt(err, AccountFromRecord(v), now)
		},
		Enrich: func(info *accountcore.UsageInfo, v *accountcore.Record) {
			enrichUsageWithAccountError(info, AccountFromRecord(v))
		},
	}
}

// geminiUsageOptions 仅投影动态额度和原统计查询，原模型归类规则已由 account 唯一实现。
func (s *AccountUsageService) geminiUsageOptions() accountcore.GeminiUsageOptions {
	options := accountcore.GeminiUsageOptions{Location: geminiQuotaLocation}
	if s.geminiQuotaService != nil && s.usageLogRepo != nil {
		options.Quota = func(ctx context.Context, v *accountcore.Record) (accountcore.GeminiQuota, bool) {
			return s.geminiQuotaService.QuotaForAccount(ctx, AccountFromRecord(v))
		}
		options.Totals = func(ctx context.Context, id int64, start, end time.Time) (accountcore.GeminiUsageTotals, error) {
			values, err := s.usageLogRepo.GetModelStatsWithFilters(ctx, start, end, 0, 0, id, 0, nil, nil, nil)
			if err != nil {
				return accountcore.GeminiUsageTotals{}, err
			}
			return geminiAggregateUsage(values), nil
		}
	}
	return options
}

// grokUsageOptions 只投影供应商快照与探测结果，保留实际查询的按需执行时机。
func (s *AccountUsageService) grokUsageOptions() accountcore.GrokUsageOptions {
	options := accountcore.GrokUsageOptions{Available: func() bool { return s.grokQuotaFetcher != nil }, StatsAvailable: func() bool { return s.usageLogRepo != nil }, Build: func(v *accountcore.Record) *accountcore.UsageInfo {
		return s.grokQuotaFetcher.BuildUsageInfo(AccountFromRecord(v))
	}, Enrich: func(info *accountcore.UsageInfo, v *accountcore.Record) {
		enrichUsageWithAccountError(info, AccountFromRecord(v))
	}}
	if s.grokQuotaService != nil {
		options.Probe = func(ctx context.Context, id int64) (*accountcore.GrokUsageProbe, error) {
			v, err := s.grokQuotaService.ProbeBilling(ctx, id)
			if v == nil {
				return nil, err
			}
			return &accountcore.GrokUsageProbe{Billing: v.Billing, LocalUsage24h: v.LocalUsage24h, LocalUsage7d: v.LocalUsage7d, LocalUsageMonthly: v.LocalUsageMonthly}, err
		}
	}
	return options
}
func legacyGrokUsageReader(repo UsageLogRepository) accountcore.LocalUsageStats {
	if repo == nil {
		return nil
	}
	return legacyLocalUsageStats{source: repo}
}

// openAIUsageOptions 只执行原供应商请求和影子报文转换，核心拥有查询决策与写回生命周期。
func (s *AccountUsageService) openAIUsageOptions() accountcore.OpenAIUsageOptions {
	options := accountcore.OpenAIUsageOptions{Probe: func(ctx context.Context, value *accountcore.Record) (map[string]any, error) {
		return s.fetchOpenAICodexSnapshot(ctx, AccountFromRecord(value))
	}}
	if s.openAIQuotaService != nil {
		options.Shadow = func(ctx context.Context, id int64, now time.Time) (map[string]any, error) {
			value, err := s.openAIQuotaService.QueryUsage(ctx, id)
			if err != nil {
				return nil, err
			}
			return buildCodexSparkWindowExtraUpdates(value, now), nil
		}
	}
	return options
}

// 过渡读取包装保留条件写入端口，不允许退回无锁读后覆盖。
func (r legacyOAuthUsageReader) UpdateUsageExtraIfUnchanged(ctx context.Context, v accountcore.UsageObservationVersion, updates map[string]any) (bool, error) {
	writer, ok := r.source.(accountcore.UsageExtraWriter)
	if !ok {
		return false, accountcore.ErrUsageObservationWriterMissing
	}
	return writer.UpdateUsageExtraIfUnchanged(ctx, v, updates)
}
func (r legacyOAuthUsageReader) SetUsageRateLimitIfUnchanged(ctx context.Context, v accountcore.UsageObservationVersion, reset time.Time) (bool, error) {
	writer, ok := r.source.(accountcore.UsageObservationWriter)
	if !ok {
		return false, accountcore.ErrUsageObservationWriterMissing
	}
	return writer.SetUsageRateLimitIfUnchanged(ctx, v, reset)
}
func (r legacyOAuthUsageReader) ClearUsageRateLimitIfUnchanged(ctx context.Context, v accountcore.UsageObservationVersion) (bool, error) {
	writer, ok := r.source.(accountcore.UsageObservationWriter)
	if !ok {
		return false, accountcore.ErrUsageObservationWriterMissing
	}
	return writer.ClearUsageRateLimitIfUnchanged(ctx, v)
}
