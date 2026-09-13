// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	context "context"
	json "encoding/json"
	fmt "fmt"
	time "time"
)

// qoderUsageFlightResult 让同 key 的等待者核对本轮身份，不共享可变结果。
type qoderUsageFlightResult struct {
	Usage    *UsageInfo
	Identity string
}

// QoderUsageOptions 只提供供应商请求、响应归一化和故障诊断；缓存与账号写入由核心拥有。
type QoderUsageOptions struct {
	Fetch   func(context.Context, *Record, func() time.Time) (*UsageInfo, error)
	Degrade func(error, *Record, time.Time) *UsageInfo
	Enrich  func(*UsageInfo, *Record)
}

const (
	QoderUsageQuotaSnapshotExtraKey  = "qoder_quota_snapshot"
	QoderUsageQuotaUpdatedAtExtraKey = "qoder_quota_updated_at"
)

func (s *OAuthUsageService) GetQoderUsage(ctx context.Context, account *Record, force bool) (*UsageInfo, error) {
	now := s.options.Now()
	if account == nil {
		return &UsageInfo{UpdatedAt: &now}, nil
	}
	if s.cache == nil {
		s.cache = NewOAuthUsageCache()
	}

	if !force {
		if cached, ok := s.cache.LoadQoder(account.ID); ok {
			if cache, ok := cached.(*OAuthQoderUsageCache); ok && QoderUsageCacheUsable(account, cache, s.options.Now()) {
				return CloneUsageInfo(cache.UsageInfo), nil
			}
		}
	}

	identity := UsageCacheIdentity(account)
	flightKey := fmt.Sprintf("qoder-usage:%d", account.ID)
	result, flightErr, _ := s.cache.DoQoder(flightKey, func() (any, error) {
		if !force {
			if cached, ok := s.cache.LoadQoder(account.ID); ok {
				if cache, ok := cached.(*OAuthQoderUsageCache); ok && QoderUsageCacheUsable(account, cache, s.options.Now()) {
					return qoderUsageFlightResult{CloneUsageInfo(cache.UsageInfo), cache.Identity}, nil
				}
			}
		}

		fetchCtx, cancel, beginErr := s.BeginDetached(ctx, 30*time.Second)
		if beginErr != nil {
			return nil, beginErr
		}
		defer cancel()

		usage, err := s.options.Qoder.Fetch(fetchCtx, account, s.options.Now)
		if fetchCtx.Err() == context.Canceled {
			return nil, fetchCtx.Err()
		}
		if err != nil {
			degraded := s.options.Qoder.Degrade(err, account, s.options.Now())
			s.options.Qoder.Enrich(degraded, account)
			s.cache.StoreQoder(account.ID, &OAuthQoderUsageCache{Identity: identity, UsageInfo: CloneUsageInfo(degraded), Timestamp: s.options.Now()})
			return qoderUsageFlightResult{CloneUsageInfo(degraded), identity}, nil
		}

		s.options.Qoder.Enrich(usage, account)
		version := ObserveUsageVersion(account)
		applied, persistErr := s.persistQoderQuotaSnapshot(fetchCtx, version, usage.QoderQuota)
		if persistErr == nil && !applied {
			return nil, ErrUsageObservationChanged
		}
		s.applyQoderQuotaSchedulingSignal(fetchCtx, account, usage.QoderQuota)
		s.cache.StoreQoder(account.ID, &OAuthQoderUsageCache{Identity: identity, UsageInfo: CloneUsageInfo(usage), Timestamp: s.options.Now()})
		return qoderUsageFlightResult{CloneUsageInfo(usage), identity}, nil
	})
	if flightErr != nil {
		return nil, flightErr
	}
	completed, ok := result.(qoderUsageFlightResult)
	if !ok || completed.Usage == nil {
		return &UsageInfo{UpdatedAt: &now}, nil
	}
	if completed.Identity != identity {
		return nil, ErrUsageObservationChanged
	}
	return CloneUsageInfo(completed.Usage), nil
}
func (s *OAuthUsageService) persistQoderQuotaSnapshot(ctx context.Context, version UsageObservationVersion, quota *QoderQuotaInfo) (bool, error) {
	if s == nil || s.accountRepo == nil || quota == nil {
		return true, nil
	}
	raw, err := json.Marshal(quota)
	if err != nil {
		return true, nil
	}
	var snapshot map[string]any
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return true, nil
	}
	writer, ok := s.accountRepo.(UsageObservationWriter)
	if !ok {
		return false, ErrUsageObservationWriterMissing
	}
	applied, err := writer.UpdateUsageExtraIfUnchanged(ctx, version, map[string]any{
		QoderUsageQuotaSnapshotExtraKey:  snapshot,
		QoderUsageQuotaUpdatedAtExtraKey: s.options.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		s.options.Warn("failed to persist qoder quota snapshot", "account_id", version.ID, "error", err)
	}
	return applied, err
}

func (s *OAuthUsageService) applyQoderQuotaSchedulingSignal(ctx context.Context, account *Record, quota *QoderQuotaInfo) {
	if s == nil || s.accountRepo == nil || account == nil {
		return
	}
	writer, ok := s.accountRepo.(UsageObservationWriter)
	if !ok {
		s.options.Warn("qoder usage conditional writer is missing")
		return
	}
	version := ObserveUsageVersion(account)
	now := s.options.Now()
	resetAt, ok := QoderQuotaRateLimitResetAt(quota, now)
	if !ok {
		if QoderQuotaShouldClearRateLimit(account, quota, now) {
			if _, err := writer.ClearUsageRateLimitIfUnchanged(ctx, version); err != nil {
				s.options.Warn("failed to clear qoder quota rate limit", "account_id", account.ID, "error", err)
			}
		}
		return
	}
	if _, err := writer.SetUsageRateLimitIfUnchanged(ctx, version, resetAt); err != nil {
		s.options.Warn("failed to apply qoder quota rate limit", "account_id", account.ID, "error", err)
	}
}
