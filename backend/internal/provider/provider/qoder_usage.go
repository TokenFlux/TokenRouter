package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// qoder_usage.go queries Qoder quota and builds active or saved-snapshot usage
// displays. Qoder quota display combines site-specific authentication with
// legacy quota field aliases. Global, CN PAT and QoderCN20 requests use COSY
// signing, while legacy CN direct sessions use Bearer authentication. CN
// requests carry an available organization ID as orgId, and a PAT
// authentication failure rebuilds its session once before another query.
//
// Active responses are converted to provider display values with normalized
// progress and second-or-millisecond expiry handling. Failure display can
// load the current account's last snapshot while retaining the current error
// classification and reauthorization hint. The provider core checks the saved
// account identity and returns a private copy for display enrichment.

// QoderUsage binds sessions and transport; the provider core owns usage
// caching and conditional health writes.
type QoderUsage struct {
	// Sessions supplies provider sessions for quota queries. Nil creates a local
	// default source and installs Transport and Profiles for this fetch.
	Sessions *QoderTokenProvider
	// Transport and Profiles are combined by QoderRequestDoer for site requests.
	// The same selection also configures a locally created session source when
	// Sessions
	// is absent, so its PAT exchange uses these request dependencies.
	Transport QoderTransport              // shared quota request transport
	Profiles  *egressprovider.TLSProfiles // optional TLS profile resolver
}

// Options binds active fetch conversion and degraded snapshot display for the core.
// Fetch queries through this adapter, then timestamps the converted response
// using the supplied clock callback. The core retains responsibility for shared
// usage caching and conditional provider writes.
func (s *QoderUsage) Options() providercore.QoderUsageOptions {
	return providercore.QoderUsageOptions{
		Fetch: func(ctx context.Context, value *providercore.Record, now func() time.Time) (*providercore.UsageInfo, error) {
			response, err := s.fetchQoderQuotaUsage(ctx, value)
			if err != nil {
				return nil, err
			}
			return buildQoderUsageInfoAt(response, now()), nil
		},
		Degrade: buildQoderDegradedUsageAt,
	}
}

// qoderQuotaProgressFromRaw converts normalized site progress into a new
// display value.
// Nil raw progress returns nil. The upstream normalizer handles cap-based totals,
// explicit-zero fields, percentages and legacy detail URLs before this adapter
// copies the resulting quota fields.
func qoderQuotaProgressFromRaw(raw *qoder.QuotaProgress, useCapAsTotal bool) *providercore.QoderQuotaProgress {
	value := qoder.NormalizeQuotaProgress(raw, useCapAsTotal)
	if value == nil {
		return nil
	}
	return &providercore.QoderQuotaProgress{
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

// fetchQoderQuotaUsage queries the provider quota with its configured session
// source.
// Nil providers return an error; missing Sessions creates a local source with
// the provider transport. Only PAT input with a typed 401 or 403 invalidates
// that session and retries once; other results return immediately.
func (s *QoderUsage) fetchQoderQuotaUsage(ctx context.Context, provider *providercore.Record) (*qoder.QuotaUsageResponse, error) {
	if provider == nil {
		return nil, fmt.Errorf("qoder: provider is nil")
	}
	sessionSource := s.Sessions
	if sessionSource == nil {
		sessionSource = NewQoderTokenProvider(qoder.SessionBuilder{})
		sessionSource.SetHTTPUpstream(s.Transport, s.Profiles)
	}
	usage, err := s.fetchQoderQuotaUsageWithProvider(ctx, provider, sessionSource)
	if err == nil || strings.TrimSpace(provider.GetCredential("pat")) == "" || !isQoderAuthenticationError(err) {
		return usage, err
	}

	// A typed authentication rejection can leave a previously built PAT
	// session stale.
	// Evict it before the one retry so acquisition exchanges the PAT again.
	sessionSource.Invalidate(provider.ID)
	return s.fetchQoderQuotaUsageWithProvider(ctx, provider, sessionSource)
}

// fetchQoderQuotaUsageWithProvider prepares a site quota query from an
// acquired session.
// The supplied session source must be configured and successful acquisition must
// provide identity metadata. CN adds nonblank organization identity to orgId;
// authentication follows the provider's site and refresh mode. Query errors are
// wrapped for the core usage service.
func (s *QoderUsage) fetchQoderQuotaUsageWithProvider(ctx context.Context, provider *providercore.Record, sessionSource *QoderTokenProvider,
) (*qoder.QuotaUsageResponse, error) {
	session, err := sessionSource.GetSession(ctx, provider)
	if err != nil {
		return nil, err
	}
	site, err := qoder.ParseSite(provider.GetCredential("site"))
	if err != nil {
		return nil, err
	}
	profile, err := qoder.ProfileForSite(site)
	if err != nil {
		return nil, err
	}
	mode, err := qoder.ParseRefreshMode(provider.GetCredential("refresh_mode"))
	if err != nil {
		return nil, err
	}
	if mode == qoder.RefreshModeDeviceToken && strings.TrimSpace(provider.GetCredential("pat")) == "" {
		client := qoder.NewOAuthClientForProfile(profile, nil)
		client.Doer = QoderRequestDoer(provider, s.Transport, s.Profiles)
		return client.GetUsagePresentation(ctx, session.Identity.SecurityOauthToken)
	}
	logicalPath := qoder.QuotaUsagePath
	// CN quota queries use cached organization identity as orgId; Global queries
	// retain the plain quota path, so site-specific routing does not leak that
	// query field.
	if profile.Site == qoder.SiteCN {
		query := url.Values{}
		if organizationID := strings.TrimSpace(session.Identity.OrganizationID); organizationID != "" {
			query.Set("orgId", organizationID)
		}
		if encoded := query.Encode(); encoded != "" {
			logicalPath += "?" + encoded
		}
	}
	doer := QoderRequestDoer(provider, s.Transport, s.Profiles)
	var usage qoder.QuotaUsageResponse
	client := qoder.NewClientForProfile(profile)
	request := client.BearerJSONRequestContextWithDoer
	// Imported legacy CN sessions use their bearer token, while supported PAT and
	// CN20 identities take the signed method used by the Global quota route.
	if qoderQuotaUsesSignedAuth(provider, profile.Site) {
		request = client.JSONRequestContextWithDoer
	}
	if err := request(ctx, http.MethodGet, session, logicalPath, nil, nil, doer, &usage); err != nil {
		return nil, fmt.Errorf("qoder: quota usage request: %w", err)
	}
	return &usage, nil
}

// qoderQuotaUsesSignedAuth selects signing for the provider's quota query route.
// Global uses signing, as do CN PAT and QoderCN20 records. Remaining CN records
// use Bearer authentication, retaining compatibility with imported legacy direct
// COSY sessions.
func qoderQuotaUsesSignedAuth(provider *providercore.Record, site qoder.Site) bool {
	if site != qoder.SiteCN {
		return true
	}
	if strings.TrimSpace(provider.GetCredential("pat")) != "" {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(provider.GetCredential("refresh_mode")), qoder.RefreshModeQoderCN20)
}

// isQoderAuthenticationError recognizes typed API errors eligible for
// PAT-session rebuild.
// It follows wrapped errors and accepts HTTP 401 or 403. Text-only status
// messages and other statuses return false, keeping query retry tied to a
// typed authentication response.
func isQoderAuthenticationError(err error) bool {
	var apiErr *qoder.APIError
	return errors.As(err, &apiErr) && (apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusForbidden)
}

// buildQoderUsageInfoAt creates active display usage timestamped with the
// supplied instant.
// Quota conversion uses that same time for its last-update field. A nil response
// still produces an active UsageInfo whose QoderQuota is nil.
func buildQoderUsageInfoAt(resp *qoder.QuotaUsageResponse, now time.Time) *providercore.UsageInfo {
	return &providercore.UsageInfo{
		Source:     "active",
		UpdatedAt:  &now,
		QoderQuota: qoderQuotaInfoFromResponse(resp, now, false),
	}
}

// qoderQuotaInfoFromResponse converts a site response to a timestamped quota
// display.
// Nil responses return nil. Positive expiry values at least one trillion are
// interpreted as milliseconds; smaller positive values are seconds, and nonpositive
// expiry leaves the deadline absent.
//
// Progress aliases select the first nonnil value, preserving explicit zero-valued
// objects over later alternatives. Add-on and organization buckets each normalize
// with cap-based totals, while the supplied fromSnapshot flag marks the display
// source independently of response status. New display fields keep update time
// and normalized expiry together for downstream countdown calculations.
func qoderQuotaInfoFromResponse(resp *qoder.QuotaUsageResponse, updatedAt time.Time, fromSnapshot bool) *providercore.QoderQuotaInfo {
	if resp == nil {
		return nil
	}
	var expiresAt *time.Time
	if resp.ExpiresAt > 0 {
		rawExpiresAt := int64(resp.ExpiresAt)
		var t time.Time
		// Stored quota responses can carry second or millisecond epochs. This
		// magnitude
		// split keeps both accepted forms usable for the display expiry and
		// countdown.
		if rawExpiresAt >= 1_000_000_000_000 {
			t = time.UnixMilli(rawExpiresAt)
		} else {
			t = time.Unix(rawExpiresAt, 0)
		}
		expiresAt = &t
	}
	quota := &providercore.QoderQuotaInfo{
		DisplayMode:          resp.DisplayMode,
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
		SnapshotFromProvider: fromSnapshot,
	}
	if resp.Enterprise != nil {
		quota.Enterprise = &providercore.QoderEnterpriseUsage{OpenMode: resp.Enterprise.OpenMode, DetailURL: resp.Enterprise.DetailURL, FallbackURL: resp.Enterprise.FallbackURL}
	}
	for _, item := range resp.Dedicated {
		progress := providercore.QoderQuotaProgress{Total: item.Total, Used: item.Used, Remaining: item.Remaining, Percentage: qoder.NormalizeQuotaPercentage(item.Percentage), Unit: item.Unit, Available: item.Available}
		packageInfo := providercore.QoderDedicatedQuota{QoderQuotaProgress: progress, ID: item.ID, Name: item.Name, Description: item.Description, Status: item.Status}
		if item.ExpiresAt > 0 {
			expiry := time.Unix(int64(item.ExpiresAt), 0)
			if item.ExpiresAt >= 1_000_000_000_000 {
				expiry = time.UnixMilli(int64(item.ExpiresAt))
			}
			packageInfo.ExpiresAt = &expiry
		}
		for _, label := range item.Labels {
			packageInfo.Labels = append(packageInfo.Labels, providercore.QoderQuotaLabel{Dimension: label.Dimension, Value: label.Value})
		}
		quota.Dedicated = append(quota.Dedicated, packageInfo)
	}
	quota.UserQuota = qoderQuotaProgressFromRaw(resp.UserQuota, true)
	// A present zero-valued bucket is authoritative. Prefer each canonical object
	// before legacy aliases so a later bucket cannot overwrite that explicit
	// observation.
	quota.AddOnQuota = qoderQuotaProgressFromRaw(firstNonNilQoderQuotaProgress(resp.AddOnQuota, resp.AddOnQuotaSnake), true)
	quota.OrgResourcePackage = qoderQuotaProgressFromRaw(firstNonNilQoderQuotaProgress(
		resp.OrgResourcePackage,
		resp.OrgResourcePkgSnake,
		resp.SharedQuota,
		resp.SharedQuotaSnake,
	), true)
	return quota
}

// firstNonNilQoderQuotaProgress returns the first supplied progress object
// that is present.
// A zero-valued object still wins over later aliases. All-nil input returns nil,
// allowing the display converter to leave that quota bucket absent.
func firstNonNilQoderQuotaProgress(values ...*qoder.QuotaProgress) *qoder.QuotaProgress {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

// buildQoderDegradedUsageAt combines a query failure with any last known quota
// snapshot.
// It timestamps the failure and formats its error even when err is nil.
// Typed API statuses map 401/403 to reauthorization and 429 to rate limiting;
// other typed statuses map to the network category.
//
// Remaining errors use the existing diagnostic string hints for those same
// categories. A matching account snapshot is attached with its source marker,
// so the caller can show stale quota alongside the current failure. Snapshot
// decode failures leave only the failure display and do not alter stored Extra.
func buildQoderDegradedUsageAt(err error, provider *providercore.Record, now time.Time) *providercore.UsageInfo {
	info := &providercore.UsageInfo{
		UpdatedAt:  &now,
		QoderQuota: provider.SavedQoderQuota(),
		Error:      fmt.Sprintf("usage API error: %v", err),
	}
	// Prefer typed status information over string hints so wrapped authentication
	// and rate-limit failures retain their category beside a stale snapshot
	// display.
	if err != nil {
		if apiErr, ok := errors.AsType[*qoder.APIError](err); ok {
			switch apiErr.StatusCode {
			case http.StatusUnauthorized, http.StatusForbidden:
				info.ErrorCode = providercore.ErrorCodeUnauthenticated
				info.NeedsReauth = true
			case http.StatusTooManyRequests:
				info.ErrorCode = providercore.ErrorCodeRateLimited
			default:
				info.ErrorCode = providercore.ErrorCodeNetworkError
			}
			return info
		}
		errStr := err.Error()
		switch {
		case strings.Contains(errStr, "status 401") || strings.Contains(errStr, "status 403"):
			info.ErrorCode = providercore.ErrorCodeUnauthenticated
			info.NeedsReauth = true
		case strings.Contains(errStr, "status 429"):
			info.ErrorCode = providercore.ErrorCodeRateLimited
		case strings.Contains(errStr, "request:"):
			info.ErrorCode = providercore.ErrorCodeNetworkError
		default:
			info.ErrorCode = providercore.ErrorCodeNetworkError
		}
	}
	return info
}
