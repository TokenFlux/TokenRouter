package provider

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	providercore "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	upstreamcore "github.com/TokenFlux/TokenRouter/internal/upstream"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

type accountUsageCodexProbeRepo struct {
	usageRecordFixture
	updateExtraCh chan map[string]any
	rateLimitCh   chan time.Time
	clearLimitCh  chan int64
	clearErrorCh  chan int64
}

func (r *accountUsageCodexProbeRepo) UpdateExtra(_ context.Context, _ int64, updates map[string]any) error {
	if r.updateExtraCh != nil {
		copied := make(map[string]any, len(updates))
		for k, v := range updates {
			copied[k] = v
		}
		r.updateExtraCh <- copied
	}
	return nil
}

func (r *accountUsageCodexProbeRepo) SetRateLimited(_ context.Context, _ int64, resetAt time.Time) error {
	if r.rateLimitCh != nil {
		r.rateLimitCh <- resetAt
	}
	return nil
}

func (r *accountUsageCodexProbeRepo) ClearRateLimit(_ context.Context, id int64) error {
	if r.clearLimitCh != nil {
		r.clearLimitCh <- id
	}
	return nil
}

func (r *accountUsageCodexProbeRepo) ClearError(_ context.Context, id int64) error {
	if r.clearErrorCh != nil {
		r.clearErrorCh <- id
	}
	return nil
}

type accountUsageHTTPUpstreamStub struct {
	tlsProfile *tlsfingerprint.Profile
	req        *http.Request
	proxyURL   string
	accountID  int64
}

func (s *accountUsageHTTPUpstreamStub) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	return s.DoWithTLS(req, proxyURL, accountID, accountConcurrency, nil)
}

func (s *accountUsageHTTPUpstreamStub) DoWithTLS(req *http.Request, proxyURL string, accountID int64, _ int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	s.req = req
	s.proxyURL = proxyURL
	s.accountID = accountID
	s.tlsProfile = profile
	headers := make(http.Header)
	headers.Set("x-codex-primary-used-percent", "7")
	headers.Set("x-codex-primary-window-minutes", "10080")
	headers.Set("x-codex-secondary-used-percent", "3")
	headers.Set("x-codex-secondary-window-minutes", "300")
	return &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     headers,
		Body:       io.NopCloser(strings.NewReader("")),
	}, nil
}

type qoderUsageHTTPUpstreamStub struct {
	req         *http.Request
	statusCode  int
	statusCodes []int
	body        string
	bodies      []string
	calls       int32
}

func qoderUsageCredentials(token string) map[string]any {
	return map[string]any{
		"security_oauth_token": token,
		"machine_id":           "machine-usage",
		"machine_token":        "machine-token-usage",
		"machine_type":         "machine-type-usage",
		"uid":                  "uid-usage",
		"organization_id":      "org-usage",
	}
}

func (s *qoderUsageHTTPUpstreamStub) Do(req *http.Request, proxyURL string, accountID int64, accountConcurrency int) (*http.Response, error) {
	return s.DoWithTLS(req, proxyURL, accountID, accountConcurrency, nil)
}

func (s *qoderUsageHTTPUpstreamStub) DoWithTLS(req *http.Request, _ string, _ int64, _ int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	s.req = req
	call := atomic.AddInt32(&s.calls, 1)
	status := s.statusCode
	if idx := int(call) - 1; idx >= 0 && idx < len(s.statusCodes) {
		status = s.statusCodes[idx]
	}
	if status == 0 {
		status = http.StatusOK
	}
	body := s.body
	if idx := int(call) - 1; idx >= 0 && idx < len(s.bodies) {
		body = s.bodies[idx]
	}
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}

func TestAccountUsageService_QoderUsageFetchesQuotaAndPersistsSnapshot(t *testing.T) {
	t.Parallel()

	upstream := &qoderUsageHTTPUpstreamStub{body: `{
		"userType":"teams",
		"usageType":"credits",
		"totalUsagePercentage":0.125,
		"isQuotaExceeded":false,
		"expiresAt":1783875207000,
		"userQuota":{"total":2940,"used":2,"remaining":2938,"percentage":0.01,"unit":"credits"}
	}`}
	repo := &accountUsageCodexProbeRepo{
		usageRecordFixture: usageRecordFixture{accounts: []accountcore.Record{{
			ID:          1,
			Platform:    capability.PlatformQoder,
			Type:        capability.AccountTypeCosy,
			Credentials: qoderUsageCredentials("sec-token"),
		}}},
		updateExtraCh: make(chan map[string]any, 1),
	}
	svc := newOAuthUsageFixture(oauthUsageFixtureOptions{
		accountRepo:  repo,
		cache:        accountcore.NewOAuthUsageCache(),
		httpUpstream: upstream,
	})

	usage, err := svc.GetUsage(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetUsage() error = %v", err)
	}
	if usage.QoderQuota == nil || usage.QoderQuota.UserQuota == nil {
		t.Fatalf("expected qoder quota in usage: %#v", usage)
	}
	if usage.QoderQuota.UserType != "teams" {
		t.Fatalf("UserType = %q, want teams", usage.QoderQuota.UserType)
	}
	if usage.QoderQuota.UserQuota.Remaining != 2938 {
		t.Fatalf("remaining = %v, want 2938", usage.QoderQuota.UserQuota.Remaining)
	}
	if usage.QoderQuota.TotalUsagePercentage != 12.5 {
		t.Fatalf("total percentage = %v, want 12.5", usage.QoderQuota.TotalUsagePercentage)
	}
	if usage.QoderQuota.UserQuota.Percentage != 1 {
		t.Fatalf("user quota percentage = %v, want 1", usage.QoderQuota.UserQuota.Percentage)
	}
	if got := upstream.req.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer COSY.") {
		t.Fatalf("Authorization = %q, want signed COSY bearer", got)
	}
	select {
	case updates := <-repo.updateExtraCh:
		if updates[accountcore.QoderUsageQuotaSnapshotExtraKey] == nil {
			t.Fatalf("expected qoder quota snapshot update: %#v", updates)
		}
	case <-time.After(time.Second):
		t.Fatal("expected UpdateExtra call")
	}
}

func TestAccountUsageService_QoderCNQuotaUsesSignedGatewayQueryAndParsesExtensions(t *testing.T) {
	upstream := &qoderUsageHTTPUpstreamStub{body: `{
		"userId":"user-cn",
		"userType":"enterprise_standard",
		"usageType":"credits",
		"isPlanQuotaProrated":true,
		"expiresAt":"1783875207000",
		"addCreditsUrl":"https://qoder.com.cn/credits",
		"orgResourcePackage":{"organizationId":"org-cn","cap":100,"used":20,"remaining":80,"available":true}
	}`}
	credentials := qoderUsageCredentials("cosy-cn")
	credentials["site"] = "cn"
	credentials["refresh_mode"] = qoder.RefreshModeQoderCN20
	credentials["organization_id"] = "org-cn"
	repo := &accountUsageCodexProbeRepo{
		usageRecordFixture: usageRecordFixture{accounts: []accountcore.Record{{
			ID:          2,
			Platform:    capability.PlatformQoder,
			Type:        capability.AccountTypeCosy,
			Credentials: credentials,
		}}},
	}
	svc := newOAuthUsageFixture(oauthUsageFixtureOptions{accountRepo: repo, cache: accountcore.NewOAuthUsageCache(), httpUpstream: upstream})

	usage, err := svc.GetUsage(context.Background(), 2)
	if err != nil {
		t.Fatalf("GetUsage() error = %v", err)
	}
	if upstream.req == nil {
		t.Fatal("expected signed quota request")
	}
	if upstream.req.URL.Path != "/algo"+qoder.QuotaUsagePath {
		t.Fatalf("quota path = %q", upstream.req.URL.Path)
	}
	if upstream.req.URL.Query().Get("orgId") != "org-cn" || upstream.req.URL.Query().Has("quotaKey") {
		t.Fatalf("quota query = %q", upstream.req.URL.RawQuery)
	}
	if upstream.req.Header.Get("Cosy-Version") != qoder.CNClientVersion {
		t.Fatalf("Cosy-Version = %q", upstream.req.Header.Get("Cosy-Version"))
	}
	if usage.QoderQuota == nil || usage.QoderQuota.OrgResourcePackage == nil {
		t.Fatalf("expected CN quota extensions: %#v", usage.QoderQuota)
	}
	if usage.QoderQuota.UserID != "user-cn" || !usage.QoderQuota.IsPlanQuotaProrated {
		t.Fatalf("unexpected CN quota metadata: %#v", usage.QoderQuota)
	}
	if usage.QoderQuota.AddCreditsURL != "https://qoder.com.cn/credits" || usage.QoderQuota.OrgResourcePackage.OrganizationID != "org-cn" {
		t.Fatalf("unexpected CN quota extension fields: %#v", usage.QoderQuota)
	}
}

func TestAccountUsageService_QoderCNLegacyQuotaUsesBearerToken(t *testing.T) {
	upstream := &qoderUsageHTTPUpstreamStub{body: `{
		"userType":"teams",
		"usageType":"credits",
		"isQuotaExceeded":false,
		"expiresAt":1783875207000,
		"userQuota":{"total":100,"used":20,"remaining":80,"percentage":20,"unit":"credits"}
	}`}
	credentials := qoderUsageCredentials("legacy-cn-token")
	credentials["site"] = "cn"
	repo := &accountUsageCodexProbeRepo{
		usageRecordFixture: usageRecordFixture{accounts: []accountcore.Record{{
			ID:          20,
			Platform:    capability.PlatformQoder,
			Type:        capability.AccountTypeCosy,
			Credentials: credentials,
		}}},
	}
	svc := newOAuthUsageFixture(oauthUsageFixtureOptions{accountRepo: repo, cache: accountcore.NewOAuthUsageCache(), httpUpstream: upstream})

	usage, err := svc.GetUsage(context.Background(), 20)

	if err != nil {
		t.Fatalf("GetUsage() error = %v", err)
	}
	if upstream.req == nil {
		t.Fatal("expected bearer quota request")
	}
	if got := upstream.req.Header.Get("Authorization"); got != "Bearer legacy-cn-token" {
		t.Fatalf("Authorization = %q, want legacy bearer token", got)
	}
	if upstream.req.Header.Get("Cosy-Key") != "" || upstream.req.Header.Get("Cosy-Date") != "" {
		t.Fatalf("legacy bearer request unexpectedly contains COSY signature headers: %#v", upstream.req.Header)
	}
	if upstream.req.URL.Query().Get("orgId") != "org-usage" || upstream.req.URL.Query().Has("quotaKey") {
		t.Fatalf("quota query = %q", upstream.req.URL.RawQuery)
	}
	if usage.QoderQuota == nil || usage.QoderQuota.UserQuota == nil || usage.QoderQuota.UserQuota.Remaining != 80 {
		t.Fatalf("unexpected qoder quota: %#v", usage.QoderQuota)
	}
}

func TestAccountUsageService_QoderUsagePrefersPATBootstrapOverStoredSecurityToken(t *testing.T) {
	t.Parallel()

	upstream := &qoderUsageHTTPUpstreamStub{bodies: []string{
		`{"id":"user-1","name":"Qoder User","userType":"teams","securityOauthToken":"fresh-token","refreshToken":"refresh-1"}`,
		`{
			"userType":"teams",
			"usageType":"credits",
			"totalUsagePercentage":1,
			"isQuotaExceeded":false,
			"expiresAt":1783875207000,
			"userQuota":{"total":100,"used":1,"remaining":99,"percentage":1,"unit":"credits"}
		}`,
	}}
	repo := &accountUsageCodexProbeRepo{
		usageRecordFixture: usageRecordFixture{accounts: []accountcore.Record{{
			ID:       5,
			Platform: capability.PlatformQoder,
			Type:     capability.AccountTypeCosy,
			Credentials: map[string]any{
				"pat":                  "pat-token",
				"security_oauth_token": "stale-token",
				"machine_id":           "machine-1",
				"machine_token":        "machine-token",
				"machine_type":         "5",
				"organization_id":      "org-test",
			},
		}}},
	}
	svc := newOAuthUsageFixture(oauthUsageFixtureOptions{accountRepo: repo, cache: accountcore.NewOAuthUsageCache(), httpUpstream: upstream})

	usage, err := svc.GetUsage(context.Background(), 5)

	if err != nil {
		t.Fatalf("GetUsage() error = %v", err)
	}
	if usage.QoderQuota == nil || usage.QoderQuota.UserQuota == nil || usage.QoderQuota.UserQuota.Used != 1 {
		t.Fatalf("unexpected qoder quota: %#v", usage.QoderQuota)
	}
	if got := atomic.LoadInt32(&upstream.calls); got != 2 {
		t.Fatalf("upstream calls = %d, want PAT exchange + quota usage", got)
	}
	if got := upstream.req.Header.Get("Authorization"); !strings.HasPrefix(got, "Bearer COSY.") {
		t.Fatalf("quota Authorization = %q, want signed COSY bearer", got)
	}
}

func TestAccountUsageService_QoderUsageDoesNotReuseStoredTokenWhenPATBootstrapFails(t *testing.T) {
	t.Parallel()

	upstream := &qoderUsageHTTPUpstreamStub{
		statusCodes: []int{http.StatusInternalServerError, http.StatusOK},
		bodies: []string{
			`{"message":"pat unavailable","securityOauthToken":"leaked"}`,
			`{
				"userType":"teams",
				"usageType":"credits",
				"totalUsagePercentage":3,
				"isQuotaExceeded":false,
				"expiresAt":1783875207000,
				"userQuota":{"total":100,"used":3,"remaining":97,"percentage":3,"unit":"credits"}
			}`,
		},
	}
	repo := &accountUsageCodexProbeRepo{
		usageRecordFixture: usageRecordFixture{accounts: []accountcore.Record{{
			ID:       6,
			Platform: capability.PlatformQoder,
			Type:     capability.AccountTypeCosy,
			Credentials: map[string]any{
				"pat":                  "pat-token",
				"security_oauth_token": "stored-token",
				"machine_id":           "machine-1",
				"machine_token":        "machine-token",
				"machine_type":         "5",
				"organization_id":      "org-test",
			},
		}}},
	}
	svc := newOAuthUsageFixture(oauthUsageFixtureOptions{accountRepo: repo, cache: accountcore.NewOAuthUsageCache(), httpUpstream: upstream})

	usage, err := svc.GetUsage(context.Background(), 6)

	if err != nil {
		t.Fatalf("GetUsage() error = %v", err)
	}
	if usage.QoderQuota != nil || usage.Error == "" {
		t.Fatalf("expected degraded usage after PAT exchange failure: %#v", usage)
	}
	if strings.Contains(usage.Error, "leaked") {
		t.Fatalf("degraded usage leaked upstream credential: %q", usage.Error)
	}
	if got := atomic.LoadInt32(&upstream.calls); got != 1 {
		t.Fatalf("upstream calls = %d, want only failed PAT exchange", got)
	}
}

func TestAccountUsageService_QoderCNPATRebuildsSessionAfterAuthenticationFailure(t *testing.T) {
	upstream := &qoderUsageHTTPUpstreamStub{
		statusCodes: []int{http.StatusUnauthorized, http.StatusOK},
		bodies: []string{
			`{"code":"401","message":"expired"}`,
			`{
				"userType":"teams",
				"usageType":"credits",
				"totalUsagePercentage":2,
				"isQuotaExceeded":false,
				"expiresAt":1783875207000,
				"userQuota":{"total":100,"used":2,"remaining":98,"percentage":2,"unit":"credits"}
			}`,
		},
	}
	exchangeCalls := 0
	provider := NewQoderTokenProvider(qoder.SessionBuilder{ExchangeCNPAT: func(_ context.Context, _ string, _ *qoder.MachineIdentity) (*qoder.AuthIdentity, time.Time, error) {
		exchangeCalls++
		return &qoder.AuthIdentity{
			UID:                "uid-cn",
			AID:                "uid-cn",
			OrganizationID:     "org-cn",
			SecurityOauthToken: fmt.Sprintf("cosy-cn-%d", exchangeCalls),
			RefreshToken:       "refresh-cn",
		}, time.Now().Add(time.Hour), nil
	}})
	account := accountcore.Record{
		ID:       7,
		Platform: capability.PlatformQoder,
		Type:     capability.AccountTypeCosy,
		Credentials: map[string]any{
			"site":          "cn",
			"pat":           "cn-pat",
			"machine_id":    "machine-cn",
			"machine_token": "machine-token-cn",
			"machine_type":  "5",
		},
	}
	repo := &accountUsageCodexProbeRepo{
		usageRecordFixture: usageRecordFixture{accounts: []accountcore.Record{account}},
	}
	svc := newOAuthUsageFixture(oauthUsageFixtureOptions{
		accountRepo:          repo,
		cache:                accountcore.NewOAuthUsageCache(),
		httpUpstream:         upstream,
		qoderSessionProvider: provider,
	})

	usage, err := svc.GetUsage(context.Background(), account.ID)

	if err != nil {
		t.Fatalf("GetUsage() error = %v", err)
	}
	if usage.QoderQuota == nil || usage.QoderQuota.UserQuota == nil || usage.QoderQuota.UserQuota.Used != 2 {
		t.Fatalf("unexpected qoder quota after PAT session rebuild: %#v", usage)
	}
	if exchangeCalls != 2 {
		t.Fatalf("PAT exchange calls = %d, want 2", exchangeCalls)
	}
	if got := atomic.LoadInt32(&upstream.calls); got != 2 {
		t.Fatalf("quota calls = %d, want 2", got)
	}
}

func TestAccountUsageService_QoderUsageForceBypassesCache(t *testing.T) {
	t.Parallel()

	upstream := &qoderUsageHTTPUpstreamStub{bodies: []string{
		`{
			"userType":"teams",
			"usageType":"credits",
			"totalUsagePercentage":1,
			"isQuotaExceeded":false,
			"expiresAt":1783875207000,
			"userQuota":{"total":100,"used":1,"remaining":99,"percentage":1,"unit":"credits"}
		}`,
		`{
			"userType":"teams",
			"usageType":"credits",
			"totalUsagePercentage":2,
			"isQuotaExceeded":false,
			"expiresAt":1783875207000,
			"userQuota":{"total":100,"used":2,"remaining":98,"percentage":2,"unit":"credits"}
		}`,
	}}
	repo := &accountUsageCodexProbeRepo{
		usageRecordFixture: usageRecordFixture{accounts: []accountcore.Record{{
			ID:          4,
			Platform:    capability.PlatformQoder,
			Type:        capability.AccountTypeCosy,
			Credentials: qoderUsageCredentials("sec-token"),
		}}},
	}
	svc := newOAuthUsageFixture(oauthUsageFixtureOptions{accountRepo: repo, cache: accountcore.NewOAuthUsageCache(), httpUpstream: upstream})

	first, err := svc.GetUsage(context.Background(), 4)
	if err != nil {
		t.Fatalf("first GetUsage() error = %v", err)
	}
	if first.QoderQuota == nil || first.QoderQuota.UserQuota == nil || first.QoderQuota.UserQuota.Used != 1 {
		t.Fatalf("first used = %#v, want 1", first.QoderQuota)
	}

	cached, err := svc.GetUsage(context.Background(), 4)
	if err != nil {
		t.Fatalf("cached GetUsage() error = %v", err)
	}
	if cached.QoderQuota == nil || cached.QoderQuota.UserQuota == nil || cached.QoderQuota.UserQuota.Used != 1 {
		t.Fatalf("cached used = %#v, want cached 1", cached.QoderQuota)
	}
	if got := atomic.LoadInt32(&upstream.calls); got != 1 {
		t.Fatalf("calls after cached request = %d, want 1", got)
	}

	forced, err := svc.GetUsage(context.Background(), 4, true)
	if err != nil {
		t.Fatalf("forced GetUsage() error = %v", err)
	}
	if forced.QoderQuota == nil || forced.QoderQuota.UserQuota == nil || forced.QoderQuota.UserQuota.Used != 2 {
		t.Fatalf("forced used = %#v, want 2", forced.QoderQuota)
	}
	if got := atomic.LoadInt32(&upstream.calls); got != 2 {
		t.Fatalf("calls after forced request = %d, want 2", got)
	}
}

func TestAccountUsageService_QoderQuotaExceededSetsRateLimited(t *testing.T) {
	t.Parallel()

	expiresAt := time.Now().Add(time.Hour).UnixMilli()
	upstream := &qoderUsageHTTPUpstreamStub{body: fmt.Sprintf(`{
		"userType":"teams",
		"usageType":"credits",
		"totalUsagePercentage":100,
		"isQuotaExceeded":true,
		"expiresAt":%d,
		"userQuota":{"total":100,"used":100,"remaining":0,"percentage":100,"unit":"credits"}
	}`, expiresAt)}
	repo := &accountUsageCodexProbeRepo{
		usageRecordFixture: usageRecordFixture{accounts: []accountcore.Record{{
			ID:          2,
			Platform:    capability.PlatformQoder,
			Type:        capability.AccountTypeCosy,
			Credentials: qoderUsageCredentials("sec-token"),
		}}},
		updateExtraCh: make(chan map[string]any, 1),
		rateLimitCh:   make(chan time.Time, 1),
	}
	svc := newOAuthUsageFixture(oauthUsageFixtureOptions{accountRepo: repo, cache: accountcore.NewOAuthUsageCache(), httpUpstream: upstream})

	usage, err := svc.GetUsage(context.Background(), 2)

	if err != nil {
		t.Fatalf("GetUsage() error = %v", err)
	}
	if usage.QoderQuota == nil || !usage.QoderQuota.IsQuotaExceeded {
		t.Fatalf("expected exceeded qoder quota: %#v", usage.QoderQuota)
	}
	select {
	case resetAt := <-repo.rateLimitCh:
		if resetAt.UnixMilli() != expiresAt {
			t.Fatalf("resetAt = %d, want %d", resetAt.UnixMilli(), expiresAt)
		}
	case <-time.After(time.Second):
		t.Fatal("expected SetRateLimited call")
	}
}

func TestAccountUsageService_QoderAddOnQuotaRemainingPreventsUserQuotaRateLimit(t *testing.T) {
	t.Parallel()

	expiresAt := time.Now().Add(time.Hour).Truncate(time.Millisecond)
	upstream := &qoderUsageHTTPUpstreamStub{body: fmt.Sprintf(`{
		"userType":"teams",
		"usageType":"credits",
		"totalUsagePercentage":90,
		"isQuotaExceeded":false,
		"expiresAt":%d,
		"userQuota":{"total":100,"used":100,"remaining":0,"percentage":100,"unit":"credits"},
		"addOnQuota":{"total":50,"used":10,"remaining":40,"percentage":20,"unit":"credits","detailUrl":"https://qoder.example/addon"}
	}`, expiresAt.UnixMilli())}
	repo := &accountUsageCodexProbeRepo{
		usageRecordFixture: usageRecordFixture{accounts: []accountcore.Record{{
			ID:               12,
			Platform:         capability.PlatformQoder,
			Type:             capability.AccountTypeCosy,
			Credentials:      qoderUsageCredentials("sec-token"),
			RateLimitResetAt: &expiresAt,
		}}},
		rateLimitCh:  make(chan time.Time, 1),
		clearLimitCh: make(chan int64, 1),
	}
	svc := newOAuthUsageFixture(oauthUsageFixtureOptions{accountRepo: repo, cache: accountcore.NewOAuthUsageCache(), httpUpstream: upstream})

	usage, err := svc.GetUsage(context.Background(), 12)

	if err != nil {
		t.Fatalf("GetUsage() error = %v", err)
	}
	if usage.QoderQuota == nil || usage.QoderQuota.AddOnQuota == nil || usage.QoderQuota.AddOnQuota.Remaining != 40 {
		t.Fatalf("expected add-on quota remaining in qoder usage, got %#v", usage.QoderQuota)
	}
	select {
	case resetAt := <-repo.rateLimitCh:
		t.Fatalf("unexpected SetRateLimited call while add-on quota remains: %v", resetAt)
	default:
	}
	select {
	case id := <-repo.clearLimitCh:
		if id != 12 {
			t.Fatalf("ClearRateLimit id = %d, want 12", id)
		}
	case <-time.After(time.Second):
		t.Fatal("expected ClearRateLimit call for matching stale quota lock")
	}
}

func TestAccountUsageService_QoderQuotaLockedAccountBypassesCachedUsage(t *testing.T) {
	t.Parallel()

	expiresAt := time.Now().Add(time.Hour).Truncate(time.Millisecond)
	upstream := &qoderUsageHTTPUpstreamStub{bodies: []string{
		fmt.Sprintf(`{
			"userType":"teams",
			"usageType":"credits",
			"totalUsagePercentage":100,
			"isQuotaExceeded":true,
			"expiresAt":%d,
			"userQuota":{"total":100,"used":100,"remaining":0,"percentage":100,"unit":"credits"}
		}`, expiresAt.UnixMilli()),
		fmt.Sprintf(`{
			"userType":"teams",
			"usageType":"credits",
			"totalUsagePercentage":50,
			"isQuotaExceeded":false,
			"expiresAt":%d,
			"userQuota":{"total":100,"used":50,"remaining":50,"percentage":50,"unit":"credits"}
		}`, expiresAt.UnixMilli()),
	}}
	repo := &accountUsageCodexProbeRepo{
		usageRecordFixture: usageRecordFixture{accounts: []accountcore.Record{{
			ID:          9,
			Platform:    capability.PlatformQoder,
			Type:        capability.AccountTypeCosy,
			Credentials: qoderUsageCredentials("sec-token"),
		}}},
		rateLimitCh:  make(chan time.Time, 1),
		clearLimitCh: make(chan int64, 1),
	}
	svc := newOAuthUsageFixture(oauthUsageFixtureOptions{accountRepo: repo, cache: accountcore.NewOAuthUsageCache(), httpUpstream: upstream})

	first, err := svc.GetUsage(context.Background(), 9)
	if err != nil {
		t.Fatalf("first GetUsage() error = %v", err)
	}
	if first.QoderQuota == nil || !first.QoderQuota.IsQuotaExceeded {
		t.Fatalf("expected cached exceeded quota: %#v", first.QoderQuota)
	}
	select {
	case <-repo.rateLimitCh:
	case <-time.After(time.Second):
		t.Fatal("expected SetRateLimited call")
	}
	repo.accounts[0].RateLimitResetAt = &expiresAt

	second, err := svc.GetUsage(context.Background(), 9)
	if err != nil {
		t.Fatalf("second GetUsage() error = %v", err)
	}
	if second.QoderQuota == nil || second.QoderQuota.IsQuotaExceeded || second.QoderQuota.UserQuota == nil || second.QoderQuota.UserQuota.Remaining != 50 {
		t.Fatalf("expected refreshed available quota, got %#v", second.QoderQuota)
	}
	if got := atomic.LoadInt32(&upstream.calls); got != 2 {
		t.Fatalf("upstream calls = %d, want 2; quota-locked account must bypass cached exceeded usage", got)
	}
	select {
	case id := <-repo.clearLimitCh:
		if id != 9 {
			t.Fatalf("ClearRateLimit id = %d, want 9", id)
		}
	case <-time.After(time.Second):
		t.Fatal("expected ClearRateLimit call after refreshed quota became available")
	}
}

func TestAccountUsageService_QoderQuotaAvailableClearsMatchingQuotaRateLimit(t *testing.T) {
	t.Parallel()

	expiresAt := time.Now().Add(time.Hour).Truncate(time.Millisecond)
	upstream := &qoderUsageHTTPUpstreamStub{body: fmt.Sprintf(`{
		"userType":"teams",
		"usageType":"credits",
		"totalUsagePercentage":50,
		"isQuotaExceeded":false,
		"expiresAt":%d,
		"userQuota":{"total":100,"used":50,"remaining":50,"percentage":50,"unit":"credits"}
	}`, expiresAt.UnixMilli())}
	repo := &accountUsageCodexProbeRepo{
		usageRecordFixture: usageRecordFixture{accounts: []accountcore.Record{{
			ID:               7,
			Platform:         capability.PlatformQoder,
			Type:             capability.AccountTypeCosy,
			Credentials:      qoderUsageCredentials("sec-token"),
			RateLimitResetAt: &expiresAt,
		}}},
		rateLimitCh:  make(chan time.Time, 1),
		clearLimitCh: make(chan int64, 1),
	}
	svc := newOAuthUsageFixture(oauthUsageFixtureOptions{accountRepo: repo, cache: accountcore.NewOAuthUsageCache(), httpUpstream: upstream})

	usage, err := svc.GetUsage(context.Background(), 7)

	if err != nil {
		t.Fatalf("GetUsage() error = %v", err)
	}
	if usage.QoderQuota == nil || usage.QoderQuota.IsQuotaExceeded {
		t.Fatalf("expected available qoder quota: %#v", usage.QoderQuota)
	}
	select {
	case id := <-repo.clearLimitCh:
		if id != 7 {
			t.Fatalf("ClearRateLimit id = %d, want 7", id)
		}
	case <-time.After(time.Second):
		t.Fatal("expected ClearRateLimit call")
	}
	select {
	case resetAt := <-repo.rateLimitCh:
		t.Fatalf("unexpected SetRateLimited call: %v", resetAt)
	default:
	}
}

func TestAccountUsageService_QoderQuotaLockedAccountKeepsDegradedCache(t *testing.T) {
	t.Parallel()

	expiresAt := time.Now().Add(time.Hour).Truncate(time.Millisecond)
	upstream := &qoderUsageHTTPUpstreamStub{body: fmt.Sprintf(`{
		"userType":"teams",
		"usageType":"credits",
		"totalUsagePercentage":50,
		"isQuotaExceeded":false,
		"expiresAt":%d,
		"userQuota":{"total":100,"used":50,"remaining":50,"percentage":50,"unit":"credits"}
	}`, expiresAt.UnixMilli())}
	repo := &accountUsageCodexProbeRepo{
		usageRecordFixture: usageRecordFixture{accounts: []accountcore.Record{{
			ID:               11,
			Platform:         capability.PlatformQoder,
			Type:             capability.AccountTypeCosy,
			Credentials:      qoderUsageCredentials("sec-token"),
			RateLimitResetAt: &expiresAt,
		}}},
		clearLimitCh: make(chan int64, 1),
	}
	cache := accountcore.NewOAuthUsageCache()
	cache.StoreQoder(int64(11), &accountcore.OAuthQoderUsageCache{
		Identity: accountcore.UsageCacheIdentity(&repo.accounts[0]),
		UsageInfo: &accountcore.UsageInfo{
			Error:     "usage API error: temporary network error",
			ErrorCode: accountcore.ErrorCodeNetworkError,
			QoderQuota: &accountcore.QoderQuotaInfo{
				UserType:        "teams",
				IsQuotaExceeded: true,
				ExpiresAt:       &expiresAt,
				UserQuota:       &accountcore.QoderQuotaProgress{Total: 100, Used: 100, Remaining: 0, Percentage: 100, Unit: "credits"},
			},
		},
		Timestamp: time.Now(),
	})
	svc := newOAuthUsageFixture(oauthUsageFixtureOptions{accountRepo: repo, cache: cache, httpUpstream: upstream})

	usage, err := svc.GetUsage(context.Background(), 11)

	if err != nil {
		t.Fatalf("GetUsage() error = %v", err)
	}
	if usage == nil || usage.ErrorCode != accountcore.ErrorCodeNetworkError {
		t.Fatalf("expected cached degraded usage, got %#v", usage)
	}
	if got := atomic.LoadInt32(&upstream.calls); got != 0 {
		t.Fatalf("upstream calls = %d, want 0 while degraded cache TTL is valid", got)
	}
	select {
	case id := <-repo.clearLimitCh:
		t.Fatalf("unexpected ClearRateLimit(%d) from degraded cache", id)
	default:
	}
}

func TestAccountUsageService_QoderQuotaAvailableDoesNotClearActiveOverload(t *testing.T) {
	t.Parallel()

	expiresAt := time.Now().Add(time.Hour).Truncate(time.Millisecond)
	overloadUntil := time.Now().Add(5 * time.Minute)
	upstream := &qoderUsageHTTPUpstreamStub{body: fmt.Sprintf(`{
		"userType":"teams",
		"usageType":"credits",
		"totalUsagePercentage":20,
		"isQuotaExceeded":false,
		"expiresAt":%d,
		"userQuota":{"total":100,"used":20,"remaining":80,"percentage":20,"unit":"credits"}
	}`, expiresAt.UnixMilli())}
	repo := &accountUsageCodexProbeRepo{
		usageRecordFixture: usageRecordFixture{accounts: []accountcore.Record{{
			ID:               10,
			Platform:         capability.PlatformQoder,
			Type:             capability.AccountTypeCosy,
			Credentials:      qoderUsageCredentials("sec-token"),
			RateLimitResetAt: &expiresAt,
			OverloadUntil:    &overloadUntil,
		}}},
		clearLimitCh: make(chan int64, 1),
	}
	svc := newOAuthUsageFixture(oauthUsageFixtureOptions{accountRepo: repo, cache: accountcore.NewOAuthUsageCache(), httpUpstream: upstream})

	usage, err := svc.GetUsage(context.Background(), 10)

	if err != nil {
		t.Fatalf("GetUsage() error = %v", err)
	}
	if usage.QoderQuota == nil || usage.QoderQuota.IsQuotaExceeded {
		t.Fatalf("expected available qoder quota: %#v", usage.QoderQuota)
	}
	select {
	case id := <-repo.clearLimitCh:
		t.Fatalf("unexpected ClearRateLimit(%d) while active overload is present", id)
	default:
	}
}

func TestAccountUsageService_QoderQuotaAvailableDoesNotClearUnrelatedRateLimit(t *testing.T) {
	t.Parallel()

	resetAt := time.Now().Add(30 * time.Second)
	expiresAt := time.Now().Add(time.Hour).UnixMilli()
	upstream := &qoderUsageHTTPUpstreamStub{body: fmt.Sprintf(`{
		"userType":"teams",
		"usageType":"credits",
		"totalUsagePercentage":10,
		"isQuotaExceeded":false,
		"expiresAt":%d,
		"userQuota":{"total":100,"used":10,"remaining":90,"percentage":10,"unit":"credits"}
	}`, expiresAt)}
	repo := &accountUsageCodexProbeRepo{
		usageRecordFixture: usageRecordFixture{accounts: []accountcore.Record{{
			ID:               8,
			Platform:         capability.PlatformQoder,
			Type:             capability.AccountTypeCosy,
			Credentials:      qoderUsageCredentials("sec-token"),
			RateLimitResetAt: &resetAt,
		}}},
		clearLimitCh: make(chan int64, 1),
	}
	svc := newOAuthUsageFixture(oauthUsageFixtureOptions{accountRepo: repo, cache: accountcore.NewOAuthUsageCache(), httpUpstream: upstream})

	usage, err := svc.GetUsage(context.Background(), 8)

	if err != nil {
		t.Fatalf("GetUsage() error = %v", err)
	}
	if usage.QoderQuota == nil || usage.QoderQuota.IsQuotaExceeded {
		t.Fatalf("expected available qoder quota: %#v", usage.QoderQuota)
	}
	select {
	case id := <-repo.clearLimitCh:
		t.Fatalf("unexpected ClearRateLimit(%d) for unrelated reset", id)
	default:
	}
}

func TestAccountUsageService_QoderPersonalZeroQuotaDoesNotSetRateLimited(t *testing.T) {
	t.Parallel()

	upstream := &qoderUsageHTTPUpstreamStub{body: `{
		"userType":"personal_standard",
		"usageType":"credits",
		"totalUsagePercentage":0,
		"isQuotaExceeded":true,
		"expiresAt":253402214400000,
		"userQuota":{"total":0,"used":0,"remaining":0,"percentage":0,"unit":"credits"}
	}`}
	repo := &accountUsageCodexProbeRepo{
		usageRecordFixture: usageRecordFixture{accounts: []accountcore.Record{{
			ID:          3,
			Platform:    capability.PlatformQoder,
			Type:        capability.AccountTypeCosy,
			Credentials: qoderUsageCredentials("sec-token"),
		}}},
		rateLimitCh: make(chan time.Time, 1),
	}
	svc := newOAuthUsageFixture(oauthUsageFixtureOptions{accountRepo: repo, cache: accountcore.NewOAuthUsageCache(), httpUpstream: upstream})

	usage, err := svc.GetUsage(context.Background(), 3)

	if err != nil {
		t.Fatalf("GetUsage() error = %v", err)
	}
	if usage.QoderQuota == nil || !usage.QoderQuota.IsQuotaExceeded {
		t.Fatalf("expected display-only exceeded quota: %#v", usage.QoderQuota)
	}
	select {
	case resetAt := <-repo.rateLimitCh:
		t.Fatalf("unexpected SetRateLimited call: %v", resetAt)
	default:
	}
}

func TestAccountUsageService_QoderUsageDegradedUsesLastKnownSnapshot(t *testing.T) {
	t.Parallel()

	upstream := &qoderUsageHTTPUpstreamStub{statusCode: http.StatusTooManyRequests, body: `rate limited`}
	updatedAt := time.Now().UTC().Format(time.RFC3339)
	repo := &accountUsageCodexProbeRepo{
		usageRecordFixture: usageRecordFixture{accounts: []accountcore.Record{{
			ID:          1,
			Platform:    capability.PlatformQoder,
			Type:        capability.AccountTypeCosy,
			Credentials: qoderUsageCredentials("sec-token"),
			Extra: map[string]any{
				accountcore.QoderUsageQuotaUpdatedAtExtraKey: updatedAt,
				accountcore.QoderUsageQuotaSnapshotExtraKey: map[string]any{
					"user_type":              "teams",
					"usage_type":             "credits",
					"total_usage_percentage": 50,
					"is_quota_exceeded":      false,
					"user_quota": map[string]any{
						"total":     10,
						"used":      5,
						"remaining": 5,
						"unit":      "credits",
					},
				},
			},
		}}},
	}
	svc := newOAuthUsageFixture(oauthUsageFixtureOptions{
		accountRepo:  repo,
		cache:        accountcore.NewOAuthUsageCache(),
		httpUpstream: upstream,
	})

	usage, err := svc.GetUsage(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetUsage() error = %v", err)
	}
	if usage.ErrorCode != accountcore.ErrorCodeRateLimited {
		t.Fatalf("ErrorCode = %q, want rate_limited", usage.ErrorCode)
	}
	if usage.QoderQuota == nil || !usage.QoderQuota.SnapshotFromAccount {
		t.Fatalf("expected snapshot qoder quota: %#v", usage.QoderQuota)
	}
	if usage.QoderQuota.UserQuota == nil || usage.QoderQuota.UserQuota.Used != 5 {
		t.Fatalf("unexpected snapshot quota: %#v", usage.QoderQuota.UserQuota)
	}
}

func TestAccountUsageService_QoderUsageDegradedDoesNotClearAccountError(t *testing.T) {
	t.Parallel()

	upstream := &qoderUsageHTTPUpstreamStub{statusCode: http.StatusUnauthorized, body: `unauthenticated`}
	repo := &accountUsageCodexProbeRepo{
		usageRecordFixture: usageRecordFixture{accounts: []accountcore.Record{{
			ID:           1,
			Platform:     capability.PlatformQoder,
			Type:         capability.AccountTypeCosy,
			Status:       accountcore.StatusError,
			ErrorMessage: "unauthenticated",
			Credentials:  qoderUsageCredentials("sec-token"),
		}}},
		clearErrorCh: make(chan int64, 1),
	}
	svc := newOAuthUsageFixture(oauthUsageFixtureOptions{
		accountRepo:  repo,
		cache:        accountcore.NewOAuthUsageCache(),
		httpUpstream: upstream,
	})

	usage, err := svc.GetUsage(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetUsage() error = %v", err)
	}
	if usage == nil || usage.ErrorCode != accountcore.ErrorCodeUnauthenticated {
		t.Fatalf("expected degraded unauthenticated usage, got %#v", usage)
	}
	select {
	case id := <-repo.clearErrorCh:
		t.Fatalf("ClearError(%d) called for degraded usage", id)
	default:
	}
}

func TestAccountUsageService_ShouldProbeOpenAICodexSnapshot_ForceBypassesCache(t *testing.T) {
	t.Parallel()

	svc := newOAuthUsageFixture(oauthUsageFixtureOptions{cache: accountcore.NewOAuthUsageCache()})
	now := time.Now()
	accountID := int64(123)

	if !svc.ShouldProbeOpenAICodexSnapshot(accountID, now) {
		t.Fatal("首次探测应该写入缓存并允许执行")
	}
	if svc.ShouldProbeOpenAICodexSnapshot(accountID, now.Add(time.Minute)) {
		t.Fatal("缓存有效期内的普通探测应该被跳过")
	}
	if !svc.ShouldProbeOpenAICodexSnapshot(accountID, now.Add(2*time.Minute), true) {
		t.Fatal("强制刷新应该绕过探测缓存")
	}
}

func TestAccountUsageService_ProbeOpenAICodexSnapshotUsesHTTPUpstreamTLSProfile(t *testing.T) {
	t.Parallel()

	upstream := &accountUsageHTTPUpstreamStub{}
	svc := newOAuthUsageFixture(oauthUsageFixtureOptions{
		httpUpstream:        upstream,
		tlsFPProfileService: &providercore.TLSProfiles{},
	})
	account := &accountcore.Record{
		ID:          456,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeOAuth,
		Concurrency: 9,
		Credentials: map[string]any{"access_token": "token"},
		Extra:       map[string]any{"enable_tls_fingerprint": true},
	}

	updates, err := svc.ProbeOpenAICodexSnapshot(context.Background(), account)
	if err != nil {
		t.Fatalf("probeOpenAICodexSnapshot() error = %v", err)
	}
	if len(updates) == 0 {
		t.Fatal("expected codex usage updates")
	}
	if upstream.tlsProfile == nil {
		t.Fatal("expected non-nil TLS profile")
	}
	if upstream.req == nil || upstreamcore.HTTPUpstreamProfileFromContext(upstream.req.Context()) != upstreamcore.HTTPUpstreamProfileOpenAI {
		t.Fatal("expected OpenAI upstream profile on probe request")
	}
	if upstream.accountID != account.ID {
		t.Fatalf("accountID = %d, want %d", upstream.accountID, account.ID)
	}
}

func TestAccountUsageService_ProbeOpenAICodexSnapshotSkipsTLSProfileWhenDisabled(t *testing.T) {
	t.Parallel()

	upstream := &accountUsageHTTPUpstreamStub{}
	svc := newOAuthUsageFixture(oauthUsageFixtureOptions{
		httpUpstream:        upstream,
		tlsFPProfileService: &providercore.TLSProfiles{},
	})
	account := &accountcore.Record{
		ID:          789,
		Platform:    capability.PlatformOpenAI,
		Type:        capability.AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "token"},
		Extra:       map[string]any{"enable_tls_fingerprint": false},
	}

	updates, err := svc.ProbeOpenAICodexSnapshot(context.Background(), account)
	if err != nil {
		t.Fatalf("probeOpenAICodexSnapshot() error = %v", err)
	}
	if len(updates) == 0 {
		t.Fatal("expected codex usage updates")
	}
	if upstream.tlsProfile != nil {
		t.Fatal("关闭 TLS 指纹时不应传入 profile")
	}
}

func TestExtractOpenAICodexProbeUpdatesAccepts429WithCodexHeaders(t *testing.T) {
	t.Parallel()

	headers := make(http.Header)
	headers.Set("x-codex-primary-used-percent", "100")
	headers.Set("x-codex-primary-reset-after-seconds", "604800")
	headers.Set("x-codex-primary-window-minutes", "10080")
	headers.Set("x-codex-secondary-used-percent", "100")
	headers.Set("x-codex-secondary-reset-after-seconds", "18000")
	headers.Set("x-codex-secondary-window-minutes", "300")

	updates, err := ExtractOpenAIUsageUpdates(&http.Response{StatusCode: http.StatusTooManyRequests, Header: headers}, time.Now())
	if err != nil {
		t.Fatalf("extractOpenAICodexProbeUpdates() error = %v", err)
	}
	if len(updates) == 0 {
		t.Fatal("expected codex probe updates from 429 headers")
	}
	if got := updates["codex_5h_used_percent"]; got != 100.0 {
		t.Fatalf("codex_5h_used_percent = %v, want 100", got)
	}
	if got := updates["codex_7d_used_percent"]; got != 100.0 {
		t.Fatalf("codex_7d_used_percent = %v, want 100", got)
	}
}

func TestAccountUsageService_PersistOpenAICodexProbeSnapshotOnlyUpdatesExtra(t *testing.T) {
	t.Parallel()

	repo := &accountUsageCodexProbeRepo{
		updateExtraCh: make(chan map[string]any, 1),
		rateLimitCh:   make(chan time.Time, 1),
	}
	svc := newOAuthUsageFixture(oauthUsageFixtureOptions{accountRepo: repo})
	svc.PersistOpenAICodexProbeSnapshot(&accountcore.Record{ID: 321}, map[string]any{
		"codex_7d_used_percent": 100.0,
		"codex_7d_reset_at":     time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second).Format(time.RFC3339),
	})

	select {
	case updates := <-repo.updateExtraCh:
		if got := updates["codex_7d_used_percent"]; got != 100.0 {
			t.Fatalf("codex_7d_used_percent = %v, want 100", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("等待 codex 探测快照写入 extra 超时")
	}

	select {
	case got := <-repo.rateLimitCh:
		t.Fatalf("不应将探测快照写入运行时限流状态: %v", got)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestAccountUsageService_GetOpenAIUsage_DoesNotPromoteCodexExtraToRateLimit(t *testing.T) {
	t.Parallel()

	resetAt := time.Now().Add(6 * 24 * time.Hour).UTC().Truncate(time.Second)
	repo := &accountUsageCodexProbeRepo{
		rateLimitCh: make(chan time.Time, 1),
	}
	svc := newOAuthUsageFixture(oauthUsageFixtureOptions{accountRepo: repo})
	account := &accountcore.Record{
		Platform: capability.PlatformOpenAI,
		Type:     capability.AccountTypeOAuth,
		Extra: map[string]any{
			"codex_5h_used_percent": 1.0,
			"codex_5h_reset_at":     time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second).Format(time.RFC3339),
			"codex_7d_used_percent": 100.0,
			"codex_7d_reset_at":     resetAt.Format(time.RFC3339),
		},
	}

	usage, err := svc.GetOpenAIUsage(context.Background(), account, false)
	if err != nil {
		t.Fatalf("getOpenAIUsage() error = %v", err)
	}
	if usage.SevenDay == nil || usage.SevenDay.Utilization != 100.0 {
		t.Fatalf("预期 7 天用量仍然可见，实际为 %#v", usage.SevenDay)
	}
	if account.RateLimitResetAt != nil {
		t.Fatalf("不应让已耗尽的 codex extra 改写运行时限流状态: %v", account.RateLimitResetAt)
	}
	select {
	case got := <-repo.rateLimitCh:
		t.Fatalf("不应将已耗尽的 codex extra 持久化为运行时限流状态: %v", got)
	case <-time.After(200 * time.Millisecond):
	}
}
