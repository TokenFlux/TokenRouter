package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/pkg/qoder"
	"github.com/BrandonVee/TokenRouter/internal/pkg/tlsfingerprint"
)

type accountUsageCodexProbeRepo struct {
	stubOpenAIAccountRepo
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
		stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: []Account{{
			ID:          1,
			Platform:    PlatformQoder,
			Type:        AccountTypeCosy,
			Credentials: qoderUsageCredentials("sec-token"),
		}}},
		updateExtraCh: make(chan map[string]any, 1),
	}
	svc := &AccountUsageService{
		accountRepo:  repo,
		cache:        NewUsageCache(),
		httpUpstream: upstream,
	}

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
		if updates[qoderQuotaSnapshotExtraKey] == nil {
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
	credentials["quota_key"] = "monthly"
	credentials["organization_id"] = "org-cn"
	repo := &accountUsageCodexProbeRepo{
		stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: []Account{{
			ID:          2,
			Platform:    PlatformQoder,
			Type:        AccountTypeCosy,
			Credentials: credentials,
		}}},
	}
	svc := &AccountUsageService{accountRepo: repo, cache: NewUsageCache(), httpUpstream: upstream}

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
	if upstream.req.URL.Query().Get("orgId") != "org-cn" || upstream.req.URL.Query().Get("quotaKey") != "monthly" {
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
		stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: []Account{{
			ID:       5,
			Platform: PlatformQoder,
			Type:     AccountTypeCosy,
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
	svc := &AccountUsageService{accountRepo: repo, cache: NewUsageCache(), httpUpstream: upstream}

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
		stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: []Account{{
			ID:       6,
			Platform: PlatformQoder,
			Type:     AccountTypeCosy,
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
	svc := &AccountUsageService{accountRepo: repo, cache: NewUsageCache(), httpUpstream: upstream}

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
	provider := NewQoderTokenProvider()
	exchangeCalls := 0
	provider.exchangeCNPAT = func(_ context.Context, _ string, _ *qoder.MachineIdentity) (*qoder.AuthIdentity, time.Time, error) {
		exchangeCalls++
		return &qoder.AuthIdentity{
			UID:                "uid-cn",
			AID:                "uid-cn",
			OrganizationID:     "org-cn",
			SecurityOauthToken: fmt.Sprintf("cosy-cn-%d", exchangeCalls),
			RefreshToken:       "refresh-cn",
		}, time.Now().Add(time.Hour), nil
	}
	account := Account{
		ID:       7,
		Platform: PlatformQoder,
		Type:     AccountTypeCosy,
		Credentials: map[string]any{
			"site":          "cn",
			"pat":           "cn-pat",
			"machine_id":    "machine-cn",
			"machine_token": "machine-token-cn",
			"machine_type":  "5",
		},
	}
	repo := &accountUsageCodexProbeRepo{
		stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: []Account{account}},
	}
	svc := &AccountUsageService{
		accountRepo:          repo,
		cache:                NewUsageCache(),
		httpUpstream:         upstream,
		qoderSessionProvider: provider,
	}

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

func TestIsQoderAuthenticationError(t *testing.T) {
	for _, statusCode := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		err := fmt.Errorf("wrapped: %w", &qoder.APIError{StatusCode: statusCode})
		if !isQoderAuthenticationError(err) {
			t.Fatalf("status %d should be treated as authentication failure", statusCode)
		}
	}
	if isQoderAuthenticationError(&qoder.APIError{StatusCode: http.StatusInternalServerError}) {
		t.Fatal("status 500 should not trigger PAT session rebuild")
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
		stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: []Account{{
			ID:          4,
			Platform:    PlatformQoder,
			Type:        AccountTypeCosy,
			Credentials: qoderUsageCredentials("sec-token"),
		}}},
	}
	svc := &AccountUsageService{accountRepo: repo, cache: NewUsageCache(), httpUpstream: upstream}

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
		stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: []Account{{
			ID:          2,
			Platform:    PlatformQoder,
			Type:        AccountTypeCosy,
			Credentials: qoderUsageCredentials("sec-token"),
		}}},
		updateExtraCh: make(chan map[string]any, 1),
		rateLimitCh:   make(chan time.Time, 1),
	}
	svc := &AccountUsageService{accountRepo: repo, cache: NewUsageCache(), httpUpstream: upstream}

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
		stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: []Account{{
			ID:               12,
			Platform:         PlatformQoder,
			Type:             AccountTypeCosy,
			Credentials:      qoderUsageCredentials("sec-token"),
			RateLimitResetAt: &expiresAt,
		}}},
		rateLimitCh:  make(chan time.Time, 1),
		clearLimitCh: make(chan int64, 1),
	}
	svc := &AccountUsageService{accountRepo: repo, cache: NewUsageCache(), httpUpstream: upstream}

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

func TestQoderQuotaInfoFromResponseInfersAddOnQuotaRemainingFromCap(t *testing.T) {
	t.Parallel()

	quota := qoderQuotaInfoFromResponse(&qoderQuotaUsageResponse{
		UserType:        "teams",
		UsageType:       "credits",
		IsQuotaExceeded: true,
		ExpiresAt:       qoder.FlexibleInt64(time.Now().Add(time.Hour).UnixMilli()),
		UserQuota:       &qoderQuotaProgressRaw{Total: 100, Used: 100, Remaining: 0, Unit: "credits"},
		AddOnQuota: &qoderQuotaProgressRaw{
			Cap:  50,
			Used: 10,
			Unit: "credits",
		},
	}, time.Now(), false)

	if quota.AddOnQuota == nil {
		t.Fatalf("expected add-on quota: %#v", quota)
	}
	if quota.AddOnQuota.Total != 50 {
		t.Fatalf("add-on total = %v, want 50", quota.AddOnQuota.Total)
	}
	if quota.AddOnQuota.Remaining != 40 {
		t.Fatalf("add-on remaining = %v, want 40", quota.AddOnQuota.Remaining)
	}
	if _, limited := qoderQuotaRateLimitResetAt(quota, time.Now()); limited {
		t.Fatalf("add-on cap-derived remaining credits should prevent quota rate limit")
	}
}

func TestQoderQuotaInfoFromResponseInfersOrgResourcePackageTotalFromUsedRemaining(t *testing.T) {
	t.Parallel()

	quota := qoderQuotaInfoFromResponse(&qoderQuotaUsageResponse{
		UserType:        "teams",
		UsageType:       "credits",
		IsQuotaExceeded: true,
		ExpiresAt:       qoder.FlexibleInt64(time.Now().Add(time.Hour).UnixMilli()),
		UserQuota:       &qoderQuotaProgressRaw{Total: 100, Used: 100, Remaining: 0, Unit: "credits"},
		OrgResourcePackage: &qoderQuotaProgressRaw{
			Used:      25,
			Remaining: 75,
			Unit:      "credits",
		},
	}, time.Now(), false)

	if quota.OrgResourcePackage == nil {
		t.Fatalf("expected org resource package quota: %#v", quota)
	}
	if quota.OrgResourcePackage.Total != 100 {
		t.Fatalf("org resource total = %v, want 100", quota.OrgResourcePackage.Total)
	}
	if capacity := qoderQuotaTotalCapacity(quota); capacity != 200 {
		t.Fatalf("total capacity = %v, want 200", capacity)
	}
	if remaining, ok := qoderQuotaTotalRemaining(quota); !ok || remaining != 75 {
		t.Fatalf("total remaining = (%v, %v), want (75, true)", remaining, ok)
	}
	if _, limited := qoderQuotaRateLimitResetAt(quota, time.Now()); limited {
		t.Fatalf("org resource remaining credits should prevent quota rate limit")
	}
}

func TestQoderQuotaProgressFromJSONInfersOnlyMissingRemaining(t *testing.T) {
	t.Parallel()

	var explicit qoderQuotaUsageResponse
	if err := json.Unmarshal([]byte(`{
		"userType":"teams",
		"isQuotaExceeded":true,
		"expiresAt":4102444800000,
		"userQuota":{"total":100,"used":50,"remaining":0,"percentage":0,"unit":"credits"}
	}`), &explicit); err != nil {
		t.Fatalf("unmarshal explicit quota response: %v", err)
	}
	quota := qoderQuotaInfoFromResponse(&explicit, time.Now(), false)
	if quota == nil || quota.UserQuota == nil {
		t.Fatalf("expected user quota: %#v", quota)
	}
	if quota.UserQuota.Remaining != 0 {
		t.Fatalf("explicit remaining=0 must not be inferred to positive balance, got %v", quota.UserQuota.Remaining)
	}
	if quota.UserQuota.Percentage != 0 {
		t.Fatalf("explicit percentage=0 must be preserved, got %v", quota.UserQuota.Percentage)
	}
	if _, limited := qoderQuotaRateLimitResetAt(quota, time.Now()); !limited {
		t.Fatalf("explicit zero remaining with exceeded quota should set quota rate limit")
	}

	var missing qoderQuotaUsageResponse
	if err := json.Unmarshal([]byte(`{
		"userType":"teams",
		"isQuotaExceeded":false,
		"expiresAt":4102444800000,
		"userQuota":{"total":100,"used":40,"unit":"credits"}
	}`), &missing); err != nil {
		t.Fatalf("unmarshal missing quota response: %v", err)
	}
	missingQuota := qoderQuotaInfoFromResponse(&missing, time.Now(), false)
	if missingQuota == nil || missingQuota.UserQuota == nil {
		t.Fatalf("expected missing user quota: %#v", missingQuota)
	}
	if missingQuota.UserQuota.Remaining != 60 {
		t.Fatalf("missing remaining should be inferred from total-used, got %v", missingQuota.UserQuota.Remaining)
	}
	if missingQuota.UserQuota.Percentage != 40 {
		t.Fatalf("missing percentage should be inferred from used/total, got %v", missingQuota.UserQuota.Percentage)
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
		stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: []Account{{
			ID:          9,
			Platform:    PlatformQoder,
			Type:        AccountTypeCosy,
			Credentials: qoderUsageCredentials("sec-token"),
		}}},
		rateLimitCh:  make(chan time.Time, 1),
		clearLimitCh: make(chan int64, 1),
	}
	svc := &AccountUsageService{accountRepo: repo, cache: NewUsageCache(), httpUpstream: upstream}

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
		stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: []Account{{
			ID:               7,
			Platform:         PlatformQoder,
			Type:             AccountTypeCosy,
			Credentials:      qoderUsageCredentials("sec-token"),
			RateLimitResetAt: &expiresAt,
		}}},
		rateLimitCh:  make(chan time.Time, 1),
		clearLimitCh: make(chan int64, 1),
	}
	svc := &AccountUsageService{accountRepo: repo, cache: NewUsageCache(), httpUpstream: upstream}

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
		stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: []Account{{
			ID:               11,
			Platform:         PlatformQoder,
			Type:             AccountTypeCosy,
			Credentials:      qoderUsageCredentials("sec-token"),
			RateLimitResetAt: &expiresAt,
		}}},
		clearLimitCh: make(chan int64, 1),
	}
	cache := NewUsageCache()
	cache.qoderCache.Store(int64(11), &qoderUsageCache{
		usageInfo: &UsageInfo{
			Error:     "usage API error: temporary network error",
			ErrorCode: errorCodeNetworkError,
			QoderQuota: &QoderQuotaInfo{
				UserType:        "teams",
				IsQuotaExceeded: true,
				ExpiresAt:       &expiresAt,
				UserQuota:       &QoderQuotaProgress{Total: 100, Used: 100, Remaining: 0, Percentage: 100, Unit: "credits"},
			},
		},
		timestamp: time.Now(),
	})
	svc := &AccountUsageService{accountRepo: repo, cache: cache, httpUpstream: upstream}

	usage, err := svc.GetUsage(context.Background(), 11)

	if err != nil {
		t.Fatalf("GetUsage() error = %v", err)
	}
	if usage == nil || usage.ErrorCode != errorCodeNetworkError {
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
		stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: []Account{{
			ID:               10,
			Platform:         PlatformQoder,
			Type:             AccountTypeCosy,
			Credentials:      qoderUsageCredentials("sec-token"),
			RateLimitResetAt: &expiresAt,
			OverloadUntil:    &overloadUntil,
		}}},
		clearLimitCh: make(chan int64, 1),
	}
	svc := &AccountUsageService{accountRepo: repo, cache: NewUsageCache(), httpUpstream: upstream}

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
		stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: []Account{{
			ID:               8,
			Platform:         PlatformQoder,
			Type:             AccountTypeCosy,
			Credentials:      qoderUsageCredentials("sec-token"),
			RateLimitResetAt: &resetAt,
		}}},
		clearLimitCh: make(chan int64, 1),
	}
	svc := &AccountUsageService{accountRepo: repo, cache: NewUsageCache(), httpUpstream: upstream}

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
		stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: []Account{{
			ID:          3,
			Platform:    PlatformQoder,
			Type:        AccountTypeCosy,
			Credentials: qoderUsageCredentials("sec-token"),
		}}},
		rateLimitCh: make(chan time.Time, 1),
	}
	svc := &AccountUsageService{accountRepo: repo, cache: NewUsageCache(), httpUpstream: upstream}

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
		stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: []Account{{
			ID:          1,
			Platform:    PlatformQoder,
			Type:        AccountTypeCosy,
			Credentials: qoderUsageCredentials("sec-token"),
			Extra: map[string]any{
				qoderQuotaUpdatedAtExtraKey: updatedAt,
				qoderQuotaSnapshotExtraKey: map[string]any{
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
	svc := &AccountUsageService{
		accountRepo:  repo,
		cache:        NewUsageCache(),
		httpUpstream: upstream,
	}

	usage, err := svc.GetUsage(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetUsage() error = %v", err)
	}
	if usage.ErrorCode != errorCodeRateLimited {
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
		stubOpenAIAccountRepo: stubOpenAIAccountRepo{accounts: []Account{{
			ID:           1,
			Platform:     PlatformQoder,
			Type:         AccountTypeCosy,
			Status:       StatusError,
			ErrorMessage: "unauthenticated",
			Credentials:  qoderUsageCredentials("sec-token"),
		}}},
		clearErrorCh: make(chan int64, 1),
	}
	svc := &AccountUsageService{
		accountRepo:  repo,
		cache:        NewUsageCache(),
		httpUpstream: upstream,
	}

	usage, err := svc.GetUsage(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetUsage() error = %v", err)
	}
	if usage == nil || usage.ErrorCode != errorCodeUnauthenticated {
		t.Fatalf("expected degraded unauthenticated usage, got %#v", usage)
	}
	select {
	case id := <-repo.clearErrorCh:
		t.Fatalf("ClearError(%d) called for degraded usage", id)
	default:
	}
}

func TestShouldRefreshOpenAICodexSnapshot(t *testing.T) {
	t.Parallel()

	rateLimitedUntil := time.Now().Add(5 * time.Minute)
	now := time.Now()
	usage := &UsageInfo{
		FiveHour: &UsageProgress{Utilization: 0},
		SevenDay: &UsageProgress{Utilization: 0},
	}

	if !shouldRefreshOpenAICodexSnapshot(&Account{RateLimitResetAt: &rateLimitedUntil}, usage, now) {
		t.Fatal("expected rate-limited account to force codex snapshot refresh")
	}

	if shouldRefreshOpenAICodexSnapshot(&Account{}, usage, now) {
		t.Fatal("expected complete non-rate-limited usage to skip codex snapshot refresh")
	}

	if !shouldRefreshOpenAICodexSnapshot(&Account{}, &UsageInfo{FiveHour: nil, SevenDay: &UsageProgress{}}, now) {
		t.Fatal("expected missing 5h snapshot to require refresh")
	}

	staleAt := now.Add(-(openAIProbeCacheTTL + time.Minute)).Format(time.RFC3339)
	if !shouldRefreshOpenAICodexSnapshot(&Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Extra: map[string]any{
			"openai_oauth_responses_websockets_v2_enabled": true,
			"codex_usage_updated_at":                       staleAt,
		},
	}, usage, now) {
		t.Fatal("expected stale ws snapshot to trigger refresh")
	}
}

func TestAccountUsageService_ShouldProbeOpenAICodexSnapshot_ForceBypassesCache(t *testing.T) {
	t.Parallel()

	svc := &AccountUsageService{cache: NewUsageCache()}
	now := time.Now()
	accountID := int64(123)

	if !svc.shouldProbeOpenAICodexSnapshot(accountID, now) {
		t.Fatal("首次探测应该写入缓存并允许执行")
	}
	if svc.shouldProbeOpenAICodexSnapshot(accountID, now.Add(time.Minute)) {
		t.Fatal("缓存有效期内的普通探测应该被跳过")
	}
	if !svc.shouldProbeOpenAICodexSnapshot(accountID, now.Add(2*time.Minute), true) {
		t.Fatal("强制刷新应该绕过探测缓存")
	}
}

func TestAccountUsageService_ProbeOpenAICodexSnapshotUsesHTTPUpstreamTLSProfile(t *testing.T) {
	t.Parallel()

	upstream := &accountUsageHTTPUpstreamStub{}
	svc := &AccountUsageService{
		httpUpstream:        upstream,
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}
	account := &Account{
		ID:          456,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Concurrency: 9,
		Credentials: map[string]any{"access_token": "token"},
		Extra:       map[string]any{"enable_tls_fingerprint": true},
	}

	updates, err := svc.probeOpenAICodexSnapshot(context.Background(), account)
	if err != nil {
		t.Fatalf("probeOpenAICodexSnapshot() error = %v", err)
	}
	if len(updates) == 0 {
		t.Fatal("expected codex usage updates")
	}
	if upstream.tlsProfile == nil {
		t.Fatal("expected non-nil TLS profile")
	}
	if upstream.req == nil || HTTPUpstreamProfileFromContext(upstream.req.Context()) != HTTPUpstreamProfileOpenAI {
		t.Fatal("expected OpenAI upstream profile on probe request")
	}
	if upstream.accountID != account.ID {
		t.Fatalf("accountID = %d, want %d", upstream.accountID, account.ID)
	}
}

func TestAccountUsageService_ProbeOpenAICodexSnapshotSkipsTLSProfileWhenDisabled(t *testing.T) {
	t.Parallel()

	upstream := &accountUsageHTTPUpstreamStub{}
	svc := &AccountUsageService{
		httpUpstream:        upstream,
		tlsFPProfileService: &TLSFingerprintProfileService{},
	}
	account := &Account{
		ID:          789,
		Platform:    PlatformOpenAI,
		Type:        AccountTypeOAuth,
		Credentials: map[string]any{"access_token": "token"},
		Extra:       map[string]any{"enable_tls_fingerprint": false},
	}

	updates, err := svc.probeOpenAICodexSnapshot(context.Background(), account)
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

// TestShouldRefreshOpenAICodexSnapshot_SparkShadowIgnoresWSv2 外审第9轮 P1:spark 影子用量走
// QueryUsage(/wham/usage,与 WSv2 无关),staleness 不得被 WSv2 门控,否则首刷后窗口永久冻结。
func TestShouldRefreshOpenAICodexSnapshot_SparkShadowIgnoresWSv2(t *testing.T) {
	t.Parallel()

	now := time.Now()
	usage := &UsageInfo{
		FiveHour: &UsageProgress{Utilization: 0},
		SevenDay: &UsageProgress{Utilization: 0},
	}
	staleAt := now.Add(-(openAIProbeCacheTTL + time.Minute)).Format(time.RFC3339)
	freshAt := now.Add(-time.Minute).Format(time.RFC3339)
	parentID := int64(7001)

	// 影子无 WSv2,但首刷后窗口已存在;过期 codex_usage_updated_at 必须触发再刷新。
	shadowStale := &Account{
		Platform:        PlatformOpenAI,
		Type:            AccountTypeOAuth,
		ParentAccountID: &parentID,
		QuotaDimension:  QuotaDimensionSpark,
		Extra:           map[string]any{"codex_usage_updated_at": staleAt},
	}
	if !shouldRefreshOpenAICodexSnapshot(shadowStale, usage, now) {
		t.Fatal("expected stale spark shadow (no WSv2) to trigger refresh")
	}

	// 影子时间戳仍新鲜则不刷新，确保 TTL 生效。
	shadowFresh := &Account{
		Platform:        PlatformOpenAI,
		Type:            AccountTypeOAuth,
		ParentAccountID: &parentID,
		QuotaDimension:  QuotaDimensionSpark,
		Extra:           map[string]any{"codex_usage_updated_at": freshAt},
	}
	if shouldRefreshOpenAICodexSnapshot(shadowFresh, usage, now) {
		t.Fatal("expected fresh spark shadow to skip refresh (TTL not elapsed)")
	}

	// 反向对照:普通账号无 WSv2 + 过期时间戳仍不刷新，WSv2 仅门控普通账号的 probe 刷新。
	normalNoWS := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Extra:    map[string]any{"codex_usage_updated_at": staleAt},
	}
	if shouldRefreshOpenAICodexSnapshot(normalNoWS, usage, now) {
		t.Fatal("expected non-WSv2 normal account to skip codex probe refresh")
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

	updates, err := extractOpenAICodexProbeUpdates(&http.Response{StatusCode: http.StatusTooManyRequests, Header: headers})
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
	svc := &AccountUsageService{accountRepo: repo}
	svc.persistOpenAICodexProbeSnapshot(321, map[string]any{
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
	svc := &AccountUsageService{accountRepo: repo}
	account := &Account{
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Extra: map[string]any{
			"codex_5h_used_percent": 1.0,
			"codex_5h_reset_at":     time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second).Format(time.RFC3339),
			"codex_7d_used_percent": 100.0,
			"codex_7d_reset_at":     resetAt.Format(time.RFC3339),
		},
	}

	usage, err := svc.getOpenAIUsage(context.Background(), account, false)
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

func TestBuildCodexUsageProgressFromExtra_ZerosExpiredWindow(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 3, 16, 12, 0, 0, 0, time.UTC)

	t.Run("expired 5h window zeroes utilization", func(t *testing.T) {
		extra := map[string]any{
			"codex_5h_used_percent": 42.0,
			"codex_5h_reset_at":     "2026-03-16T10:00:00Z", // 2h ago
		}
		progress := buildCodexUsageProgressFromExtra(extra, "5h", now)
		if progress == nil {
			t.Fatal("expected non-nil progress")
			return
		}
		if progress.Utilization != 0 {
			t.Fatalf("expected Utilization=0 for expired window, got %v", progress.Utilization)
		}
		if progress.RemainingSeconds != 0 {
			t.Fatalf("expected RemainingSeconds=0, got %v", progress.RemainingSeconds)
		}
	})

	t.Run("active 5h window keeps utilization", func(t *testing.T) {
		resetAt := now.Add(2 * time.Hour).Format(time.RFC3339)
		extra := map[string]any{
			"codex_5h_used_percent": 42.0,
			"codex_5h_reset_at":     resetAt,
		}
		progress := buildCodexUsageProgressFromExtra(extra, "5h", now)
		if progress == nil {
			t.Fatal("expected non-nil progress")
			return
		}
		if progress.Utilization != 42.0 {
			t.Fatalf("expected Utilization=42, got %v", progress.Utilization)
		}
	})

	t.Run("expired 7d window zeroes utilization", func(t *testing.T) {
		extra := map[string]any{
			"codex_7d_used_percent": 88.0,
			"codex_7d_reset_at":     "2026-03-15T00:00:00Z", // yesterday
		}
		progress := buildCodexUsageProgressFromExtra(extra, "7d", now)
		if progress == nil {
			t.Fatal("expected non-nil progress")
			return
		}
		if progress.Utilization != 0 {
			t.Fatalf("expected Utilization=0 for expired 7d window, got %v", progress.Utilization)
		}
	})
}

func TestCodexWindowStatsStart(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	window := 5 * time.Hour
	activeReset := now.Add(2 * time.Hour)

	tests := []struct {
		name     string
		progress *UsageProgress
		want     time.Time
	}{
		{
			name:     "active reset window",
			progress: &UsageProgress{ResetsAt: &activeReset},
			want:     activeReset.Add(-window),
		},
		{
			name:     "missing reset falls back",
			progress: &UsageProgress{},
			want:     now.Add(-window),
		},
		{
			name:     "nil progress falls back",
			progress: nil,
			want:     now.Add(-window),
		},
	}

	expiredReset := now.Add(-time.Minute)
	tests = append(tests, struct {
		name     string
		progress *UsageProgress
		want     time.Time
	}{
		name:     "expired reset falls back",
		progress: &UsageProgress{ResetsAt: &expiredReset},
		want:     now.Add(-window),
	})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := codexWindowStatsStart(tt.progress, window, now); !got.Equal(tt.want) {
				t.Fatalf("codexWindowStatsStart() = %v, want %v", got, tt.want)
			}
		})
	}
}
