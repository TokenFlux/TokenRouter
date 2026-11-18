package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	egressadapter "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// Qoder session cache entries follow credential identity and generation, so an
// invalidated or older build cannot replace the newest observed provider
// state. These cases exercise qoder_tokens.go through the core cache and site
// builder, checking direct/PAT identity, organization hints, CN expiry,
// transport selection and cache invalidation for one provider ID.
//
// Gated exchanges hold specific generations while callers cancel, mutate
// source credentials or introduce newer versions. Result channels and worker
// joins expose accepted versus discarded builds after those gates open. Cached
// session pointers are compared directly, and version precedence is checked
// separately from UpdatedAt. The cooperative timeout fixture returns after ctx
// cancellation; it does not establish forced termination of an exchanger that
// ignores ctx. The broad concurrency case also counts expected invalidation
// errors among its accepted outcomes rather than requiring every request to
// return a session.

// qoderCenterHTTPUpstreamStub records serial provider request routing and
// returns configurable response data.
type qoderCenterHTTPUpstreamStub struct {
	proxyURL            string // last selected proxy URL
	providerID          int64  // last selected provider ID
	providerConcurrency int    // last selected transport concurrency
	// statusCode supplies the fixture response status. Zero becomes HTTP 200
	// for exchange and organization queries sent through this same transport substitute.
	statusCode int
	// body supplies response text for every endpoint. Empty input selects fixed
	// identity JSON, allowing exchange cases to omit explicit response construction.
	body       string
	profileSet bool // last fingerprint profile presence
	// requests retains the caller request pointers in serial arrival order. Later
	// assertions inspect their endpoint paths; this fixture performs no request cloning.
	requests []*http.Request
}

// qoderInvalidationCache records external key deletion while unused cache
// methods remain unconfigured.
type qoderInvalidationCache struct {
	// AccessTokenCache leaves unconfigured operations on a nil interface. These
	// invalidator cases use DeleteAccessToken below; reaching another method panics.
	providercore.AccessTokenCache          // unused token-cache method substitute
	deletedKeys                   []string // serial attempted deletion keys
}

// TestQoderTokenProviderConcurrent runs fifty workers over repeated cache
// reads and periodic invalidation. It checks nonnil successful sessions and at
// least 80 percent accepted outcomes, including recognized
// generation-invalidation errors.
func TestQoderTokenProviderConcurrent(t *testing.T) {
	tokenSource := &QoderTokenProvider{Core: &qoderSessionState{Sessions: make(map[int64]qoderSessionCacheEntry)}}

	provider := &providercore.Record{
		ID:       12345,
		Platform: capability.PlatformQoder,
		Type:     capability.ProviderTypeCosy,
		Credentials: map[string]any{
			"security_oauth_token": "test_oauth_token",
			"machine_id":           "test_machine_id",
			"uid":                  "test_uid",
		},
	}

	// Fifty workers each issue twenty reads to overlap cache acquisition with
	// periodic generation invalidation. The fixture measures accepted outcomes
	// after all workers finish.
	const numGoroutines = 50
	const numRequestsPerGoroutine = 20

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	ctx := context.Background()
	successCount := sync.Map{}

	// Join the full workload before counting outcomes so invalidation and
	// session errors remain tied to completed worker/request pairs rather than
	// still-running calls.
	for i := range numGoroutines {
		go func(workerID int) {
			defer wg.Done()
			// A thousand-key stride exceeds the per-worker request count,
			// keeping each worker/request outcome distinct in successCount
			// even when invalidation is an accepted result.
			for j := range numRequestsPerGoroutine {
				session, err := tokenSource.GetSession(ctx, provider)
				if err != nil {
					// Generation invalidation is an expected discarded-build outcome in this workload.
					// Count it as accepted so the ratio distinguishes generation protection from unrelated acquisition failures.
					if errors.Is(err, errQoderSessionBuildInvalidated) {
						successCount.Store(workerID*1000+j, true)
						continue
					}
					t.Logf("worker %d request %d failed: %v", workerID, j, err)
					continue
				}
				if session == nil {
					t.Errorf("worker %d request %d: got nil session", workerID, j)
					continue
				}
				successCount.Store(workerID*1000+j, true)

				// Periodic worker/request pairs evict the shared provider generation while
				// other workers continue reading, exposing publication of builds that lost ownership.
				if workerID%3 == 0 && j%5 == 0 {
					tokenSource.Invalidate(provider.ID)
				}
			}
		}(i)
	}

	wg.Wait()

	count := 0
	successCount.Range(func(key, value any) bool {
		count++
		return true
	})

	expectedTotal := numGoroutines * numRequestsPerGoroutine
	successRate := float64(count) / float64(expectedTotal)
	t.Logf("Success rate: %d/%d (%.2f%%)", count, expectedTotal, successRate*100)

	// Require eighty percent accepted outcomes, counting valid sessions and
	// expected invalidation errors together; other failures remain outside
	// this fixture acceptance threshold.
	if successRate < 0.8 {
		t.Errorf("success rate too low: %.2f%%, expected >= 80%%", successRate*100)
	}
}

// TestQoderTokenProviderInvalidateRace runs repeated reads and invalidations
// in two goroutines. It joins both loops and checks completion without a
// panic; read errors are discarded, so the case does not assert per-call
// session success.
func TestQoderTokenProviderInvalidateRace(t *testing.T) {
	tokenSource := &QoderTokenProvider{Core: &qoderSessionState{Sessions: make(map[int64]qoderSessionCacheEntry)}}

	provider := &providercore.Record{
		ID:       999,
		Platform: capability.PlatformQoder,
		Type:     capability.ProviderTypeCosy,
		Credentials: map[string]any{
			"security_oauth_token": "token_999",
			"machine_id":           "machine_999",
			"aid":                  "aid_999",
		},
	}

	ctx := context.Background()
	const numIterations = 1000

	// Run repeated session reads concurrently with invalidations.
	var wg sync.WaitGroup
	wg.Add(2)

	// Discard individual acquisition errors in this stress loop; its
	// observable assertion is joined completion, while the companion goroutine
	// repeatedly evicts the same provider generation.
	go func() {
		defer wg.Done()
		for range numIterations {
			_, _ = tokenSource.GetSession(ctx, provider)
		}
	}()

	// Evict the same provider repeatedly while the companion loop acquires
	// sessions, then let the shared join distinguish completed invalidation
	// work from an unfinished reader.
	go func() {
		defer wg.Done()
		for range numIterations {
			tokenSource.Invalidate(provider.ID)
		}
	}()

	wg.Wait()

	// Completion checks that neither goroutine panics or deadlocks.
	t.Log("GetSession/Invalidate race test completed without panic")
}

// TestQoderTokenProviderBuildsAndCachesDirectSession builds direct-token
// identity with stored organization metadata. It checks user/machine values
// and pointer reuse, then an organization credential change forces a distinct
// cached session.
func TestQoderTokenProviderBuildsAndCachesDirectSession(t *testing.T) {
	tokenSource := NewQoderTokenProvider(qoder.SessionBuilder{})
	provider := &providercore.Record{
		ID:       101,
		Platform: capability.PlatformQoder,
		Type:     capability.ProviderTypeCosy,
		Credentials: map[string]any{
			"security_oauth_token": "dt-token",
			"machine_id":           "machine-1",
			"uid":                  "uid-1",
			"organization_id":      "org-1",
			"organization_name":    "Org 1",
		},
	}

	session1, err := tokenSource.GetSession(context.Background(), provider)
	require.NoError(t, err)
	require.NotNil(t, session1)
	require.Equal(t, "dt-token", session1.Identity.SecurityOauthToken)
	require.Equal(t, "uid-1", session1.Identity.UID)
	require.Equal(t, "uid-1", session1.Identity.AID)
	require.Equal(t, "org-1", session1.Identity.OrganizationID)
	require.Equal(t, "Org 1", session1.Identity.OrganizationName)
	require.Equal(t, "machine-1", session1.Machine.MachineID)

	session2, err := tokenSource.GetSession(context.Background(), provider)
	require.NoError(t, err)
	require.Same(t, session1, session2, "session should be cached per provider credentials")

	provider.Credentials["organization_id"] = "org-2"
	session3, err := tokenSource.GetSession(context.Background(), provider)
	require.NoError(t, err)
	require.NotSame(t, session1, session3)
	require.Equal(t, "org-2", session3.Identity.OrganizationID)
}

// TestQoderTokenProviderRejectsUnsupportedCredentialShape supplies a security
// token without machine identity. It checks the machine_id diagnostic from the
// site builder rather than accepting a partially specified direct session
// input.
func TestQoderTokenProviderRejectsUnsupportedCredentialShape(t *testing.T) {
	tokenSource := NewQoderTokenProvider(qoder.SessionBuilder{})
	provider := &providercore.Record{
		ID:          102,
		Platform:    capability.PlatformQoder,
		Type:        capability.ProviderTypeCosy,
		Credentials: map[string]any{"security_oauth_token": "dt-token"},
	}

	_, err := tokenSource.GetSession(context.Background(), provider)
	require.ErrorContains(t, err, "machine_id")
}

// TestQoderTokenProviderRejectsDirectTokenWithoutIdentity supplies direct
// token and machine_id without user identity. It checks the uid-or-aid
// diagnostic, protecting the user metadata needed by the session builder.
func TestQoderTokenProviderRejectsDirectTokenWithoutIdentity(t *testing.T) {
	tokenSource := NewQoderTokenProvider(qoder.SessionBuilder{})
	provider := &providercore.Record{
		ID:       105,
		Platform: capability.PlatformQoder,
		Type:     capability.ProviderTypeCosy,
		Credentials: map[string]any{
			"security_oauth_token": "dt-token",
			"machine_id":           "machine-1",
		},
	}

	_, err := tokenSource.GetSession(context.Background(), provider)
	require.ErrorContains(t, err, "uid or aid")
}

// TestQoderTokenProviderRejectsCN20RefreshModeOnGlobalSite combines Global
// site with the CN20 mode and otherwise usable identity fields. It checks the
// site/mode diagnostic before that incompatible session form can be returned.
func TestQoderTokenProviderRejectsCN20RefreshModeOnGlobalSite(t *testing.T) {
	tokenSource := NewQoderTokenProvider(qoder.SessionBuilder{})
	provider := &providercore.Record{
		ID:       32,
		Platform: capability.PlatformQoder,
		Type:     capability.ProviderTypeCosy,
		Credentials: map[string]any{
			"site":                 "global",
			"refresh_mode":         qoder.RefreshModeQoderCN20,
			"security_oauth_token": "token",
			"machine_id":           "machine",
			"uid":                  "uid",
		},
	}

	_, err := tokenSource.GetSession(context.Background(), provider)
	require.ErrorContains(t, err, "require cn site")
}

// TestQoderTokenProviderRejectsMachineIDOnlyWithoutReadingLocalAuth supplies
// machine_id with no authentication input. It checks the missing
// PAT/security-token diagnostic rather than a constructed session from that
// isolated machine identity.
func TestQoderTokenProviderRejectsMachineIDOnlyWithoutReadingLocalAuth(t *testing.T) {
	tokenSource := NewQoderTokenProvider(qoder.SessionBuilder{})
	provider := &providercore.Record{
		ID:       106,
		Platform: capability.PlatformQoder,
		Type:     capability.ProviderTypeCosy,
		Credentials: map[string]any{
			"machine_id": "machine-1",
		},
	}

	_, err := tokenSource.GetSession(context.Background(), provider)
	require.ErrorContains(t, err, "pat or security_oauth_token")
}

// TestQoderTokenProviderRejectsExplicitAuthDirWithoutReadingLocalAuth adds an
// auth_dir hint beside machine_id but no token. It checks the same explicit
// authentication diagnostic, keeping local credential hints outside runtime
// session construction.
func TestQoderTokenProviderRejectsExplicitAuthDirWithoutReadingLocalAuth(t *testing.T) {
	tokenSource := NewQoderTokenProvider(qoder.SessionBuilder{})
	provider := &providercore.Record{
		ID:       106,
		Platform: capability.PlatformQoder,
		Type:     capability.ProviderTypeCosy,
		Credentials: map[string]any{
			"machine_id": "machine-1",
			"auth_dir":   "/tmp/qoder-auth",
		},
	}

	_, err := tokenSource.GetSession(context.Background(), provider)
	require.ErrorContains(t, err, "pat or security_oauth_token")
}

// TestQoderTokenProviderSupportsInjectedPATExchange supplies a configured PAT
// exchange and stored organization metadata. It checks one cached exchange, no
// organization query and a distinct session when PAT input later changes.
func TestQoderTokenProviderSupportsInjectedPATExchange(t *testing.T) {
	calls := 0
	orgCalls := 0
	var exchangedPATs []string
	tokenSource := NewQoderTokenProvider(qoder.SessionBuilder{})
	tokenSource.exchangePAT = func(_ context.Context, pat string, machine *qoder.MachineIdentity) (*qoder.AuthIdentity, error) {
		calls++
		exchangedPATs = append(exchangedPATs, pat)
		require.NotEmpty(t, machine.MachineID)
		return &qoder.AuthIdentity{
			Name:               "PAT User",
			UID:                "uid-1",
			AID:                "uid-1",
			UserType:           "personal_standard",
			SecurityOauthToken: "dt-from-pat",
			RefreshToken:       "rt-from-pat",
		}, nil
	}
	tokenSource.getOrgTags = func(context.Context, string, string) (*qoder.OrganizationTags, error) {
		orgCalls++
		return nil, nil
	}

	provider := &providercore.Record{
		ID:       103,
		Platform: capability.PlatformQoder,
		Type:     capability.ProviderTypeCosy,
		Credentials: map[string]any{
			"pat":               "pat-123",
			"organization_id":   "org-from-provider",
			"organization_name": "Org From providercore.Record",
		},
	}

	session1, err := tokenSource.GetSession(context.Background(), provider)
	require.NoError(t, err)
	require.Equal(t, "dt-from-pat", session1.Identity.SecurityOauthToken)
	require.Equal(t, "org-from-provider", session1.Identity.OrganizationID)
	require.Equal(t, "Org From providercore.Record", session1.Identity.OrganizationName)

	session2, err := tokenSource.GetSession(context.Background(), provider)
	require.NoError(t, err)
	require.Same(t, session1, session2)
	require.Equal(t, 1, calls, "PAT exchange should not run after cache hit")
	require.Equal(t, 0, orgCalls, "provider-provided organization metadata should skip org lookup")

	provider.Credentials["pat"] = "pat-456"
	session3, err := tokenSource.GetSession(context.Background(), provider)
	require.NoError(t, err)
	require.NotSame(t, session1, session3)
	require.Equal(t, []string{"pat-123", "pat-456"}, exchangedPATs)
}

// TestQoderRefreshCredentialsHashTracksAuthenticationContext varies PAT, site,
// mode and organization beside a model mapping. It checks those authentication
// inputs change the hash while a mapping-only change retains the
// failed-identity comparison key.
func TestQoderRefreshCredentialsHashTracksAuthenticationContext(t *testing.T) {
	base := map[string]any{
		"pat":             "pat-123",
		"site":            "global",
		"refresh_mode":    "cosy",
		"organization_id": "org-1",
		"model_mapping":   map[string]any{"auto": "auto"},
	}
	baseHash := providercore.QoderRefreshCredentialsHash(base)
	for _, change := range []map[string]any{
		{"pat": "pat-456"},
		{"site": "cn"},
		{"refresh_mode": qoder.RefreshModeQoderCN20},
		{"organization_id": "org-2"},
	} {
		credentials := providercore.MergeCredentials(base, change)
		require.NotEqual(t, baseHash, providercore.QoderRefreshCredentialsHash(credentials))
	}
	unrelated := providercore.MergeCredentials(base, map[string]any{
		"model_mapping": map[string]any{"custom": "auto"},
	})
	require.Equal(t, baseHash, providercore.QoderRefreshCredentialsHash(unrelated))
}

// TestQoderTokenProviderPATExchangePopulatesOrganizationFromAPI exchanges a
// PAT without stored organization fields. It checks the exchanged token and organization ID
// passed to tag lookup and the organization metadata added to the resulting
// session.
func TestQoderTokenProviderPATExchangePopulatesOrganizationFromAPI(t *testing.T) {
	tokenSource := NewQoderTokenProvider(qoder.SessionBuilder{})
	tokenSource.exchangePAT = func(_ context.Context, _ string, _ *qoder.MachineIdentity) (*qoder.AuthIdentity, error) {
		return &qoder.AuthIdentity{
			OrganizationID:     "org-from-api",
			Name:               "PAT User",
			UID:                "uid-1",
			AID:                "aid-1",
			UserType:           "personal_standard",
			SecurityOauthToken: "dt-from-pat",
		}, nil
	}
	tokenSource.getOrgTags = func(_ context.Context, token, uid string) (*qoder.OrganizationTags, error) {
		require.Equal(t, "dt-from-pat", token)
		require.Equal(t, "org-from-api", uid)
		return &qoder.OrganizationTags{
			OrganizationID:   "org-from-api",
			OrganizationName: "Org From API",
		}, nil
	}

	provider := &providercore.Record{
		ID:       104,
		Platform: capability.PlatformQoder,
		Type:     capability.ProviderTypeCosy,
		Credentials: map[string]any{
			"pat": "pat-123",
		},
	}

	session, err := tokenSource.GetSession(context.Background(), provider)
	require.NoError(t, err)
	require.Equal(t, "org-from-api", session.Identity.OrganizationID)
	require.Equal(t, "Org From API", session.Identity.OrganizationName)
}

// TestQoderTokenProviderRoutesCNPATAndBuildsCNSession uses CN PAT and legacy
// machine fields with an injected site exchange. It checks CN site/version,
// returned security token and omission of the extra machine-token/type fields.
func TestQoderTokenProviderRoutesCNPATAndBuildsCNSession(t *testing.T) {
	tokenSource := NewQoderTokenProvider(qoder.SessionBuilder{})
	tokenSource.exchangeCNPAT = func(_ context.Context, pat string, machine *qoder.MachineIdentity) (*qoder.AuthIdentity, time.Time, error) {
		require.Equal(t, "cn-pat", pat)
		require.Equal(t, "machine-cn", machine.MachineID)
		require.Empty(t, machine.MachineToken)
		require.Empty(t, machine.MachineType)
		return &qoder.AuthIdentity{
			UID:                "uid-cn",
			AID:                "aid-cn",
			OrganizationID:     "org-cn",
			SecurityOauthToken: "cosy-cn",
			RefreshToken:       "refresh-cn",
		}, time.Now().Add(time.Hour), nil
	}
	provider := &providercore.Record{
		ID:       31,
		Platform: capability.PlatformQoder,
		Type:     capability.ProviderTypeCosy,
		Credentials: map[string]any{
			"site":          "cn",
			"pat":           "cn-pat",
			"machine_id":    "machine-cn",
			"machine_token": "machine-token-cn",
			"machine_type":  "machine-type-cn",
		},
	}

	session, err := tokenSource.GetSession(context.Background(), provider)
	require.NoError(t, err)
	require.Equal(t, qoder.SiteCN, session.Site)
	require.Equal(t, qoder.CNClientVersion, session.ClientVersion)
	require.Equal(t, "cosy-cn", session.Identity.SecurityOauthToken)
	require.Empty(t, session.Machine.MachineToken)
	require.Empty(t, session.Machine.MachineType)
}

// TestQoderTokenProviderRebuildsExpiredCNPATSession manually expires a cached
// CN entry with unchanged credentials. It checks a second exchange produces a
// distinct session while the first build's expiry was recorded as returned.
func TestQoderTokenProviderRebuildsExpiredCNPATSession(t *testing.T) {
	tokenSource := NewQoderTokenProvider(qoder.SessionBuilder{})
	expiresAt := time.Now().Add(time.Hour)
	exchangeCalls := 0
	tokenSource.exchangeCNPAT = func(_ context.Context, _ string, _ *qoder.MachineIdentity) (*qoder.AuthIdentity, time.Time, error) {
		exchangeCalls++
		return &qoder.AuthIdentity{
			UID:                "uid-cn",
			AID:                "uid-cn",
			SecurityOauthToken: fmt.Sprintf("cosy-cn-%d", exchangeCalls),
			RefreshToken:       "refresh-cn",
		}, expiresAt, nil
	}
	provider := &providercore.Record{
		ID:       32,
		Platform: capability.PlatformQoder,
		Type:     capability.ProviderTypeCosy,
		Credentials: map[string]any{
			"site":       "cn",
			"pat":        "cn-pat",
			"machine_id": "machine-cn",
		},
	}

	first, err := tokenSource.GetSession(context.Background(), provider)
	require.NoError(t, err)
	require.Equal(t, expiresAt, tokenSource.qoderState().Sessions[provider.ID].ExpiresAt)

	// Expire the cached OpenAPI token so the next read must exchange the PAT again.
	// Change expiry between serial reads so the next acquisition cannot reuse
	// the warm entry solely because the same credential hash remains
	// configured.
	entry := tokenSource.qoderState().Sessions[provider.ID]
	entry.ExpiresAt = time.Now().Add(-time.Second)
	tokenSource.qoderState().Sessions[provider.ID] = entry
	second, err := tokenSource.GetSession(context.Background(), provider)

	require.NoError(t, err)
	require.NotSame(t, first, second)
	require.Equal(t, "cosy-cn-2", second.Identity.SecurityOauthToken)
	require.Equal(t, 2, exchangeCalls)
}

// TestQoderTokenProviderSingleflightsConcurrentExpiredCNPATSession gates an
// expired CN rebuild while thirty-two workers acquire that identity. It checks
// all workers return the same pointer and only initial build plus one rebuild
// invoke the exchanger.
func TestQoderTokenProviderSingleflightsConcurrentExpiredCNPATSession(t *testing.T) {
	tokenSource := NewQoderTokenProvider(qoder.SessionBuilder{})
	var exchangeCalls atomic.Int32
	var blockRebuild atomic.Bool
	rebuildStarted := make(chan struct{})
	releaseRebuild := make(chan struct{})
	tokenSource.exchangeCNPAT = func(_ context.Context, _ string, _ *qoder.MachineIdentity) (*qoder.AuthIdentity, time.Time, error) {
		call := exchangeCalls.Add(1)
		// Once expiry is injected, hold the rebuilding exchange and signal
		// only its second overall invocation. A duplicate exchanger changes
		// the count while sharing the release gate.
		if blockRebuild.Load() {
			if call == 2 {
				close(rebuildStarted)
			}
			<-releaseRebuild
		}
		return &qoder.AuthIdentity{
			UID:                "uid-cn",
			AID:                "uid-cn",
			SecurityOauthToken: fmt.Sprintf("cosy-cn-%d", call),
		}, time.Now().Add(time.Hour), nil
	}
	provider := &providercore.Record{
		ID:       3201,
		Platform: capability.PlatformQoder,
		Type:     capability.ProviderTypeCosy,
		Credentials: map[string]any{
			"site":       "cn",
			"pat":        "cn-pat",
			"machine_id": "machine-cn",
		},
	}

	_, err := tokenSource.GetSession(context.Background(), provider)
	require.NoError(t, err)
	// Expire the warm entry under core synchronization before releasing
	// concurrent readers; the next shared build therefore tests expiry
	// recovery rather than a normal cache hit.
	tokenSource.qoderState().Mu.Lock()
	entry := tokenSource.qoderState().Sessions[provider.ID]
	entry.ExpiresAt = time.Now().Add(-time.Second)
	tokenSource.qoderState().Sessions[provider.ID] = entry
	tokenSource.qoderState().Mu.Unlock()
	blockRebuild.Store(true)

	const workers = 32
	start := make(chan struct{})
	results := make(chan *qoder.SessionContext, workers)
	errorsCh := make(chan error, workers)
	var ready sync.WaitGroup
	var done sync.WaitGroup
	ready.Add(workers)
	done.Add(workers)
	// Release workers from one start gate and buffer every result/error so
	// they can finish before the main goroutine closes and drains those
	// channels.
	for range workers {
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			session, getErr := tokenSource.GetSession(context.Background(), provider)
			results <- session
			errorsCh <- getErr
		}()
	}
	ready.Wait()
	close(start)
	<-rebuildStarted
	// Keep the first rebuild held briefly so released workers have a scheduling
	// opportunity to join its flight; elapsed sleep does not prove every worker registered.
	time.Sleep(20 * time.Millisecond)
	require.Equal(t, int32(2), exchangeCalls.Load())
	// Release the held rebuild before joining its callers; close result
	// channels only after all senders have exited so pointer comparison
	// observes completed acquisition outcomes.
	close(releaseRebuild)
	done.Wait()
	close(results)
	close(errorsCh)

	for getErr := range errorsCh {
		require.NoError(t, getErr)
	}
	var shared *qoder.SessionContext
	for session := range results {
		require.NotNil(t, session)
		if shared == nil {
			shared = session
		}
		require.Same(t, shared, session)
	}
	require.Equal(t, int32(2), exchangeCalls.Load(), "初次构建加一次过期重建应只交换两次")
}

// TestQoderTokenProviderSingleflightSeparatesWaiterCancellation cancels the
// first caller after shared exchange begins, then permits another acquisition.
// It checks first cancellation, a usable later session and one supplier
// exchange.
func TestQoderTokenProviderSingleflightSeparatesWaiterCancellation(t *testing.T) {
	tokenSource := NewQoderTokenProvider(qoder.SessionBuilder{})
	var exchangeCalls atomic.Int32
	buildStarted := make(chan struct{})
	releaseBuild := make(chan struct{})
	tokenSource.exchangeCNPAT = func(ctx context.Context, _ string, _ *qoder.MachineIdentity) (*qoder.AuthIdentity, time.Time, error) {
		exchangeCalls.Add(1)
		// Signal entry using the shared build context, then hold completion on
		// release or that context so cancelling an individual waiter cannot
		// silently finish the supplier operation.
		close(buildStarted)
		select {
		case <-ctx.Done():
			return nil, time.Time{}, ctx.Err()
		case <-releaseBuild:
			return &qoder.AuthIdentity{
				UID:                "uid-cn",
				AID:                "uid-cn",
				SecurityOauthToken: "cosy-cn-shared",
			}, time.Now().Add(time.Hour), nil
		}
	}
	provider := &providercore.Record{
		ID:       3204,
		Platform: capability.PlatformQoder,
		Type:     capability.ProviderTypeCosy,
		Credentials: map[string]any{
			"site":       "cn",
			"pat":        "cn-pat",
			"machine_id": "machine-cn",
		},
	}

	firstCtx, cancelFirst := context.WithCancel(context.Background())
	firstResult := make(chan error, 1)
	// Cancel a registered caller only after supplier entry is signaled,
	// exposing whether that caller cancellation incorrectly stops the
	// independently budgeted shared exchange.
	go func() {
		_, getErr := tokenSource.GetSession(firstCtx, provider)
		firstResult <- getErr
	}()
	<-buildStarted
	cancelFirst()
	require.ErrorIs(t, <-firstResult, context.Canceled)

	// Start a later acquisition with a buffered paired result while the
	// independent build remains held. Reading this result after release checks
	// availability without assuming prior waiter registration.
	secondResult := make(chan struct {
		session *qoder.SessionContext // acquired session result pointer
		err     error                 // associated caller result error
	}, 1)
	go func() {
		session, getErr := tokenSource.GetSession(context.Background(), provider)
		secondResult <- struct {
			session *qoder.SessionContext // acquired session result pointer
			err     error                 // associated caller result error
		}{session: session, err: getErr}
	}()
	require.Eventually(t, func() bool {
		return exchangeCalls.Load() == 1
	}, time.Second, 10*time.Millisecond)
	// Permit the shared exchange to finish for the later reader. The
	// call-count check establishes one supplier operation, not that the later
	// reader already registered as a waiter.
	close(releaseBuild)

	second := <-secondResult
	require.NoError(t, second.err)
	require.NotNil(t, second.session)
	require.Equal(t, "cosy-cn-shared", second.session.Identity.SecurityOauthToken)
	require.Equal(t, int32(1), exchangeCalls.Load())
}

// TestQoderTokenProviderDetachedBuildHasHardTimeout supplies a cooperative
// exchanger that waits for its short shared-build deadline. It checks
// DeadlineExceeded and a bounded elapsed observation for that
// cancellation-responsive callback.
func TestQoderTokenProviderDetachedBuildHasHardTimeout(t *testing.T) {
	tokenSource := NewQoderTokenProvider(qoder.SessionBuilder{})
	tokenSource.qoderState().SessionBuildTimeout = 20 * time.Millisecond
	tokenSource.exchangeCNPAT = func(ctx context.Context, _ string, _ *qoder.MachineIdentity) (*qoder.AuthIdentity, time.Time, error) {
		// Respond cooperatively to the build context deadline so the timing
		// assertion measures a callback that releases itself rather than
		// forced interruption of arbitrary supplier work.
		<-ctx.Done()
		return nil, time.Time{}, ctx.Err()
	}
	provider := &providercore.Record{ID: 3205, Platform: capability.PlatformQoder, Type: capability.ProviderTypeCosy, Credentials: map[string]any{
		"site": "cn", "pat": "cn-pat", "machine_id": "machine-cn",
	}}

	startedAt := time.Now()
	_, err := tokenSource.GetSession(context.Background(), provider)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(startedAt), time.Second)
}

// TestQoderTokenProviderBuildUsesProviderSnapshot holds a PAT build before
// changing the caller's organization credential. It checks the resulting
// identity uses the captured original value rather than the later source-map
// mutation.
func TestQoderTokenProviderBuildUsesProviderSnapshot(t *testing.T) {
	tokenSource := NewQoderTokenProvider(qoder.SessionBuilder{})
	buildStarted := make(chan struct{})
	releaseBuild := make(chan struct{})
	tokenSource.exchangePAT = func(_ context.Context, _ string, _ *qoder.MachineIdentity) (*qoder.AuthIdentity, error) {
		// Announce exchange after the builder captured input, then hold its
		// result so the main goroutine can change the original organization
		// before this captured identity is returned.
		close(buildStarted)
		<-releaseBuild
		return &qoder.AuthIdentity{
			UID:                "uid-global",
			AID:                "uid-global",
			SecurityOauthToken: "cosy-global",
		}, nil
	}
	provider := &providercore.Record{ID: 3206, Platform: capability.PlatformQoder, Type: capability.ProviderTypeCosy, Credentials: map[string]any{
		"site": "global", "pat": "global-pat", "machine_id": "machine-global", "organization_id": "org-original",
	}}

	// sessionResult carries one asynchronous acquisition pointer and its paired caller error.
	type sessionResult struct {
		session *qoder.SessionContext // acquired session result pointer
		err     error                 // associated caller result error
	}
	resultCh := make(chan sessionResult, 1)
	// Hold exchange after the core has captured provider input, then mutate
	// the caller map before release so the identity result can expose snapshot
	// sharing.
	go func() {
		session, getErr := tokenSource.GetSession(context.Background(), provider)
		resultCh <- sessionResult{session: session, err: getErr}
	}()
	<-buildStarted
	// Supplier entry follows snapshot capture. Mutate the original
	// organization now so a builder reading the caller map late would produce
	// the wrong identity value.
	provider.Credentials["organization_id"] = "org-mutated"
	close(releaseBuild)

	result := <-resultCh
	require.NoError(t, result.err)
	require.Equal(t, "org-original", result.session.Identity.OrganizationID)
}

// TestQoderTokenProviderInvalidateDoesNotReturnOrCacheInflightSession holds
// the first CN generation before invalidating and rebuilding. It checks the
// old caller gets invalidation with no session and the new pointer remains
// cached after release.
func TestQoderTokenProviderInvalidateDoesNotReturnOrCacheInflightSession(t *testing.T) {
	tokenSource := NewQoderTokenProvider(qoder.SessionBuilder{})
	var exchangeCalls atomic.Int32
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	tokenSource.exchangeCNPAT = func(_ context.Context, _ string, _ *qoder.MachineIdentity) (*qoder.AuthIdentity, time.Time, error) {
		call := exchangeCalls.Add(1)
		// Hold the first supplier result before generation invalidation; later
		// exchanges remain unblocked so the test can publish a replacement
		// before this old result returns.
		if call == 1 {
			close(firstStarted)
			<-releaseFirst
		}
		return &qoder.AuthIdentity{
			UID:                "uid-cn",
			AID:                "uid-cn",
			SecurityOauthToken: fmt.Sprintf("cosy-cn-%d", call),
		}, time.Now().Add(time.Hour), nil
	}
	provider := &providercore.Record{
		ID:       3202,
		Platform: capability.PlatformQoder,
		Type:     capability.ProviderTypeCosy,
		Credentials: map[string]any{
			"site":       "cn",
			"pat":        "cn-pat",
			"machine_id": "machine-cn",
		},
	}

	// sessionResult carries one asynchronous acquisition pointer and its paired caller error.
	type sessionResult struct {
		session *qoder.SessionContext // acquired session result pointer
		err     error                 // associated caller result error
	}
	// Capture the held first-generation caller result in a buffered channel,
	// allowing its late invalidation error to be joined after a new generation
	// has completed.
	firstResult := make(chan sessionResult, 1)
	go func() {
		session, getErr := tokenSource.GetSession(context.Background(), provider)
		firstResult <- sessionResult{session: session, err: getErr}
	}()
	<-firstStarted
	// Advance the generation while the first exchange is held, then build the
	// replacement before release so late publication must reject that older
	// in-flight result.
	tokenSource.Invalidate(provider.ID)

	second, err := tokenSource.GetSession(context.Background(), provider)
	require.NoError(t, err)
	require.Equal(t, "cosy-cn-2", second.Identity.SecurityOauthToken)
	close(releaseFirst)
	stale := <-firstResult
	require.Nil(t, stale.session)
	require.ErrorIs(t, stale.err, errQoderSessionBuildInvalidated)

	tokenSource.qoderState().Mu.Lock()
	cached := tokenSource.qoderState().Sessions[provider.ID].Session
	tokenSource.qoderState().Mu.Unlock()
	require.Same(t, second, cached)
	require.Equal(t, int32(2), exchangeCalls.Load())
}

// TestQoderTokenProviderDoesNotMergeDifferentCredentialHashes holds an older
// PAT build while newer credentials arrive for the same ID. It checks separate
// exchange, rejection of the old result and the latest hash/session retained
// in cache.
func TestQoderTokenProviderDoesNotMergeDifferentCredentialHashes(t *testing.T) {
	tokenSource := NewQoderTokenProvider(qoder.SessionBuilder{})
	var exchangeCalls atomic.Int32
	oldStarted := make(chan struct{})
	releaseOld := make(chan struct{})
	tokenSource.exchangeCNPAT = func(_ context.Context, pat string, _ *qoder.MachineIdentity) (*qoder.AuthIdentity, time.Time, error) {
		exchangeCalls.Add(1)
		// Gate only the old PAT exchange so a new credential hash can proceed
		// independently. The old result is released after the new identity has
		// been accepted.
		if pat == "old-pat" {
			close(oldStarted)
			<-releaseOld
		}
		return &qoder.AuthIdentity{
			UID:                "uid-cn",
			AID:                "uid-cn",
			SecurityOauthToken: "cosy-" + pat,
		}, time.Now().Add(time.Hour), nil
	}
	oldProvider := &providercore.Record{ID: 3203, Platform: capability.PlatformQoder, Type: capability.ProviderTypeCosy, Credentials: map[string]any{
		"site": "cn", "pat": "old-pat", "machine_id": "machine-cn", "_token_version": int64(1),
	}}
	newProvider := &providercore.Record{ID: 3203, Platform: capability.PlatformQoder, Type: capability.ProviderTypeCosy, Credentials: map[string]any{
		"site": "cn", "pat": "new-pat", "machine_id": "machine-cn", "_token_version": int64(2),
	}}

	// Run the old identity acquisition behind its supplier gate and retain its
	// eventual error separately, so the newer hash can be tested before
	// joining the rejected old flight.
	oldResult := make(chan error, 1)
	go func() {
		_, getErr := tokenSource.GetSession(context.Background(), oldProvider)
		oldResult <- getErr
	}()
	<-oldStarted
	// Acquire a higher-version PAT while the old identity exchange remains
	// held; returning the new token proves these different credential hashes
	// do not share that old flight.
	newSession, err := tokenSource.GetSession(context.Background(), newProvider)
	require.NoError(t, err)
	require.Equal(t, "cosy-new-pat", newSession.Identity.SecurityOauthToken)
	close(releaseOld)
	require.ErrorIs(t, <-oldResult, errQoderSessionBuildInvalidated)

	tokenSource.qoderState().Mu.Lock()
	cached := tokenSource.qoderState().Sessions[newProvider.ID]
	tokenSource.qoderState().Mu.Unlock()
	require.Equal(t, providercore.QoderCredentialsHash(newProvider.Credentials), cached.CredentialsHash)
	require.Same(t, newSession, cached.Session)
	require.Equal(t, int32(2), exchangeCalls.Load())
}

// TestQoderTokenProviderDoesNotRegressToOlderCredentialVersion builds version
// two before reading a version-one snapshot. It checks pointer reuse, one
// exchange and retention of the newer credential hash/version in provider
// state.
func TestQoderTokenProviderDoesNotRegressToOlderCredentialVersion(t *testing.T) {
	tokenSource := NewQoderTokenProvider(qoder.SessionBuilder{})
	var exchangeCalls atomic.Int32
	tokenSource.exchangeCNPAT = func(_ context.Context, pat string, _ *qoder.MachineIdentity) (*qoder.AuthIdentity, time.Time, error) {
		exchangeCalls.Add(1)
		return &qoder.AuthIdentity{
			UID:                "uid-cn",
			AID:                "uid-cn",
			SecurityOauthToken: "cosy-" + pat,
		}, time.Now().Add(time.Hour), nil
	}
	newProvider := &providercore.Record{ID: 3207, Platform: capability.PlatformQoder, Type: capability.ProviderTypeCosy, Credentials: map[string]any{
		"site": "cn", "pat": "new-pat", "machine_id": "machine-cn", "_token_version": int64(2),
	}}
	staleProvider := &providercore.Record{ID: 3207, Platform: capability.PlatformQoder, Type: capability.ProviderTypeCosy, Credentials: map[string]any{
		"site": "cn", "pat": "old-pat", "machine_id": "machine-cn", "_token_version": int64(1),
	}}

	latest, err := tokenSource.GetSession(context.Background(), newProvider)
	require.NoError(t, err)
	require.Equal(t, "cosy-new-pat", latest.Identity.SecurityOauthToken)

	fromStaleSnapshot, err := tokenSource.GetSession(context.Background(), staleProvider)
	require.NoError(t, err)
	require.Same(t, latest, fromStaleSnapshot)
	require.Equal(t, int32(1), exchangeCalls.Load())

	tokenSource.qoderState().Mu.Lock()
	cached := tokenSource.qoderState().Sessions[newProvider.ID]
	currentState := tokenSource.qoderState().ProviderStates[newProvider.ID]
	tokenSource.qoderState().Mu.Unlock()
	require.Equal(t, providercore.QoderCredentialsHash(newProvider.Credentials), currentState.CredentialsHash)
	require.Equal(t, int64(2), currentState.CredentialVersion)
	require.Same(t, latest, cached.Session)
}

// TestQoderTokenProviderAuthoritativeInvalidationBlocksUnobservedStaleVersion
// supplies a refreshed version through identity-aware invalidation before a
// stale read. It checks the stale caller builds with the new PAT and retains
// version-two state.
func TestQoderTokenProviderAuthoritativeInvalidationBlocksUnobservedStaleVersion(t *testing.T) {
	tokenSource := NewQoderTokenProvider(qoder.SessionBuilder{})
	var exchangeCalls atomic.Int32
	tokenSource.exchangeCNPAT = func(_ context.Context, pat string, _ *qoder.MachineIdentity) (*qoder.AuthIdentity, time.Time, error) {
		exchangeCalls.Add(1)
		return &qoder.AuthIdentity{
			UID:                "uid-cn",
			AID:                "uid-cn",
			SecurityOauthToken: "cosy-" + pat,
		}, time.Now().Add(time.Hour), nil
	}
	staleProvider := &providercore.Record{ID: 3210, Platform: capability.PlatformQoder, Type: capability.ProviderTypeCosy, Credentials: map[string]any{
		"site": "cn", "pat": "old-pat", "machine_id": "machine-cn", "_token_version": int64(1),
	}}
	refreshedProvider := &providercore.Record{ID: 3210, Platform: capability.PlatformQoder, Type: capability.ProviderTypeCosy, Credentials: map[string]any{
		"site": "cn", "pat": "new-pat", "machine_id": "machine-cn", "_token_version": int64(2),
	}}

	oldSession, err := tokenSource.GetSession(context.Background(), staleProvider)
	require.NoError(t, err)
	require.Equal(t, "cosy-old-pat", oldSession.Identity.SecurityOauthToken)
	// Publish the newer identity through invalidation before any read observes
	// it, making the following stale snapshot rely on recorded authoritative
	// version state.
	tokenSource.InvalidateProvider(refreshedProvider)

	fromStaleSnapshot, err := tokenSource.GetSession(context.Background(), staleProvider)
	require.NoError(t, err)
	require.Equal(t, "cosy-new-pat", fromStaleSnapshot.Identity.SecurityOauthToken)
	require.Equal(t, int32(2), exchangeCalls.Load())

	tokenSource.qoderState().Mu.Lock()
	currentState := tokenSource.qoderState().ProviderStates[staleProvider.ID]
	tokenSource.qoderState().Mu.Unlock()
	require.Equal(t, int64(2), currentState.CredentialVersion)
	require.Equal(t, providercore.QoderCredentialsHash(refreshedProvider.Credentials), currentState.CredentialsHash)
}

// TestQoderTokenProviderAuthoritativeInvalidationRejectsOlderSnapshot
// invalidates version three before an older version-two notice. It checks the
// newer cached pointer/hash survive and an older read causes no additional
// exchange.
func TestQoderTokenProviderAuthoritativeInvalidationRejectsOlderSnapshot(t *testing.T) {
	tokenSource := NewQoderTokenProvider(qoder.SessionBuilder{})
	var exchangeCalls atomic.Int32
	tokenSource.exchangeCNPAT = func(_ context.Context, pat string, _ *qoder.MachineIdentity) (*qoder.AuthIdentity, time.Time, error) {
		exchangeCalls.Add(1)
		return &qoder.AuthIdentity{
			UID:                "uid-cn",
			AID:                "uid-cn",
			SecurityOauthToken: "cosy-" + pat,
		}, time.Now().Add(time.Hour), nil
	}
	now := time.Now()
	latestProvider := &providercore.Record{ID: 3211, Platform: capability.PlatformQoder, Type: capability.ProviderTypeCosy, UpdatedAt: now, Credentials: map[string]any{
		"site": "cn", "pat": "v3-pat", "machine_id": "machine-cn", "_token_version": int64(3),
	}}
	olderProvider := &providercore.Record{ID: 3211, Platform: capability.PlatformQoder, Type: capability.ProviderTypeCosy, UpdatedAt: now.Add(-time.Minute), Credentials: map[string]any{
		"site": "cn", "pat": "v2-pat", "machine_id": "machine-cn", "_token_version": int64(2),
	}}

	tokenSource.InvalidateProvider(latestProvider)
	latestSession, err := tokenSource.GetSession(context.Background(), latestProvider)
	require.NoError(t, err)
	// Attempt older invalidation after a newer session is cached; this
	// ordering exposes whether the stale notice evicts or regresses the
	// current credential generation.
	tokenSource.InvalidateProvider(olderProvider)

	tokenSource.qoderState().Mu.Lock()
	cached := tokenSource.qoderState().Sessions[latestProvider.ID]
	currentState := tokenSource.qoderState().ProviderStates[latestProvider.ID]
	tokenSource.qoderState().Mu.Unlock()
	require.Same(t, latestSession, cached.Session)
	require.Equal(t, int64(3), currentState.CredentialVersion)
	require.Equal(t, providercore.QoderCredentialsHash(latestProvider.Credentials), currentState.CredentialsHash)

	fromOlderSnapshot, err := tokenSource.GetSession(context.Background(), olderProvider)
	require.NoError(t, err)
	require.Same(t, latestSession, fromOlderSnapshot)
	require.Equal(t, int32(1), exchangeCalls.Load())
}

// TestQoderTokenProviderNewerVersionOverridesOlderUpdatedAt provides a newer
// token version whose UpdatedAt is earlier. It checks a new session is built
// from that higher version despite the timestamp ordering.
func TestQoderTokenProviderNewerVersionOverridesOlderUpdatedAt(t *testing.T) {
	tokenSource := NewQoderTokenProvider(qoder.SessionBuilder{})
	var exchangeCalls atomic.Int32
	tokenSource.exchangeCNPAT = func(_ context.Context, pat string, _ *qoder.MachineIdentity) (*qoder.AuthIdentity, time.Time, error) {
		exchangeCalls.Add(1)
		return &qoder.AuthIdentity{
			UID:                "uid-cn",
			AID:                "uid-cn",
			SecurityOauthToken: "cosy-" + pat,
		}, time.Now().Add(time.Hour), nil
	}
	now := time.Now()
	currentProvider := &providercore.Record{ID: 3212, Platform: capability.PlatformQoder, Type: capability.ProviderTypeCosy, UpdatedAt: now, Credentials: map[string]any{
		"site": "cn", "pat": "v2-pat", "machine_id": "machine-cn", "_token_version": int64(2),
	}}
	refreshedProvider := &providercore.Record{ID: 3212, Platform: capability.PlatformQoder, Type: capability.ProviderTypeCosy, UpdatedAt: now.Add(-time.Minute), Credentials: map[string]any{
		"site": "cn", "pat": "v3-pat", "machine_id": "machine-cn", "_token_version": int64(3),
	}}

	currentSession, err := tokenSource.GetSession(context.Background(), currentProvider)
	require.NoError(t, err)
	require.Equal(t, "cosy-v2-pat", currentSession.Identity.SecurityOauthToken)
	refreshedSession, err := tokenSource.GetSession(context.Background(), refreshedProvider)
	require.NoError(t, err)
	require.Equal(t, "cosy-v3-pat", refreshedSession.Identity.SecurityOauthToken)
	require.Equal(t, int32(2), exchangeCalls.Load())
}

// TestQoderTokenProviderAuthoritativeNewerVersionOverridesOlderUpdatedAt
// publishes a higher version through invalidation with an earlier UpdatedAt.
// It checks a stale caller receives that higher-version PAT and provider-state
// hash.
func TestQoderTokenProviderAuthoritativeNewerVersionOverridesOlderUpdatedAt(t *testing.T) {
	tokenSource := NewQoderTokenProvider(qoder.SessionBuilder{})
	var exchangeCalls atomic.Int32
	tokenSource.exchangeCNPAT = func(_ context.Context, pat string, _ *qoder.MachineIdentity) (*qoder.AuthIdentity, time.Time, error) {
		exchangeCalls.Add(1)
		return &qoder.AuthIdentity{
			UID:                "uid-cn",
			AID:                "uid-cn",
			SecurityOauthToken: "cosy-" + pat,
		}, time.Now().Add(time.Hour), nil
	}
	now := time.Now()
	currentProvider := &providercore.Record{ID: 3213, Platform: capability.PlatformQoder, Type: capability.ProviderTypeCosy, UpdatedAt: now, Credentials: map[string]any{
		"site": "cn", "pat": "v2-pat", "machine_id": "machine-cn", "_token_version": int64(2),
	}}
	refreshedProvider := &providercore.Record{ID: 3213, Platform: capability.PlatformQoder, Type: capability.ProviderTypeCosy, UpdatedAt: now.Add(-time.Minute), Credentials: map[string]any{
		"site": "cn", "pat": "v3-pat", "machine_id": "machine-cn", "_token_version": int64(3),
	}}

	_, err := tokenSource.GetSession(context.Background(), currentProvider)
	require.NoError(t, err)
	// Introduce the higher token version through invalidation despite its
	// earlier update timestamp; the following old snapshot must use that
	// authoritative authentication order.
	tokenSource.InvalidateProvider(refreshedProvider)
	fromStaleSnapshot, err := tokenSource.GetSession(context.Background(), currentProvider)
	require.NoError(t, err)
	require.Equal(t, "cosy-v3-pat", fromStaleSnapshot.Identity.SecurityOauthToken)
	require.Equal(t, int32(2), exchangeCalls.Load())

	tokenSource.qoderState().Mu.Lock()
	currentState := tokenSource.qoderState().ProviderStates[currentProvider.ID]
	tokenSource.qoderState().Mu.Unlock()
	require.Equal(t, int64(3), currentState.CredentialVersion)
	require.Equal(t, providercore.QoderCredentialsHash(refreshedProvider.Credentials), currentState.CredentialsHash)
}

// TestQoderTokenProviderOlderCredentialVersionJoinsNewerInflightBuild holds a
// version-two exchange before a version-one acquisition. It checks the old
// caller does not return early, both results share the new session and one
// exchange ran.
func TestQoderTokenProviderOlderCredentialVersionJoinsNewerInflightBuild(t *testing.T) {
	tokenSource := NewQoderTokenProvider(qoder.SessionBuilder{})
	var exchangeCalls atomic.Int32
	newBuildStarted := make(chan struct{})
	releaseNewBuild := make(chan struct{})
	tokenSource.exchangeCNPAT = func(_ context.Context, pat string, _ *qoder.MachineIdentity) (*qoder.AuthIdentity, time.Time, error) {
		exchangeCalls.Add(1)
		// Hold the latest identity supplier result while older snapshots
		// attempt acquisition. The old PAT branch is unblocked, making an
		// accidental separate exchange observable before release.
		if pat == "new-pat" {
			close(newBuildStarted)
			<-releaseNewBuild
		}
		return &qoder.AuthIdentity{
			UID:                "uid-cn",
			AID:                "uid-cn",
			SecurityOauthToken: "cosy-" + pat,
		}, time.Now().Add(time.Hour), nil
	}
	newProvider := &providercore.Record{ID: 3208, Platform: capability.PlatformQoder, Type: capability.ProviderTypeCosy, Credentials: map[string]any{
		"site": "cn", "pat": "new-pat", "machine_id": "machine-cn", "_token_version": int64(2),
	}}
	staleProvider := &providercore.Record{ID: 3208, Platform: capability.PlatformQoder, Type: capability.ProviderTypeCosy, Credentials: map[string]any{
		"site": "cn", "pat": "old-pat", "machine_id": "machine-cn", "_token_version": int64(1),
	}}

	// sessionResult carries one asynchronous acquisition pointer and its paired caller error.
	type sessionResult struct {
		session *qoder.SessionContext // acquired session result pointer
		err     error                 // associated caller result error
	}
	newResult := make(chan sessionResult, 1)
	// Hold the newer-version exchange before starting the stale caller so the
	// later result can distinguish joining that current build from exchanging
	// the old PAT.
	go func() {
		session, getErr := tokenSource.GetSession(context.Background(), newProvider)
		newResult <- sessionResult{session: session, err: getErr}
	}()
	<-newBuildStarted

	// Acquire the stale snapshot only after the current supplier entered its
	// gate. Buffer its result for the early-return observation and the later
	// joined pointer comparison.
	staleResult := make(chan sessionResult, 1)
	go func() {
		session, getErr := tokenSource.GetSession(context.Background(), staleProvider)
		staleResult <- sessionResult{session: session, err: getErr}
	}()
	// Check for a result while the newer exchange is held; a separate old-PAT
	// exchange can finish during this interval, exposing unintended use of
	// the stale credentials.
	select {
	case result := <-staleResult:
		close(releaseNewBuild)
		require.FailNowf(t, "stale build returned early", "session=%v err=%v", result.session, result.err)
	case <-time.After(30 * time.Millisecond):
	}
	// Release the current exchange and collect both completed results before
	// comparing pointer identity and supplier count, preserving the intended
	// newer-version interleaving.
	close(releaseNewBuild)

	latest := <-newResult
	fromStaleSnapshot := <-staleResult
	require.NoError(t, latest.err)
	require.NoError(t, fromStaleSnapshot.err)
	require.Same(t, latest.session, fromStaleSnapshot.session)
	require.Equal(t, "cosy-new-pat", latest.session.Identity.SecurityOauthToken)
	require.Equal(t, int32(1), exchangeCalls.Load())
}

// TestQoderTokenProviderUpdatesSnapshotForSameCredentials changes the proxy
// object while authentication remains identical. It checks the cached session
// pointer is reused and the retained provider snapshot contains the later
// proxy field.
func TestQoderTokenProviderUpdatesSnapshotForSameCredentials(t *testing.T) {
	tokenSource := NewQoderTokenProvider(qoder.SessionBuilder{})
	tokenSource.exchangeCNPAT = func(_ context.Context, _ string, _ *qoder.MachineIdentity) (*qoder.AuthIdentity, time.Time, error) {
		return &qoder.AuthIdentity{UID: "uid-cn", AID: "uid-cn", SecurityOauthToken: "cosy-cn"}, time.Now().Add(time.Hour), nil
	}
	credentials := map[string]any{
		"site": "cn", "pat": "cn-pat", "machine_id": "machine-cn", "_token_version": int64(2),
	}
	oldProxy := &egress.Proxy{Protocol: "http", Host: "old-proxy.example", Port: 8080}
	newProxy := &egress.Proxy{Protocol: "http", Host: "new-proxy.example", Port: 8080}
	firstProvider := &providercore.Record{ID: 3209, Platform: capability.PlatformQoder, Type: capability.ProviderTypeCosy, Credentials: credentials, Proxy: oldProxy}
	latestProvider := &providercore.Record{ID: 3209, Platform: capability.PlatformQoder, Type: capability.ProviderTypeCosy, Credentials: maps.Clone(credentials), Proxy: newProxy}

	first, err := tokenSource.GetSession(context.Background(), firstProvider)
	require.NoError(t, err)
	second, err := tokenSource.GetSession(context.Background(), latestProvider)
	require.NoError(t, err)
	require.Same(t, first, second)

	tokenSource.qoderState().Mu.Lock()
	storedSnapshot := tokenSource.qoderState().ProviderStates[latestProvider.ID].ProviderSnapshot
	tokenSource.qoderState().Mu.Unlock()
	require.NotNil(t, storedSnapshot)
	require.Equal(t, "new-proxy.example", storedSnapshot.Proxy.Host)
}

// TestQoderTokenProviderDirectTokenPopulatesOrganizationFromAPI builds direct
// identity without organization metadata. It checks tag lookup receives the
// stored token/user and the returned organization values enrich the session
// identity.
func TestQoderTokenProviderDirectTokenPopulatesOrganizationFromAPI(t *testing.T) {
	tokenSource := NewQoderTokenProvider(qoder.SessionBuilder{})
	tokenSource.SetHTTPUpstream(&qoderCenterHTTPUpstreamStub{body: `{"user_id":"uid-1","organization":{"id":"org-from-api"}}`}, nil)
	tokenSource.getOrgTags = func(_ context.Context, token, uid string) (*qoder.OrganizationTags, error) {
		require.Equal(t, "dt-token", token)
		require.Equal(t, "org-from-api", uid)
		return &qoder.OrganizationTags{
			OrganizationID:   "org-from-api",
			OrganizationName: "Org From API",
		}, nil
	}

	provider := &providercore.Record{
		ID:       110,
		Platform: capability.PlatformQoder,
		Type:     capability.ProviderTypeCosy,
		Credentials: map[string]any{
			"security_oauth_token": "dt-token",
			"machine_id":           "machine-1",
			"uid":                  "uid-1",
		},
	}

	session, err := tokenSource.GetSession(context.Background(), provider)
	require.NoError(t, err)
	require.Equal(t, "org-from-api", session.Identity.OrganizationID)
	require.Equal(t, "Org From API", session.Identity.OrganizationName)
}

// TestQoderTokenProviderPATExchangeUsesProviderDoer binds shared transport and
// a loaded proxy for PAT exchange. It checks the returned token and the proxy,
// provider ID and concurrency routed through that doer.
func TestQoderTokenProviderPATExchangeUsesProviderDoer(t *testing.T) {
	upstream := &qoderCenterHTTPUpstreamStub{}
	tokenSource := NewQoderTokenProvider(qoder.SessionBuilder{})
	tokenSource.SetHTTPUpstream(upstream, nil)
	provider := &providercore.Record{
		ID:          107,
		Platform:    capability.PlatformQoder,
		Type:        capability.ProviderTypeCosy,
		Concurrency: 3,
		ProxyID:     ptrInt64ForQoderTest(9),
		Proxy:       &egress.Proxy{Protocol: "http", Host: "proxy.example.com", Port: 8080},
		Credentials: map[string]any{"pat": "pat-123"},
	}

	session, err := tokenSource.GetSession(context.Background(), provider)

	require.NoError(t, err)
	require.Equal(t, "dt-from-center", session.Identity.SecurityOauthToken)
	require.Equal(t, "http://proxy.example.com:8080", upstream.proxyURL)
	require.Equal(t, int64(107), upstream.providerID)
	require.Equal(t, 3, upstream.providerConcurrency)
}

// TestQoderTokenProviderPATOrganizationTagsUsesProviderDoer uses injected PAT
// identity and shared transport for organization lookup. It checks one tag
// request, organization metadata and provider proxy/ID/concurrency/fingerprint
// parameters.
func TestQoderTokenProviderPATOrganizationTagsUsesProviderDoer(t *testing.T) {
	upstream := &qoderCenterHTTPUpstreamStub{
		body: `{"organization_id":"org-via-upstream","organization_name":"Org Via Upstream"}`,
	}
	tokenSource := NewQoderTokenProvider(qoder.SessionBuilder{})
	tokenSource.SetHTTPUpstream(upstream, &egressadapter.TLSProfiles{})
	tokenSource.exchangePAT = func(_ context.Context, _ string, _ *qoder.MachineIdentity) (*qoder.AuthIdentity, error) {
		return &qoder.AuthIdentity{
			OrganizationID:     "org-via-upstream",
			Name:               "PAT User",
			UID:                "uid-1",
			AID:                "uid-1",
			UserType:           "personal_standard",
			SecurityOauthToken: "dt-from-pat",
		}, nil
	}
	provider := &providercore.Record{
		ID:          108,
		Platform:    capability.PlatformQoder,
		Type:        capability.ProviderTypeCosy,
		Concurrency: 4,
		ProxyID:     ptrInt64ForQoderTest(10),
		Proxy:       &egress.Proxy{Protocol: "http", Host: "proxy.example.com", Port: 8081},
		Credentials: map[string]any{"pat": "pat-123"},
		Extra:       map[string]any{"enable_tls_fingerprint": true},
	}

	session, err := tokenSource.GetSession(context.Background(), provider)

	require.NoError(t, err)
	require.Equal(t, "org-via-upstream", session.Identity.OrganizationID)
	require.Equal(t, "Org Via Upstream", session.Identity.OrganizationName)
	require.Len(t, upstream.requests, 1)
	require.Contains(t, upstream.requests[0].URL.Path, qoder.OrganizationTagsPathPrefix+"org-via-upstream/tags")
	require.Equal(t, "http://proxy.example.com:8081", upstream.proxyURL)
	require.Equal(t, int64(108), upstream.providerID)
	require.Equal(t, 4, upstream.providerConcurrency)
	require.True(t, upstream.profileSet)
}

// TestQoderTokenProviderOrganizationTagsErrorRedactsSensitiveBody returns a
// 500 tag response containing credential and identity text. It checks status
// diagnostics, redaction markers and omission of each injected sensitive value
// from the error.
func TestQoderTokenProviderOrganizationTagsErrorRedactsSensitiveBody(t *testing.T) {
	upstream := &qoderCenterHTTPUpstreamStub{
		statusCode: http.StatusInternalServerError,
		body:       `{"message":"failed","securityOauthToken":"sec-secret","refresh_token":"rt-secret","uid":"uid-secret","cookie":"sid=secret"}`,
	}
	tokenSource := NewQoderTokenProvider(qoder.SessionBuilder{})
	tokenSource.SetHTTPUpstream(upstream, nil)
	provider := &providercore.Record{
		ID:       109,
		Platform: capability.PlatformQoder,
		Type:     capability.ProviderTypeCosy,
		Credentials: map[string]any{
			"security_oauth_token": "sec-token",
			"machine_id":           "machine-1",
			"uid":                  "uid-1",
		},
	}

	_, err := tokenSource.sessionBuilder(provider).GetOrganizationTags(context.Background(), QoderCredentialInput(provider), "sec-token", "uid-1")

	require.Error(t, err)
	errText := err.Error()
	require.Contains(t, errText, "status 500")
	require.NotContains(t, errText, "sec-secret")
	require.NotContains(t, errText, "rt-secret")
	require.NotContains(t, errText, "uid-secret")
	require.NotContains(t, errText, "sid=secret")
	require.Contains(t, errText, "***")
}

// TestCompositeTokenCacheInvalidator_QoderCosy starts with a cached Qoder
// entry and an external deletion recorder. It checks the provider key is sent
// to external deletion and the local session entry is evicted.
func TestCompositeTokenCacheInvalidator_QoderCosy(t *testing.T) {
	cache := &qoderInvalidationCache{}
	tokenSource := NewQoderTokenProvider(qoder.SessionBuilder{})
	tokenSource.Core.Sessions[42] = providercore.QoderSessionCacheEntry[*qoder.SessionContext]{CredentialsHash: "old"}
	invalidator := providercore.NewCompositeTokenCacheInvalidator(cache, tokenSource, nil)
	provider := &providercore.Record{
		ID:       42,
		Platform: capability.PlatformQoder,
		Type:     capability.ProviderTypeCosy,
	}

	err := invalidator.InvalidateToken(context.Background(), provider)

	require.NoError(t, err)
	require.Contains(t, cache.deletedKeys, "qoder:provider:42")
	tokenSource.Core.Mu.Lock()
	_, cached := tokenSource.Core.Sessions[42]
	tokenSource.Core.Mu.Unlock()
	require.False(t, cached, "qoder provider session cache should be invalidated too")
}

// TestCompositeTokenCacheInvalidator_QoderCosyInvalidatesProviderWithoutExternalCache
// omits the external cache while retaining local Qoder state. It checks
// invalidation still succeeds and removes the local provider session entry.
func TestCompositeTokenCacheInvalidator_QoderCosyInvalidatesProviderWithoutExternalCache(t *testing.T) {
	tokenSource := NewQoderTokenProvider(qoder.SessionBuilder{})
	tokenSource.Core.Sessions[43] = providercore.QoderSessionCacheEntry[*qoder.SessionContext]{CredentialsHash: "old"}
	invalidator := providercore.NewCompositeTokenCacheInvalidator(nil, tokenSource, nil)
	provider := &providercore.Record{
		ID:       43,
		Platform: capability.PlatformQoder,
		Type:     capability.ProviderTypeCosy,
	}

	err := invalidator.InvalidateToken(context.Background(), provider)

	require.NoError(t, err)
	tokenSource.Core.Mu.Lock()
	_, cached := tokenSource.Core.Sessions[43]
	tokenSource.Core.Mu.Unlock()
	require.False(t, cached)
}

// Do delegates response construction to DoWithTLS with a nil fingerprint
// profile. It records the same serial routing arguments and returns the
// configured caller-owned body for the site exchange or organization reader to
// consume.
func (s *qoderCenterHTTPUpstreamStub) Do(req *http.Request, proxyURL string, providerID int64, providerConcurrency int) (*http.Response, error) {
	return s.DoWithTLS(req, proxyURL, providerID, providerConcurrency, nil)
}

// DoWithTLS records serial routing and retains the request pointer for
// assertions. It returns configured body/status, defaulting empty body to
// identity JSON and zero status to HTTP 200. The caller owns the newly
// allocated response body.
func (s *qoderCenterHTTPUpstreamStub) DoWithTLS(req *http.Request, proxyURL string, providerID int64, providerConcurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	s.proxyURL = proxyURL
	s.providerID = providerID
	s.providerConcurrency = providerConcurrency
	s.profileSet = profile != nil
	s.requests = append(s.requests, req)
	body := s.body
	if body == "" {
		body = `{
			"id":"user-1",
			"name":"User",
			"userType":"personal_standard",
			"securityOauthToken":"dt-from-center",
			"refreshToken":"rt-from-center"
		}`
	}
	statusCode := s.statusCode
	if statusCode == 0 {
		statusCode = http.StatusOK
	}
	return &http.Response{
		StatusCode: statusCode,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

// ptrInt64ForQoderTest returns a pointer holding the supplied provider or
// proxy ID.
func ptrInt64ForQoderTest(v int64) *int64 {
	return &v
}

// DeleteAccessToken appends the key to the serial invalidation log and returns
// nil.
func (c *qoderInvalidationCache) DeleteAccessToken(_ context.Context, key string) error {
	c.deletedKeys = append(c.deletedKeys, key)
	return nil
}
