package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// QoderUsage 绑定账号会话与技术传输；用量缓存和健康条件写入仍由账号核心拥有。
type QoderUsage struct {
	Sessions  *QoderTokenProvider
	Transport QoderTransport
	Profiles  *egressprovider.TLSProfiles
}

func (s *QoderUsage) Options() accountcore.QoderUsageOptions {
	return accountcore.QoderUsageOptions{
		Fetch: func(ctx context.Context, value *accountcore.Record, now func() time.Time) (*accountcore.UsageInfo, error) {
			response, err := s.fetchQoderQuotaUsage(ctx, value)
			if err != nil {
				return nil, err
			}
			return buildQoderUsageInfoAt(response, now()), nil
		},
		Degrade: buildQoderDegradedUsageAt,
	}
}

// qoderQuotaProgressFromRaw 将站点归一化结果投影到账号公开展示值。
func qoderQuotaProgressFromRaw(raw *qoder.QuotaProgress, useCapAsTotal bool) *accountcore.QoderQuotaProgress {
	value := qoder.NormalizeQuotaProgress(raw, useCapAsTotal)
	if value == nil {
		return nil
	}
	return &accountcore.QoderQuotaProgress{
		Total:          value.Total,
		Used:           value.Used,
		Remaining:      value.Remaining,
		Percentage:     value.Percentage,
		Unit:           value.Unit,
		DetailURL:      value.DetailURL,
		Cap:            value.Cap,
		Available:      value.Available,
		OrganizationID: value.OrganizationID,
	}
}

func (s *QoderUsage) fetchQoderQuotaUsage(ctx context.Context, account *accountcore.Record) (*qoder.QuotaUsageResponse, error) {
	if account == nil {
		return nil, fmt.Errorf("qoder: account is nil")
	}
	provider := s.Sessions
	if provider == nil {
		provider = NewQoderTokenProvider(qoder.SessionBuilder{})
		provider.SetHTTPUpstream(s.Transport, s.Profiles)
	}
	usage, err := s.fetchQoderQuotaUsageWithProvider(ctx, account, provider)
	if err == nil || strings.TrimSpace(account.GetCredential("pat")) == "" || !isQoderAuthenticationError(err) {
		return usage, err
	}

	// PAT 可随时重新交换；认证失败时丢弃旧 session 并仅重试一次，避免额度页永久停留在需重新授权状态。
	provider.Invalidate(account.ID)
	return s.fetchQoderQuotaUsageWithProvider(ctx, account, provider)
}

func (s *QoderUsage) fetchQoderQuotaUsageWithProvider(ctx context.Context, account *accountcore.Record, provider *QoderTokenProvider,

) (*qoder.QuotaUsageResponse, error) {
	session, err := provider.GetSession(ctx, account)
	if err != nil {
		return nil, err
	}
	site, err := qoder.ParseSite(account.GetCredential("site"))
	if err != nil {
		return nil, err
	}
	profile, err := qoder.ProfileForSite(site)
	if err != nil {
		return nil, err
	}
	logicalPath := qoder.QuotaUsagePath
	if profile.Site == qoder.SiteCN {
		query := url.Values{}
		if organizationID := strings.TrimSpace(session.Identity.OrganizationID); organizationID != "" {
			query.Set("orgId", organizationID)
		}
		if encoded := query.Encode(); encoded != "" {
			logicalPath += "?" + encoded
		}
	}
	doer := QoderRequestDoer(account, s.Transport, s.Profiles)
	var usage qoder.QuotaUsageResponse
	client := qoder.NewClientForProfile(profile)
	request := client.BearerJSONRequestContextWithDoer
	if qoderQuotaUsesSignedAuth(account, profile.Site) {
		request = client.JSONRequestContextWithDoer
	}
	if err := request(ctx, http.MethodGet, session, logicalPath, nil, nil, doer, &usage); err != nil {
		return nil, fmt.Errorf("qoder: quota usage request: %w", err)
	}
	return &usage, nil
}

// qoderQuotaUsesSignedAuth 对齐 1.24.2 客户端：国际站和 QoderCN20 使用 COSY 签名，国内旧会话使用普通 Bearer。
func qoderQuotaUsesSignedAuth(account *accountcore.Record, site qoder.Site) bool {
	if site != qoder.SiteCN {
		return true
	}
	if strings.TrimSpace(account.GetCredential("pat")) != "" {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(account.GetCredential("refresh_mode")), qoder.RefreshModeQoderCN20)
}

// isQoderAuthenticationError 只把明确的 401/403 视为可通过 PAT 重建 session 的认证失败。
func isQoderAuthenticationError(err error) bool {
	var apiErr *qoder.APIError
	return errors.As(err, &apiErr) && (apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden)
}

func buildQoderUsageInfoAt(resp *qoder.QuotaUsageResponse, now time.Time) *accountcore.UsageInfo {
	return &accountcore.UsageInfo{
		Source:     "active",
		UpdatedAt:  &now,
		QoderQuota: qoderQuotaInfoFromResponse(resp, now, false),
	}
}

func qoderQuotaInfoFromResponse(resp *qoder.QuotaUsageResponse, updatedAt time.Time, fromSnapshot bool) *accountcore.QoderQuotaInfo {
	if resp == nil {
		return nil
	}
	var expiresAt *time.Time
	if resp.ExpiresAt > 0 {
		rawExpiresAt := int64(resp.ExpiresAt)
		var t time.Time
		if rawExpiresAt >= 1_000_000_000_000 {
			t = time.UnixMilli(rawExpiresAt)
		} else {
			t = time.Unix(rawExpiresAt, 0)
		}
		expiresAt = &t
	}
	quota := &accountcore.QoderQuotaInfo{
		UserID:               strings.TrimSpace(resp.UserID),
		UserType:             strings.TrimSpace(resp.UserType),
		UsageType:            strings.TrimSpace(resp.UsageType),
		TotalUsagePercentage: qoder.NormalizeQuotaPercentage(resp.TotalUsagePercentage),
		IsQuotaExceeded:      resp.IsQuotaExceeded,
		ExpiresAt:            expiresAt,
		UpgradeURL:           strings.TrimSpace(resp.UpgradeURL),
		AddCreditsURL:        strings.TrimSpace(resp.AddCreditsURL),
		IsPlanQuotaProrated:  resp.IsPlanQuotaProrated,
		LastUpdatedAt:        &updatedAt,
		SnapshotFromAccount:  fromSnapshot,
	}
	quota.UserQuota = qoderQuotaProgressFromRaw(resp.UserQuota, true)
	quota.AddOnQuota = qoderQuotaProgressFromRaw(firstNonNilQoderQuotaProgress(resp.AddOnQuota, resp.AddOnQuotaSnake), true)
	quota.OrgResourcePackage = qoderQuotaProgressFromRaw(firstNonNilQoderQuotaProgress(
		resp.OrgResourcePackage,
		resp.OrgResourcePkgSnake,
		resp.SharedQuota,
		resp.SharedQuotaSnake,
	), true)
	return quota
}

func firstNonNilQoderQuotaProgress(values ...*qoder.QuotaProgress) *qoder.QuotaProgress {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func buildQoderDegradedUsageAt(err error, account *accountcore.Record, now time.Time) *accountcore.UsageInfo {
	info := &accountcore.UsageInfo{
		UpdatedAt: &now,
		Error:     fmt.Sprintf("usage API error: %v", err),
	}
	if err != nil {
		var apiErr *qoder.APIError
		if errors.As(err, &apiErr) {
			switch apiErr.StatusCode {
			case http.StatusUnauthorized, http.StatusForbidden:
				info.ErrorCode = accountcore.ErrorCodeUnauthenticated
				info.NeedsReauth = true
			case http.StatusTooManyRequests:
				info.ErrorCode = accountcore.ErrorCodeRateLimited
			default:
				info.ErrorCode = accountcore.ErrorCodeNetworkError
			}
			if snapshot := qoderQuotaSnapshotFromExtra(account); snapshot != nil {
				snapshot.SnapshotFromAccount = true
				info.QoderQuota = snapshot
			}
			return info
		}
		errStr := err.Error()
		switch {
		case strings.Contains(errStr, "status 401") || strings.Contains(errStr, "status 403"):
			info.ErrorCode = accountcore.ErrorCodeUnauthenticated
			info.NeedsReauth = true
		case strings.Contains(errStr, "status 429"):
			info.ErrorCode = accountcore.ErrorCodeRateLimited
		case strings.Contains(errStr, "request:"):
			info.ErrorCode = accountcore.ErrorCodeNetworkError
		default:
			info.ErrorCode = accountcore.ErrorCodeNetworkError
		}
	}
	if snapshot := qoderQuotaSnapshotFromExtra(account); snapshot != nil {
		snapshot.SnapshotFromAccount = true
		info.QoderQuota = snapshot
	}
	return info
}

func qoderQuotaSnapshotFromExtra(account *accountcore.Record) *accountcore.QoderQuotaInfo {
	if account == nil || account.Extra == nil {
		return nil
	}
	raw, ok := account.Extra[accountcore.QoderUsageQuotaSnapshotExtraKey]
	if !ok || raw == nil {
		return nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var quota accountcore.QoderQuotaInfo
	if err := json.Unmarshal(data, &quota); err != nil {
		return nil
	}
	if quota.LastUpdatedAt == nil {
		if updatedRaw, ok := account.Extra[accountcore.QoderUsageQuotaUpdatedAtExtraKey].(string); ok {
			if parsed, err := time.Parse(time.RFC3339, updatedRaw); err == nil {
				quota.LastUpdatedAt = &parsed
			}
		}
	}
	return &quota
}
