package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// Qoder usage queries use the provider's site session while distinguishing
// active quota from cached failure display. These cases exercise
// qoder_usage.go through the core usage service, checking Global/CN signing,
// legacy CN Bearer input, PAT priority and rebuild, force/identity cache
// bypass and normalized credit buckets.
//
// Health assertions use recorded snapshot and deadline channels to distinguish
// quota exhaustion from available add-on or organization credit, overload and
// unrelated cooldowns. Another fixture replaces authentication just before a
// response returns, making stale observation writes fail their identity check.
// The HTTP substitute uses an atomic call index for configured status/body
// sequences but retains request pointers without a mutex; those recorded
// fields still require serial fixture use rather than a blanket thread-safety
// assumption.

// qoderUsageHTTPUpstreamStub supplies indexed fixture responses and retains
// the last request pointer.
type qoderUsageHTTPUpstreamStub struct {
	// req retains the latest caller pointer without synchronization. The call
	// index is atomic, but callers keep request capture and configured sequences
	// serial when reading this field for assertions.
	req *http.Request
	// statusCode and statusCodes supply default and per-call HTTP results. The
	// one-based call counter selects index call-1 when present; after that sequence
	// is exhausted, the default resumes and a selected zero becomes HTTP 200.
	statusCode  int   // default fixture HTTP status
	statusCodes []int // indexed fixture HTTP statuses
	// body and bodies supply default and per-call response text for that same
	// call index. An in-range empty item remains authoritative; calls beyond the
	// configured sequence use body rather than repeating its final item.
	body   string   // default fixture response text
	bodies []string // indexed fixture response texts
	calls  int32    // atomic request sequence counter
}

// qoderObservationIdentityUpstream injects an administrator change after
// response construction and before return.
type qoderObservationIdentityUpstream struct {
	qoderUsageHTTPUpstreamStub // delegated serial response fixture
	// beforeReturn is required and runs after the base result is constructed.
	// The identity test uses it to replace credentials before caller parsing/persistence.
	beforeReturn func()
}

// TestProviderUsageService_QoderUsageFetchesQuotaAndPersistsSnapshot returns a
// signed Global quota response with fractional percentages. It checks
// normalized display values, COSY authorization and a recorded quota snapshot
// update after the query.
func TestProviderUsageService_QoderUsageFetchesQuotaAndPersistsSnapshot(t *testing.T) {
	t.Parallel()

	upstream := &qoderUsageHTTPUpstreamStub{body: `{
		"userType":"teams",
		"usageType":"credits",
		"totalUsagePercentage":0.125,
		"isQuotaExceeded":false,
		"expiresAt":1783875207000,
		"userQuota":{"total":2940,"used":2,"remaining":2938,"percentage":0.01,"unit":"credits"}
	}`}
	repo := &providerUsageCodexProbeRepo{
		usageRecordFixture: usageRecordFixture{providers: []providercore.Record{{
			ID:          1,
			Platform:    capability.PlatformQoder,
			Type:        capability.ProviderTypeCosy,
			Credentials: qoderUsageCredentials("sec-token"),
		}}},
		updateExtraCh: make(chan map[string]any, 1),
	}
	svc := newOAuthUsageFixture(oauthUsageFixtureOptions{
		providerRepo: repo,
		cache:        providercore.NewOAuthUsageCache(),
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
	// Wait for the recorded snapshot callback after usage conversion rather
	// than inferring storage from display data; the bounded channel
	// observation exposes the actual write attempt.
	select {
	case updates := <-repo.updateExtraCh:
		saved := providercore.CloneRecord(&repo.providers[0])
		saved.Extra = updates
		quota := saved.SavedQoderQuota()
		require.NotNil(t, quota)
		require.True(t, quota.SnapshotFromProvider)
		require.Equal(t, usage.QoderQuota.UserQuota, quota.UserQuota)
	case <-time.After(time.Second):
		t.Fatal("expected UpdateExtra call")
	}
}

// TestProviderUsageService_QoderCNQuotaUsesSignedGatewayQueryAndParsesExtensions
// uses CN20 credentials and organization identity. It checks the signed
// gateway path/query, client version and decoded user, prorating, credit URL
// and organization extensions.
func TestProviderUsageService_QoderCNQuotaUsesSignedGatewayQueryAndParsesExtensions(t *testing.T) {
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
	repo := &providerUsageCodexProbeRepo{
		usageRecordFixture: usageRecordFixture{providers: []providercore.Record{{
			ID:          2,
			Platform:    capability.PlatformQoder,
			Type:        capability.ProviderTypeCosy,
			Credentials: credentials,
		}}},
	}
	svc := newOAuthUsageFixture(oauthUsageFixtureOptions{providerRepo: repo, cache: providercore.NewOAuthUsageCache(), httpUpstream: upstream})

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

// TestProviderUsageService_QoderCNLegacyQuotaUsesBearerToken supplies legacy
// CN direct credentials. It checks Bearer authentication, absent signing
// headers, organization query identity and the decoded remaining quota
// returned for that route.
func TestProviderUsageService_QoderCNLegacyQuotaUsesBearerToken(t *testing.T) {
	upstream := &qoderUsageHTTPUpstreamStub{body: `{
		"userType":"teams",
		"usageType":"credits",
		"isQuotaExceeded":false,
		"expiresAt":1783875207000,
		"userQuota":{"total":100,"used":20,"remaining":80,"percentage":20,"unit":"credits"}
	}`}
	credentials := qoderUsageCredentials("legacy-cn-token")
	credentials["site"] = "cn"
	repo := &providerUsageCodexProbeRepo{
		usageRecordFixture: usageRecordFixture{providers: []providercore.Record{{
			ID:          20,
			Platform:    capability.PlatformQoder,
			Type:        capability.ProviderTypeCosy,
			Credentials: credentials,
		}}},
	}
	svc := newOAuthUsageFixture(oauthUsageFixtureOptions{providerRepo: repo, cache: providercore.NewOAuthUsageCache(), httpUpstream: upstream})

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

// TestProviderUsageService_QoderUsagePrefersPATBootstrapOverStoredSecurityToken
// provides PAT beside stale direct access data. It checks a PAT exchange
// precedes quota, two total requests and a signed query using the resulting
// session identity.
func TestProviderUsageService_QoderUsagePrefersPATBootstrapOverStoredSecurityToken(t *testing.T) {
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
	repo := &providerUsageCodexProbeRepo{
		usageRecordFixture: usageRecordFixture{providers: []providercore.Record{{
			ID:       5,
			Platform: capability.PlatformQoder,
			Type:     capability.ProviderTypeCosy,
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
	svc := newOAuthUsageFixture(oauthUsageFixtureOptions{providerRepo: repo, cache: providercore.NewOAuthUsageCache(), httpUpstream: upstream})

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

// TestProviderUsageService_QoderUsageDoesNotReuseStoredTokenWhenPATBootstrapFails
// fails the PAT exchange despite stored access data. It checks degraded
// display, redacted diagnostics and one request, leaving the unused quota
// response unconsumed.
func TestProviderUsageService_QoderUsageDoesNotReuseStoredTokenWhenPATBootstrapFails(t *testing.T) {
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
	repo := &providerUsageCodexProbeRepo{
		usageRecordFixture: usageRecordFixture{providers: []providercore.Record{{
			ID:       6,
			Platform: capability.PlatformQoder,
			Type:     capability.ProviderTypeCosy,
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
	svc := newOAuthUsageFixture(oauthUsageFixtureOptions{providerRepo: repo, cache: providercore.NewOAuthUsageCache(), httpUpstream: upstream})

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

// TestProviderUsageService_QoderCNPATRebuildsSessionAfterAuthenticationFailure
// returns 401 then successful quota for a CN PAT source. It checks two session
// exchanges, two quota queries and the recovered quota result after
// cached-session eviction.
func TestProviderUsageService_QoderCNPATRebuildsSessionAfterAuthenticationFailure(t *testing.T) {
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
	tokenSource := NewQoderTokenProvider(qoder.SessionBuilder{ExchangeCNPAT: func(_ context.Context, _ string, _ *qoder.MachineIdentity) (*qoder.AuthIdentity, time.Time, error) {
		exchangeCalls++
		return &qoder.AuthIdentity{
			UID:                "uid-cn",
			AID:                "uid-cn",
			OrganizationID:     "org-cn",
			SecurityOauthToken: fmt.Sprintf("cosy-cn-%d", exchangeCalls),
			RefreshToken:       "refresh-cn",
		}, time.Now().Add(time.Hour), nil
	}})
	provider := providercore.Record{
		ID:       7,
		Platform: capability.PlatformQoder,
		Type:     capability.ProviderTypeCosy,
		Credentials: map[string]any{
			"site":          "cn",
			"pat":           "cn-pat",
			"machine_id":    "machine-cn",
			"machine_token": "machine-token-cn",
			"machine_type":  "5",
		},
	}
	repo := &providerUsageCodexProbeRepo{
		usageRecordFixture: usageRecordFixture{providers: []providercore.Record{provider}},
	}
	svc := newOAuthUsageFixture(oauthUsageFixtureOptions{
		providerRepo:         repo,
		cache:                providercore.NewOAuthUsageCache(),
		httpUpstream:         upstream,
		qoderSessionProvider: tokenSource,
	})

	usage, err := svc.GetUsage(context.Background(), provider.ID)
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

// TestProviderUsageService_QoderUsageForceBypassesCache serves changing quota
// across two query results. It checks ordinary lookup reuses the first value
// while explicit force performs the second query and returns its later usage.
func TestProviderUsageService_QoderUsageForceBypassesCache(t *testing.T) {
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
	repo := &providerUsageCodexProbeRepo{
		usageRecordFixture: usageRecordFixture{providers: []providercore.Record{{
			ID:          4,
			Platform:    capability.PlatformQoder,
			Type:        capability.ProviderTypeCosy,
			Credentials: qoderUsageCredentials("sec-token"),
		}}},
	}
	svc := newOAuthUsageFixture(oauthUsageFixtureOptions{providerRepo: repo, cache: providercore.NewOAuthUsageCache(), httpUpstream: upstream})

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

// TestProviderUsageService_QoderQuotaExceededSetsRateLimited returns exhausted
// credit with a future millisecond expiry. It checks exceeded display and a
// recorded provider reset whose milliseconds equal the response quota
// deadline.
func TestProviderUsageService_QoderQuotaExceededSetsRateLimited(t *testing.T) {
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
	repo := &providerUsageCodexProbeRepo{
		usageRecordFixture: usageRecordFixture{providers: []providercore.Record{{
			ID:          2,
			Platform:    capability.PlatformQoder,
			Type:        capability.ProviderTypeCosy,
			Credentials: qoderUsageCredentials("sec-token"),
		}}},
		updateExtraCh: make(chan map[string]any, 1),
		rateLimitCh:   make(chan time.Time, 1),
	}
	svc := newOAuthUsageFixture(oauthUsageFixtureOptions{providerRepo: repo, cache: providercore.NewOAuthUsageCache(), httpUpstream: upstream})

	usage, err := svc.GetUsage(context.Background(), 2)
	if err != nil {
		t.Fatalf("GetUsage() error = %v", err)
	}
	if usage.QoderQuota == nil || !usage.QoderQuota.IsQuotaExceeded {
		t.Fatalf("expected exceeded qoder quota: %#v", usage.QoderQuota)
	}
	// Observe the actual limit callback and compare its absolute milliseconds
	// with the response expiry, separating displayed exhaustion from the
	// selected scheduling deadline write.
	select {
	case resetAt := <-repo.rateLimitCh:
		if resetAt.UnixMilli() != expiresAt {
			t.Fatalf("resetAt = %d, want %d", resetAt.UnixMilli(), expiresAt)
		}
	case <-time.After(time.Second):
		t.Fatal("expected SetRateLimited call")
	}
}

// TestProviderUsageService_QoderAddOnQuotaRemainingPreventsUserQuotaRateLimit
// combines exhausted base credit with positive add-on balance. It checks that
// balance, no new limit event and cleanup of the matching stale quota window.
func TestProviderUsageService_QoderAddOnQuotaRemainingPreventsUserQuotaRateLimit(t *testing.T) {
	t.Parallel()

	// Keep the stale window on the payload millisecond instant for recovery
	// matching.
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
	repo := &providerUsageCodexProbeRepo{
		usageRecordFixture: usageRecordFixture{providers: []providercore.Record{{
			ID:               12,
			Platform:         capability.PlatformQoder,
			Type:             capability.ProviderTypeCosy,
			Credentials:      qoderUsageCredentials("sec-token"),
			RateLimitResetAt: &expiresAt,
		}}},
		rateLimitCh:  make(chan time.Time, 1),
		clearLimitCh: make(chan int64, 1),
	}
	svc := newOAuthUsageFixture(oauthUsageFixtureOptions{providerRepo: repo, cache: providercore.NewOAuthUsageCache(), httpUpstream: upstream})

	usage, err := svc.GetUsage(context.Background(), 12)
	if err != nil {
		t.Fatalf("GetUsage() error = %v", err)
	}
	if usage.QoderQuota == nil || usage.QoderQuota.AddOnQuota == nil || usage.QoderQuota.AddOnQuota.Remaining != 40 {
		t.Fatalf("expected add-on quota remaining in qoder usage, got %#v", usage.QoderQuota)
	}
	// Check the queued limit observation after completed usage handling before
	// awaiting cleanup; positive add-on credit must not install another
	// quota-exhaustion window in this fixture.
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

// TestProviderUsageService_QoderQuotaLockedProviderBypassesCachedUsage stores
// exhausted usage before installing its matching provider reset. It checks
// another query returns available credit and clears that observed quota lock
// instead of using cache.
func TestProviderUsageService_QoderQuotaLockedProviderBypassesCachedUsage(t *testing.T) {
	t.Parallel()

	// Use one millisecond fixture instant for initial quota lock and
	// revalidation.
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
	repo := &providerUsageCodexProbeRepo{
		usageRecordFixture: usageRecordFixture{providers: []providercore.Record{{
			ID:          9,
			Platform:    capability.PlatformQoder,
			Type:        capability.ProviderTypeCosy,
			Credentials: qoderUsageCredentials("sec-token"),
		}}},
		rateLimitCh:  make(chan time.Time, 1),
		clearLimitCh: make(chan int64, 1),
	}
	svc := newOAuthUsageFixture(oauthUsageFixtureOptions{providerRepo: repo, cache: providercore.NewOAuthUsageCache(), httpUpstream: upstream})

	first, err := svc.GetUsage(context.Background(), 9)
	if err != nil {
		t.Fatalf("first GetUsage() error = %v", err)
	}
	if first.QoderQuota == nil || !first.QoderQuota.IsQuotaExceeded {
		t.Fatalf("expected cached exceeded quota: %#v", first.QoderQuota)
	}
	// Observe initial exhaustion before changing the fixture provider reset,
	// then query again with that matching window so cache bypass and later
	// recovery follow the intended order.
	select {
	case <-repo.rateLimitCh:
	case <-time.After(time.Second):
		t.Fatal("expected SetRateLimited call")
	}
	// Install the response-matching limit only after its write was observed;
	// this turns the next ordinary lookup into quota-lock revalidation rather
	// than normal cache reuse.
	repo.providers[0].RateLimitResetAt = &expiresAt

	second, err := svc.GetUsage(context.Background(), 9)
	if err != nil {
		t.Fatalf("second GetUsage() error = %v", err)
	}
	if second.QoderQuota == nil || second.QoderQuota.IsQuotaExceeded || second.QoderQuota.UserQuota == nil || second.QoderQuota.UserQuota.Remaining != 50 {
		t.Fatalf("expected refreshed available quota, got %#v", second.QoderQuota)
	}
	if got := atomic.LoadInt32(&upstream.calls); got != 2 {
		t.Fatalf("upstream calls = %d, want 2; quota-locked provider must bypass cached exceeded usage", got)
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

// TestProviderUsageService_QoderQuotaAvailableClearsMatchingQuotaRateLimit
// returns available credit for an existing matching reset. It checks the clear
// event for that provider and no newly installed quota-limit event.
func TestProviderUsageService_QoderQuotaAvailableClearsMatchingQuotaRateLimit(t *testing.T) {
	t.Parallel()

	// Align the fixture reset with the decoded millisecond quota expiry.
	expiresAt := time.Now().Add(time.Hour).Truncate(time.Millisecond)
	upstream := &qoderUsageHTTPUpstreamStub{body: fmt.Sprintf(`{
		"userType":"teams",
		"usageType":"credits",
		"totalUsagePercentage":50,
		"isQuotaExceeded":false,
		"expiresAt":%d,
		"userQuota":{"total":100,"used":50,"remaining":50,"percentage":50,"unit":"credits"}
	}`, expiresAt.UnixMilli())}
	repo := &providerUsageCodexProbeRepo{
		usageRecordFixture: usageRecordFixture{providers: []providercore.Record{{
			ID:               7,
			Platform:         capability.PlatformQoder,
			Type:             capability.ProviderTypeCosy,
			Credentials:      qoderUsageCredentials("sec-token"),
			RateLimitResetAt: &expiresAt,
		}}},
		rateLimitCh:  make(chan time.Time, 1),
		clearLimitCh: make(chan int64, 1),
	}
	svc := newOAuthUsageFixture(oauthUsageFixtureOptions{providerRepo: repo, cache: providercore.NewOAuthUsageCache(), httpUpstream: upstream})

	usage, err := svc.GetUsage(context.Background(), 7)
	if err != nil {
		t.Fatalf("GetUsage() error = %v", err)
	}
	if usage.QoderQuota == nil || usage.QoderQuota.IsQuotaExceeded {
		t.Fatalf("expected available qoder quota: %#v", usage.QoderQuota)
	}
	// Await the actual clear callback before checking no new limit was queued,
	// so available display data alone cannot satisfy the intended
	// health-recovery assertion.
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

// TestProviderUsageService_QoderQuotaLockedProviderKeepsDegradedCache seeds
// cached failure display for the same identity alongside a quota lock. It checks
// the error cache is reused with zero queries and no health recovery event.
func TestProviderUsageService_QoderQuotaLockedProviderKeepsDegradedCache(t *testing.T) {
	t.Parallel()

	// Share millisecond expiry between selected reset and cached quota data.
	expiresAt := time.Now().Add(time.Hour).Truncate(time.Millisecond)
	upstream := &qoderUsageHTTPUpstreamStub{body: fmt.Sprintf(`{
		"userType":"teams",
		"usageType":"credits",
		"totalUsagePercentage":50,
		"isQuotaExceeded":false,
		"expiresAt":%d,
		"userQuota":{"total":100,"used":50,"remaining":50,"percentage":50,"unit":"credits"}
	}`, expiresAt.UnixMilli())}
	repo := &providerUsageCodexProbeRepo{
		usageRecordFixture: usageRecordFixture{providers: []providercore.Record{{
			ID:               11,
			Platform:         capability.PlatformQoder,
			Type:             capability.ProviderTypeCosy,
			Credentials:      qoderUsageCredentials("sec-token"),
			RateLimitResetAt: &expiresAt,
		}}},
		clearLimitCh: make(chan int64, 1),
	}
	cache := providercore.NewOAuthUsageCache()
	// Seed failure display with the same identity and a live cache timestamp;
	// the following query must exercise degraded-cache reuse instead of an
	// identity miss or expired entry.
	cache.StoreQoder(int64(11), &providercore.OAuthQoderUsageCache{
		Identity: providercore.UsageCacheIdentity(&repo.providers[0]),
		UsageInfo: &providercore.UsageInfo{
			Error:     "usage API error: temporary network error",
			ErrorCode: providercore.ErrorCodeNetworkError,
			QoderQuota: &providercore.QoderQuotaInfo{
				UserType:        "teams",
				IsQuotaExceeded: true,
				ExpiresAt:       &expiresAt,
				UserQuota:       &providercore.QoderQuotaProgress{Total: 100, Used: 100, Remaining: 0, Percentage: 100, Unit: "credits"},
			},
		},
		Timestamp: time.Now(),
	})
	svc := newOAuthUsageFixture(oauthUsageFixtureOptions{providerRepo: repo, cache: cache, httpUpstream: upstream})

	usage, err := svc.GetUsage(context.Background(), 11)
	if err != nil {
		t.Fatalf("GetUsage() error = %v", err)
	}
	if usage == nil || usage.ErrorCode != providercore.ErrorCodeNetworkError {
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

// TestProviderUsageService_QoderQuotaAvailableDoesNotClearActiveOverload
// returns available credit while overload remains active. It checks quota
// display remains usable but no quota-limit cleanup event is issued for that
// selected state.
func TestProviderUsageService_QoderQuotaAvailableDoesNotClearActiveOverload(t *testing.T) {
	t.Parallel()

	// Match quota timing at millisecond precision while overload keeps its
	// separate deadline.
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
	repo := &providerUsageCodexProbeRepo{
		usageRecordFixture: usageRecordFixture{providers: []providercore.Record{{
			ID:               10,
			Platform:         capability.PlatformQoder,
			Type:             capability.ProviderTypeCosy,
			Credentials:      qoderUsageCredentials("sec-token"),
			RateLimitResetAt: &expiresAt,
			OverloadUntil:    &overloadUntil,
		}}},
		clearLimitCh: make(chan int64, 1),
	}
	svc := newOAuthUsageFixture(oauthUsageFixtureOptions{providerRepo: repo, cache: providercore.NewOAuthUsageCache(), httpUpstream: upstream})

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

// TestProviderUsageService_QoderQuotaAvailableDoesNotClearUnrelatedRateLimit
// returns available credit with expiry different from the short provider
// reset. It checks display success and no clear event for that unrelated
// cooldown.
func TestProviderUsageService_QoderQuotaAvailableDoesNotClearUnrelatedRateLimit(t *testing.T) {
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
	repo := &providerUsageCodexProbeRepo{
		usageRecordFixture: usageRecordFixture{providers: []providercore.Record{{
			ID:               8,
			Platform:         capability.PlatformQoder,
			Type:             capability.ProviderTypeCosy,
			Credentials:      qoderUsageCredentials("sec-token"),
			RateLimitResetAt: &resetAt,
		}}},
		clearLimitCh: make(chan int64, 1),
	}
	svc := newOAuthUsageFixture(oauthUsageFixtureOptions{providerRepo: repo, cache: providercore.NewOAuthUsageCache(), httpUpstream: upstream})

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

// TestProviderUsageService_QoderPersonalZeroQuotaDoesNotSetRateLimited
// supplies personal zero quota with an exceeded flag and far-future expiry. It
// checks the flag remains display data while no provider quota-limit event is
// recorded.
func TestProviderUsageService_QoderPersonalZeroQuotaDoesNotSetRateLimited(t *testing.T) {
	t.Parallel()

	upstream := &qoderUsageHTTPUpstreamStub{body: `{
		"userType":"personal_standard",
		"usageType":"credits",
		"totalUsagePercentage":0,
		"isQuotaExceeded":true,
		"expiresAt":253402214400000,
		"userQuota":{"total":0,"used":0,"remaining":0,"percentage":0,"unit":"credits"}
	}`}
	repo := &providerUsageCodexProbeRepo{
		usageRecordFixture: usageRecordFixture{providers: []providercore.Record{{
			ID:          3,
			Platform:    capability.PlatformQoder,
			Type:        capability.ProviderTypeCosy,
			Credentials: qoderUsageCredentials("sec-token"),
		}}},
		rateLimitCh: make(chan time.Time, 1),
	}
	svc := newOAuthUsageFixture(oauthUsageFixtureOptions{providerRepo: repo, cache: providercore.NewOAuthUsageCache(), httpUpstream: upstream})

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

// TestProviderUsageService_QoderUsageDegradedUsesLastKnownSnapshot returns 429
// beside a stored quota snapshot. It checks the rate-limited error category
// and the last known credit values marked as originating from provider
// snapshot data.
func TestProviderUsageService_QoderUsageDegradedUsesLastKnownSnapshot(t *testing.T) {
	t.Parallel()

	upstream := &qoderUsageHTTPUpstreamStub{statusCode: http.StatusTooManyRequests, body: `rate limited`}
	updatedAt := time.Now().UTC().Format(time.RFC3339)
	repo := &providerUsageCodexProbeRepo{
		usageRecordFixture: usageRecordFixture{providers: []providercore.Record{{
			ID:          1,
			Platform:    capability.PlatformQoder,
			Type:        capability.ProviderTypeCosy,
			Credentials: qoderUsageCredentials("sec-token"),
			Extra: map[string]any{
				providercore.QoderUsageQuotaUpdatedAtExtraKey: updatedAt,
				providercore.QoderUsageQuotaSnapshotExtraKey: map[string]any{
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
	snapshot, ok := repo.providers[0].Extra[providercore.QoderUsageQuotaSnapshotExtraKey].(map[string]any)
	require.True(t, ok)
	snapshot["account_identity"] = providercore.QoderCatalogIdentity(&repo.providers[0])
	svc := newOAuthUsageFixture(oauthUsageFixtureOptions{
		providerRepo: repo,
		cache:        providercore.NewOAuthUsageCache(),
		httpUpstream: upstream,
	})

	usage, err := svc.GetUsage(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetUsage() error = %v", err)
	}
	if usage.ErrorCode != providercore.ErrorCodeRateLimited {
		t.Fatalf("ErrorCode = %q, want rate_limited", usage.ErrorCode)
	}
	if usage.QoderQuota == nil || !usage.QoderQuota.SnapshotFromProvider {
		t.Fatalf("expected snapshot qoder quota: %#v", usage.QoderQuota)
	}
	if usage.QoderQuota.UserQuota == nil || usage.QoderQuota.UserQuota.Used != 5 {
		t.Fatalf("unexpected snapshot quota: %#v", usage.QoderQuota.UserQuota)
	}
}

// TestQoderDegradedUsageAfterAccountChange checks a stored organization's
// credits after a provider edit and a failed query. Token rotation preserves
// the observation. Switching site, user or organization hides it, as does an
// upgrade from an unbound snapshot written by a previous gateway version.
func TestQoderDegradedUsageAfterAccountChange(t *testing.T) {
	row := &providercore.Record{ID: 1, Platform: "qoder", Type: "cosy", Credentials: qoderUsageCredentials("first")}
	snapshot := map[string]any{
		"account_identity":   providercore.QoderCatalogIdentity(row),
		"dedicated_packages": []any{map[string]any{"id": "org-a-only", "remaining": 48}},
	}
	row.Extra = map[string]any{providercore.QoderUsageQuotaSnapshotExtraKey: snapshot}
	for _, tc := range []struct {
		field     string
		value     string
		wantQuota bool
	}{
		{"security_oauth_token", "rotated", true},
		{"organization_id", "org-b", false},
		{"uid", "another-user", false},
		{"site", "cn", false},
	} {
		t.Run(tc.field, func(t *testing.T) {
			changed, err := providercore.ApplyConfigurationChange(row, row, providercore.ConfigurationChange{
				Fields:          providercore.ConfigCredentials,
				CredentialPatch: map[string]any{tc.field: tc.value},
			})
			require.NoError(t, err)
			view := buildQoderDegradedUsageAt(errors.New("offline"), changed, time.Now())
			require.Equal(t, providercore.ErrorCodeNetworkError, view.ErrorCode)
			if tc.wantQuota {
				require.NotNil(t, view.QoderQuota)
				require.Equal(t, "org-a-only", view.QoderQuota.Dedicated[0].ID)
			} else {
				require.Nil(t, view.QoderQuota)
			}
		})
	}
	delete(snapshot, "account_identity")
	require.Nil(t, buildQoderDegradedUsageAt(errors.New("offline"), row, time.Now()).QoderQuota)
}

// TestProviderUsageService_QoderUsageDegradedDoesNotClearProviderError returns
// 401 for a provider already in error state. It checks unauthenticated
// degraded display and no clear-error event rather than recovery from that
// failed observation.
func TestProviderUsageService_QoderUsageDegradedDoesNotClearProviderError(t *testing.T) {
	t.Parallel()

	upstream := &qoderUsageHTTPUpstreamStub{statusCode: http.StatusUnauthorized, body: `unauthenticated`}
	repo := &providerUsageCodexProbeRepo{
		usageRecordFixture: usageRecordFixture{providers: []providercore.Record{{
			ID:           1,
			Platform:     capability.PlatformQoder,
			Type:         capability.ProviderTypeCosy,
			Status:       providercore.StatusError,
			ErrorMessage: "unauthenticated",
			Credentials:  qoderUsageCredentials("sec-token"),
		}}},
		clearErrorCh: make(chan int64, 1),
	}
	svc := newOAuthUsageFixture(oauthUsageFixtureOptions{
		providerRepo: repo,
		cache:        providercore.NewOAuthUsageCache(),
		httpUpstream: upstream,
	})

	usage, err := svc.GetUsage(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetUsage() error = %v", err)
	}
	if usage == nil || usage.ErrorCode != providercore.ErrorCodeUnauthenticated {
		t.Fatalf("expected degraded unauthenticated usage, got %#v", usage)
	}
	select {
	case id := <-repo.clearErrorCh:
		t.Fatalf("ClearError(%d) called for degraded usage", id)
	default:
	}
}

// TestQoderUsageCacheDoesNotCrossCredentialIdentity changes authentication
// under the same provider ID after initial lookup. It checks the second usage
// value and two supplier queries, preventing reuse of the old identity's
// cached result.
func TestQoderUsageCacheDoesNotCrossCredentialIdentity(t *testing.T) {
	repo := &providerUsageCodexProbeRepo{usageRecordFixture: usageRecordFixture{providers: []providercore.Record{{ID: 919, Platform: capability.PlatformQoder, Type: capability.ProviderTypeCosy, Credentials: qoderUsageCredentials("first")}}}}
	upstream := &qoderUsageHTTPUpstreamStub{bodies: []string{`{"userType":"teams","userQuota":{"total":100,"used":1,"remaining":99}}`, `{"userType":"teams","userQuota":{"total":100,"used":2,"remaining":98}}`}}
	svc := newOAuthUsageFixture(oauthUsageFixtureOptions{providerRepo: repo, cache: providercore.NewOAuthUsageCache(), httpUpstream: upstream})
	first, err := svc.GetUsage(context.Background(), 919)
	require.NoError(t, err)
	require.Equal(t, float64(1), first.QoderQuota.UserQuota.Used)
	// Change authentication after the first result is cached under the same
	// provider ID, so the next lookup distinguishes identity invalidation from
	// a simple new-ID cache miss.
	repo.providers[0].Credentials = qoderUsageCredentials("second")
	second, err := svc.GetUsage(context.Background(), 919)
	require.NoError(t, err)
	require.Equal(t, float64(2), second.QoderQuota.UserQuota.Used)
	require.Equal(t, int32(2), upstream.calls)
}

// TestQoderObservationCannotWriteNewAdministratorIdentity replaces
// authentication just before an exhausted quota response returns. It checks
// zero snapshot/health writes from the old observation after the fixture
// identity comparison fails.
func TestQoderObservationCannotWriteNewAdministratorIdentity(t *testing.T) {
	repo := &qoderObservationIdentityRepo{current: &providercore.Record{ID: 917, Platform: capability.PlatformQoder, Type: capability.ProviderTypeCosy, Status: providercore.StatusActive, Credentials: qoderUsageCredentials("observed")}}
	u := &qoderObservationIdentityUpstream{qoderUsageHTTPUpstreamStub: qoderUsageHTTPUpstreamStub{body: fmt.Sprintf(`{"userType":"teams","usageType":"credits","isQuotaExceeded":true,"isPlanQuotaProrated":false,"expiresAt":%d,"userQuota":{"total":100,"used":100,"remaining":0}}`, time.Now().Add(time.Hour).UnixMilli())}, beforeReturn: func() { repo.current.Credentials = qoderUsageCredentials("administrator") }}
	svc := newOAuthUsageFixture(oauthUsageFixtureOptions{providerRepo: repo, cache: providercore.NewOAuthUsageCache(), httpUpstream: u})
	// Discard the display/error result and inspect write count instead; the
	// before-return administrator mutation targets persistence eligibility,
	// not the returned quota presentation.
	_, _ = svc.GetUsage(context.Background(), 917)
	require.Zero(t, repo.writes, "旧观测不能给新凭据写入额度快照或停调")
}

// TestIsQoderAuthenticationError supplies wrapped 401/403 API errors and a 500
// error. It checks only the authentication statuses select PAT-session
// rebuild, leaving the server failure outside that typed retry classification.
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

// TestQoderQuotaInfoFromResponseInfersAddOnQuotaRemainingFromCap supplies
// add-on cap and used credit with no remaining field. It checks inferred
// total/balance and that positive add-on capacity prevents an otherwise
// exceeded quota limit.
func TestQoderQuotaInfoFromResponseInfersAddOnQuotaRemainingFromCap(t *testing.T) {
	t.Parallel()

	quota := qoderQuotaInfoFromResponse(&qoder.QuotaUsageResponse{
		UserType:        "teams",
		UsageType:       "credits",
		IsQuotaExceeded: true,
		ExpiresAt:       qoder.FlexibleInt64(time.Now().Add(time.Hour).UnixMilli()),
		UserQuota:       &qoder.QuotaProgress{Total: 100, Used: 100, Remaining: 0, Unit: "credits"},
		AddOnQuota: &qoder.QuotaProgress{
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
	if _, limited := providercore.QoderQuotaRateLimitResetAt(quota, time.Now()); limited {
		t.Fatalf("add-on cap-derived remaining credits should prevent quota rate limit")
	}
}

// TestQoderQuotaInfoFromResponseInfersOrgResourcePackageTotalFromUsedRemaining
// supplies organization used/remaining values without total. It checks
// inferred organization capacity, combined quota totals/balance and no
// provider limit while credit remains.
func TestQoderQuotaInfoFromResponseInfersOrgResourcePackageTotalFromUsedRemaining(t *testing.T) {
	t.Parallel()

	quota := qoderQuotaInfoFromResponse(&qoder.QuotaUsageResponse{
		UserType:        "teams",
		UsageType:       "credits",
		IsQuotaExceeded: true,
		ExpiresAt:       qoder.FlexibleInt64(time.Now().Add(time.Hour).UnixMilli()),
		UserQuota:       &qoder.QuotaProgress{Total: 100, Used: 100, Remaining: 0, Unit: "credits"},
		OrgResourcePackage: &qoder.QuotaProgress{
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
	if capacity := providercore.QoderQuotaTotalCapacity(quota); capacity != 200 {
		t.Fatalf("total capacity = %v, want 200", capacity)
	}
	if remaining, ok := providercore.QoderQuotaTotalRemaining(quota); !ok || remaining != 75 {
		t.Fatalf("total remaining = (%v, %v), want (75, true)", remaining, ok)
	}
	if _, limited := providercore.QoderQuotaRateLimitResetAt(quota, time.Now()); limited {
		t.Fatalf("org resource remaining credits should prevent quota rate limit")
	}
}

// TestQoderQuotaProgressFromJSONInfersOnlyMissingRemaining compares explicit
// zero remaining/percentage with omitted fields. It checks zero observations
// stay zero and limit eligibility remains exhausted, while missing values
// derive remaining balance/utilization.
func TestQoderQuotaProgressFromJSONInfersOnlyMissingRemaining(t *testing.T) {
	t.Parallel()

	var explicit qoder.QuotaUsageResponse
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
	if _, limited := providercore.QoderQuotaRateLimitResetAt(quota, time.Now()); !limited {
		t.Fatalf("explicit zero remaining with exceeded quota should set quota rate limit")
	}

	var missing qoder.QuotaUsageResponse
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

// qoderUsageCredentials creates direct-session credentials with the supplied
// security token. Fixed machine, user and organization fields keep these quota
// cases from requiring extra identity discovery. Each call returns a new outer
// map for independent fixture setup.
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

// Do delegates fixture response selection through DoWithTLS with a nil
// profile. Proxy and provider routing inputs are accepted but unused by the
// underlying substitute. The returned body is owned by the quota reader for
// consumption/closing.
func (s *qoderUsageHTTPUpstreamStub) Do(req *http.Request, proxyURL string, providerID int64, providerConcurrency int) (*http.Response, error) {
	return s.DoWithTLS(req, proxyURL, providerID, providerConcurrency, nil)
}

// DoWithTLS retains the request and selects configured response by atomic call
// index. Status/body arrays override their respective defaults independently
// when in range; zero status becomes HTTP 200. Arguments besides request are
// unused, and only the call counter is atomic. The caller consumes/closes the
// newly allocated body.
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

// DoWithTLS constructs the base response then invokes the required
// beforeReturn hook. The hook changes fixture state before caller response
// handling, exposing stale identity writes. Base response/error are returned
// unchanged after that hook; its mutation remains the test owner's
// responsibility.
func (u *qoderObservationIdentityUpstream) DoWithTLS(req *http.Request, proxy string, id int64, n int, p *tlsfingerprint.Profile) (*http.Response, error) {
	resp, err := u.qoderUsageHTTPUpstreamStub.DoWithTLS(req, proxy, id, n, p)
	// Change identity after response construction and before quota-body parsing. Later
	// conditional writes compare their pre-request identity with this administrator replacement.
	u.beforeReturn()
	return resp, err
}

// Do delegates to the identity-interleaving TLS-aware path with no profile. It
// retains the base result while ensuring beforeReturn runs before the quota
// caller can parse the returned body or apply its observed identity.
func (u *qoderObservationIdentityUpstream) Do(req *http.Request, proxy string, id int64, n int) (*http.Response, error) {
	return u.DoWithTLS(req, proxy, id, n, nil)
}

// TestDedicatedCreditsKeepEligibleProviderSchedulable exercises omitted and explicit
// zero balances. The displayed package and the scheduler must use the same
// normalized value, while general credits stay exhausted in both cases.
func TestDedicatedCreditsKeepEligibleProviderSchedulable(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		extra   string
		balance float64
		limited bool
	}{{"", 80, false}, {`,"remaining":0`, 0, true}} {
		raw := fmt.Sprintf(`{"displayMode":"qoder","qoderUsage":{"userId":"user","userType":"teams","usageType":"credits","isQuotaExceeded":true,"isPlanQuotaProrated":false,"expiresAt":%d,"userQuota":{"total":100,"remaining":0},"dedicatedResourcePackages":[{"id":"scoped","available":true,"total":100,"used":20%s}]}}`, now.Add(time.Hour).Unix(), tc.extra)
		response, err := qoder.ParseUsagePresentation([]byte(raw))
		require.NoError(t, err)
		info := qoderQuotaInfoFromResponse(response, now, false)
		require.Equal(t, tc.balance, info.Dedicated[0].Remaining)
		require.Equal(t, 20.0, info.Dedicated[0].Percentage)
		_, limited := providercore.QoderQuotaRateLimitResetAt(info, now)
		require.Equal(t, tc.limited, limited)
	}
}

// TestImportedDeviceModeUsesOpenAPI exercises the same spelling accepted by
// credential validation. Whitespace and case must not route quota queries to
// the legacy Gateway when the saved grant is a desktop device token.
func TestImportedDeviceModeUsesOpenAPI(t *testing.T) {
	u := &qoderUsageHTTPUpstreamStub{body: `{"displayMode":"enterprise","enterpriseUsage":{"openMode":"externalBrowser","detailUrl":"https://qoder.com/usage"}}`}
	creds := qoderUsageCredentials("fixture")
	creds["refresh_mode"] = " DEVICE_TOKEN "
	repo := &providerUsageCodexProbeRepo{usageRecordFixture: usageRecordFixture{providers: []providercore.Record{{ID: 1, Platform: "qoder", Type: "cosy", Credentials: creds}}}, updateExtraCh: make(chan map[string]any, 1)}
	svc := newOAuthUsageFixture(oauthUsageFixtureOptions{providerRepo: repo, cache: providercore.NewOAuthUsageCache(), httpUpstream: u})
	info, err := svc.GetUsage(t.Context(), 1)
	require.NoError(t, err)
	require.Equal(t, "enterprise", info.QoderQuota.DisplayMode)
	require.Equal(t, "/sash/api/v2/me/usage", u.req.URL.Path)
}

// TestExpiredDedicatedPackageAliasesDriveSameScheduling applies both spellings of
// a package's expiration and restrictions. Positive credits in an expired package
// must not prevent an otherwise exhausted account from pausing until cycle end.
func TestExpiredDedicatedPackageAliasesDriveSameScheduling(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	for _, aliases := range [][2]string{{"expires_at", "display_labels"}, {"expiresAt", "displayLabels"}} {
		raw := fmt.Sprintf(`{"displayMode":"qoder","qoderUsage":{"userId":"u","userType":"teams","usageType":"credits","isQuotaExceeded":true,"isPlanQuotaProrated":false,"expiresAt":%d,"dedicatedResourcePackages":[{"id":"pack","available":true,"remaining":8,"%s":%d,"%s":[{"dimension":"model","value":"demo"}]}]}}`, now.Add(time.Hour).Unix(), aliases[0], now.Add(-time.Hour).UnixMilli(), aliases[1])
		response, err := qoder.ParseUsagePresentation([]byte(raw))
		require.NoError(t, err)
		quota := qoderQuotaInfoFromResponse(response, now, false)
		pack := quota.Dedicated[0]
		require.NotNil(t, pack.ExpiresAt)
		require.True(t, pack.ExpiresAt.Equal(now.Add(-time.Hour)))
		require.Equal(t, []providercore.QoderQuotaLabel{{Dimension: "model", Value: "demo"}}, pack.Labels)
		_, limited := providercore.QoderQuotaRateLimitResetAt(quota, now)
		require.True(t, limited)
	}
}

// TestIncompleteDesktopUsagePreservesCooldown feeds an incomplete live observation
// after a verified exhaustion snapshot. The service may return a degraded display
// but must leave both the saved snapshot and its scheduling pause intact.
func TestIncompleteDesktopUsagePreservesCooldown(t *testing.T) {
	expiry := time.Now().Add(time.Hour).Truncate(time.Second)
	credentials := qoderUsageCredentials("fixture")
	credentials["refresh_mode"] = "device_token"
	up := &qoderUsageHTTPUpstreamStub{body: fmt.Sprintf(`{"displayMode":"qoder","qoderUsage":{"userId":"u","userType":"teams","usageType":"credits","expiresAt":%d}}`, expiry.Unix())}
	row := providercore.Record{ID: 1, Platform: "qoder", Type: "cosy", Credentials: credentials, RateLimitResetAt: &expiry}
	row.Extra = map[string]any{providercore.QoderUsageQuotaSnapshotExtraKey: map[string]any{
		"account_identity":  providercore.QoderCatalogIdentity(&row),
		"is_quota_exceeded": true,
		"expires_at":        expiry,
	}}
	repo := &providerUsageCodexProbeRepo{
		usageRecordFixture: usageRecordFixture{providers: []providercore.Record{row}},
		updateExtraCh:      make(chan map[string]any, 1),
		clearLimitCh:       make(chan int64, 1),
		rateLimitCh:        make(chan time.Time, 1),
	}
	svc := newOAuthUsageFixture(oauthUsageFixtureOptions{providerRepo: repo, cache: providercore.NewOAuthUsageCache(), httpUpstream: up})
	info, err := svc.GetUsage(t.Context(), 1)
	require.NoError(t, err)
	require.NotEmpty(t, info.Error)
	require.NotNil(t, info.QoderQuota)
	require.True(t, info.QoderQuota.IsQuotaExceeded)
	require.Empty(t, repo.updateExtraCh)
	require.Empty(t, repo.clearLimitCh)
	require.Empty(t, repo.rateLimitCh)
}
