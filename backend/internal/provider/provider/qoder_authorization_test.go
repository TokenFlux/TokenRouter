package provider

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

// Qoder device authorization retains machine and PKCE identity in a stored
// site flow. These cases exercise qoder_authorization.go with Global and CN
// completion, checking session proof, creation-time proxy choice,
// user/organization precedence, pending reuse, completed-result replay and
// sanitized optional metadata failures.
//
// The concurrent case holds the first device poll while an ExchangeCode caller
// is launched for the same session proof. Buffered channels collect each caller's
// result and error after the upstream gate is released, and an atomic poll
// count checks that both callers reuse one supplier operation. Serial fixtures
// retain supplied result pointers while recording nonce, verifier and CN
// machine inputs, so the tests can distinguish browser-flow identity from
// later OpenAPI or COSY metadata.

// fakeQoderOAuthClient returns configured device and metadata results while
// recording serial calls.
type fakeQoderOAuthClient struct {
	// token, ready and pollErr are returned together from each device poll. The
	// token pointer is borrowed, and tests adjust readiness between serial calls
	// to model authorization becoming available without replacing the stored flow.
	token     *qoder.DeviceTokenResponse // configured device token result
	ready     bool                       // configured device readiness flag
	pollErr   error                      // configured device poll error
	pollCalls int                        // serial device poll count
	// userInfo and userErr describe the metadata query. A nonnil userErr returns
	// no user result; otherwise the configured userInfo pointer is returned
	// directly
	// for the site completion logic to interpret.
	userInfo *qoder.UserInfo // configured user metadata result
	userErr  error           // configured user query failure
	// orgTags and orgErr describe organization lookup after its organization argument and
	// call count are recorded. A nonnil orgErr suppresses the result; otherwise
	// callers receive the configured orgTags pointer without copying.
	orgTags     *qoder.OrganizationTags // configured organization tag result
	orgErr      error                   // configured organization query failure
	orgCalls    int                     // serial organization query count
	gotOrgID    string                  // last looked-up organization ID
	gotNonce    string                  // last polled authorization nonce
	gotVerifier string                  // last polled PKCE verifier
	// completedIdentity, completedExpiry and completionErr supply the CN20 status
	// completion result. The identity pointer remains shared; expiry and error
	// are returned by value after the method records its machine input.
	completedIdentity *qoder.AuthIdentity // configured CN status identity
	completedExpiry   time.Time           // configured CN token expiry
	completionErr     error               // configured CN completion failure
	completionCalls   int                 // serial CN completion count
	// completedMachine captures a new scalar-field copy of the latest nonnil
	// CN machine argument. A nil argument leaves any previous capture unchanged.
	completedMachine *qoder.MachineIdentity
}

// blockingQoderOAuthClient holds device completion until a test release or
// request cancellation.
type blockingQoderOAuthClient struct {
	// started announces the first poll entry and startOnce closes it once. The
	// test closes release to permit completion; until then each poll waits on
	// release or its own context, with pollCalls counting every entered invocation.
	started   chan struct{} // first device-poll entry signal
	release   chan struct{} // test-owned completion release gate
	startOnce sync.Once     // first-entry signal close ownership
	pollCalls atomic.Int32  // concurrent device poll count
}

// TestQoderOAuthServiceGenerateAuthURLCreatesSession generates a Global device
// URL. It checks stored state, nonce and verifier challenge, public
// expiry/poll interval, and identical machine_id in the browser URL and stored
// flow.
func TestQoderOAuthServiceGenerateAuthURLCreatesSession(t *testing.T) {
	svc := NewQoderAuthorization(func(context.Context, *int64) (string, error) { return "", nil }, nil)
	svc.Core.Start()
	t.Cleanup(func() { require.NoError(t, svc.Core.StopContext(context.Background())) })

	result, err := svc.GenerateAuthURLForSite(context.Background(), string(qoder.SiteGlobal), nil)
	require.NoError(t, err)
	require.NotEmpty(t, result.AuthURL)
	require.NotEmpty(t, result.SessionID)
	require.NotEmpty(t, result.State)
	require.Positive(t, result.ExpiresIn)
	require.Equal(t, provider.QoderOAuthPollInterval, result.Interval)

	session, ok := svc.Core.Store.Get(result.SessionID)
	require.True(t, ok)
	require.Equal(t, result.State, session.State)
	require.NotNil(t, session.Flow.Machine)
	require.Contains(t, result.AuthURL, "nonce="+session.Flow.Nonce)
	require.Contains(t, result.AuthURL, "challenge=")
	require.Contains(t, result.AuthURL, "client_id="+qoder.OAuthClientID)
	authURL, err := url.Parse(result.AuthURL)
	require.NoError(t, err)
	require.Equal(t, session.Flow.Machine.MachineID, authURL.Query().Get("machine_id"))
}

// TestQoderOAuthServiceCNFreezesSiteAndIgnoresPollProxyOverride completes a
// stored CN flow while passing another proxy ID to Poll. It checks retained
// profile/proxy, CN machine shape, status-organization precedence and returned
// CN20 token expiry.
func TestQoderOAuthServiceCNFreezesSiteAndIgnoresPollProxyOverride(t *testing.T) {
	svc := NewQoderAuthorization(func(context.Context, *int64) (string, error) { return "", nil }, nil)
	svc.Core.Start()
	t.Cleanup(func() { require.NoError(t, svc.Core.StopContext(context.Background())) })
	expiresAt := time.Now().Add(time.Hour).UTC().Truncate(time.Second)
	client := &fakeQoderOAuthClient{
		ready: true,
		token: &qoder.DeviceTokenResponse{
			Token:        "openapi-access",
			RefreshToken: "openapi-refresh",
			UserID:       "user-1",
			ExpiresAt:    qoder.FlexibleInt64(expiresAt.Unix()),
		},
		userInfo: &qoder.UserInfo{UserID: "user-1", UserName: "CN User", OrganizationID: "userinfo-org", OrganizationName: "Userinfo Org"},
		completedIdentity: &qoder.AuthIdentity{
			UID:                "cosy-uid",
			AID:                "cosy-aid",
			OrganizationID:     "status-org",
			OrganizationName:   "Status Org",
			SecurityOauthToken: "cosy-token",
			RefreshToken:       "openapi-refresh",
			UserType:           "personal_standard",
		},
		orgTags: &qoder.OrganizationTags{
			OrganizationID:   "tags-org",
			OrganizationName: "Tags Org",
		},
		completedExpiry: expiresAt,
	}
	var capturedProfile qoder.Profile
	var capturedProxy string
	svc.ClientFactory = func(profile qoder.Profile, proxyURL string) (qoder.OAuthClientPort, error) {
		capturedProfile = profile
		capturedProxy = proxyURL
		return client, nil
	}

	result, err := svc.GenerateAuthURLForSite(context.Background(), string(qoder.SiteCN), nil)
	require.NoError(t, err)
	require.Equal(t, "cn", result.Site)
	parsed, err := url.Parse(result.AuthURL)
	require.NoError(t, err)
	require.Equal(t, "qoder.cn", parsed.Host)
	require.Equal(t, qoder.CNOAuthClientID, parsed.Query().Get("client_id"))
	require.NotContains(t, parsed.Query().Get("nonce"), "-")
	require.Len(t, parsed.Query().Get("machine_id"), 36)
	session, ok := svc.Core.Store.Get(result.SessionID)
	require.True(t, ok)
	require.Empty(t, session.Flow.Machine.MachineToken)
	require.Empty(t, session.Flow.Machine.MachineType)

	ignoredProxyID := int64(9999)
	completed, err := svc.Poll(context.Background(), result.SessionID, result.State, &ignoredProxyID)
	require.NoError(t, err)
	require.Equal(t, "completed", completed.Status)
	require.Equal(t, qoder.SiteCN, capturedProfile.Site)
	require.Empty(t, capturedProxy)
	require.Zero(t, client.completionCalls)
	require.Equal(t, "openapi-access", completed.TokenInfo.SecurityOauthToken)
	require.Equal(t, "cn", completed.TokenInfo.Site)
	require.Equal(t, qoder.RefreshModeDeviceToken, completed.TokenInfo.RefreshMode)
	require.Equal(t, expiresAt.Format(time.RFC3339), completed.TokenInfo.ExpiresAt)
	require.Equal(t, "userinfo-org", completed.TokenInfo.OrganizationID)
	require.Equal(t, "Userinfo Org", completed.TokenInfo.OrganizationName)
	require.Zero(t, client.orgCalls)
	require.Nil(t, client.completedMachine)
	require.Equal(t, parsed.Query().Get("machine_id"), completed.TokenInfo.MachineID)
	require.Empty(t, completed.TokenInfo.MachineToken)
	require.Empty(t, completed.TokenInfo.MachineType)

	credentials := svc.Core.BuildProviderCredentials(completed.TokenInfo)
	require.Equal(t, "cn", credentials["site"])
	require.Equal(t, qoder.RefreshModeDeviceToken, credentials["refresh_mode"])
	require.Equal(t, expiresAt.Format(time.RFC3339), credentials["expires_at"])
	require.NotContains(t, credentials, "machine_token")
	require.NotContains(t, credentials, "machine_type")
}

// TestQoderOAuthServiceCNUsesOpenAPIOrganizationTags supplies a selected organization
// without a name. The optional tags lookup must address that organization,
// preserve its ID and fill the missing name.
func TestQoderOAuthServiceCNUsesOpenAPIOrganizationTags(t *testing.T) {
	svc := NewQoderAuthorization(func(context.Context, *int64) (string, error) { return "", nil }, nil)
	svc.Core.Start()
	t.Cleanup(func() { require.NoError(t, svc.Core.StopContext(context.Background())) })
	client := &fakeQoderOAuthClient{
		ready: true,
		token: &qoder.DeviceTokenResponse{
			Token:        "openapi-access",
			RefreshToken: "openapi-refresh",
			UserID:       "user-1",
			ExpiresIn:    3600,
		},
		userInfo: &qoder.UserInfo{UserID: "user-1", UserName: "CN User", OrganizationID: "tags-org"},
		completedIdentity: &qoder.AuthIdentity{
			UID:                "cosy-uid",
			AID:                "cosy-aid",
			SecurityOauthToken: "cosy-token",
			RefreshToken:       "openapi-refresh",
		},
		completedExpiry: time.Now().Add(time.Hour),
		orgTags: &qoder.OrganizationTags{
			OrganizationID:   "tags-org",
			OrganizationName: "Tags Org",
		},
	}
	svc.ClientFactory = func(_ qoder.Profile, _ string) (qoder.OAuthClientPort, error) {
		return client, nil
	}

	result, err := svc.GenerateAuthURLForSite(context.Background(), string(qoder.SiteCN), nil)
	require.NoError(t, err)
	completed, err := svc.Poll(context.Background(), result.SessionID, result.State, nil)
	require.NoError(t, err)
	require.Equal(t, "tags-org", completed.TokenInfo.OrganizationID)
	require.Equal(t, "Tags Org", completed.TokenInfo.OrganizationName)
	require.Equal(t, 1, client.orgCalls)
	require.Equal(t, "tags-org", client.gotOrgID)
}

// TestQoderOAuthServiceExchangeRejectsInvalidSessionState supplies a missing
// session then wrong state for an existing session. It checks distinct lookup
// and state-validation errors before authorization completion can return
// credentials.
func TestQoderOAuthServiceExchangeRejectsInvalidSessionState(t *testing.T) {
	svc := NewQoderAuthorization(func(context.Context, *int64) (string, error) { return "", nil }, nil)
	svc.Core.Start()
	t.Cleanup(func() { require.NoError(t, svc.Core.StopContext(context.Background())) })

	_, err := svc.ExchangeCode(context.Background(), &provider.QoderExchangeCodeInput{
		SessionID: "missing",
		State:     "state",
	})
	require.ErrorContains(t, err, "session not found")

	result, err := svc.GenerateAuthURLForSite(context.Background(), string(qoder.SiteGlobal), nil)
	require.NoError(t, err)
	_, err = svc.ExchangeCode(context.Background(), &provider.QoderExchangeCodeInput{
		SessionID: result.SessionID,
		State:     "wrong-state",
	})
	require.ErrorContains(t, err, "state is invalid")
}

// TestQoderOAuthServiceExchangePendingKeepsSession returns an unready device
// poll for a valid session. It checks the pending exchange error, retained
// session, and nonce/verifier taken from that stored authorization flow.
func TestQoderOAuthServiceExchangePendingKeepsSession(t *testing.T) {
	svc := NewQoderAuthorization(func(context.Context, *int64) (string, error) { return "", nil }, nil)
	svc.Core.Start()
	t.Cleanup(func() { require.NoError(t, svc.Core.StopContext(context.Background())) })
	client := &fakeQoderOAuthClient{ready: false}
	svc.ClientFactory = func(_ qoder.Profile, proxyURL string) (qoder.OAuthClientPort, error) {
		return client, nil
	}

	result, err := svc.GenerateAuthURLForSite(context.Background(), string(qoder.SiteGlobal), nil)
	require.NoError(t, err)
	_, err = svc.ExchangeCode(context.Background(), &provider.QoderExchangeCodeInput{
		SessionID: result.SessionID,
		State:     result.State,
		Code:      "completed",
	})
	require.ErrorContains(t, err, "still pending")

	_, ok := svc.Core.Store.Get(result.SessionID)
	require.True(t, ok, "pending authorization should keep the session available")
	session, _ := svc.Core.Store.Get(result.SessionID)
	require.Equal(t, session.Flow.Nonce, client.gotNonce)
	require.Equal(t, session.Flow.CodeVerifier, client.gotVerifier)
}

// TestQoderOAuthServiceExchangeParsesCallbackURLAndBuildsUsableCredentials
// completes a Global flow using callback state. It checks enriched
// user/organization fields, browser machine identity, returned credentials and
// successful runtime session construction.
func TestQoderOAuthServiceExchangeParsesCallbackURLAndBuildsUsableCredentials(t *testing.T) {
	svc := NewQoderAuthorization(func(context.Context, *int64) (string, error) { return "", nil }, nil)
	svc.Core.Start()
	t.Cleanup(func() { require.NoError(t, svc.Core.StopContext(context.Background())) })
	expiresAt := time.Unix(1_893_456_000, 0).UTC()
	svc.ClientFactory = func(_ qoder.Profile, proxyURL string) (qoder.OAuthClientPort, error) {
		return &fakeQoderOAuthClient{
			ready: true,
			token: &qoder.DeviceTokenResponse{
				Token:        "security-token",
				RefreshToken: "refresh-token",
				UserID:       "user-from-token",
				ExpiresAt:    qoder.FlexibleInt64(expiresAt.Unix()),
			},
			userInfo: &qoder.UserInfo{
				ID:             "user-from-info",
				OrganizationID: "org-from-tags",
				Name:           "Qoder User",
				UserType:       "personal_pro",
			},
			orgTags: &qoder.OrganizationTags{
				OrganizationID:   "org-from-tags",
				OrganizationName: "Qoder Org",
			},
		}, nil
	}

	result, err := svc.GenerateAuthURLForSite(context.Background(), string(qoder.SiteGlobal), nil)
	require.NoError(t, err)
	authURL, err := url.Parse(result.AuthURL)
	require.NoError(t, err)
	authMachineID := authURL.Query().Get("machine_id")
	require.NotEmpty(t, authMachineID)
	tokenInfo, err := svc.ExchangeCode(context.Background(), &provider.QoderExchangeCodeInput{
		SessionID:   result.SessionID,
		CallbackURL: "http://localhost:12345/callback?code=ignored-by-device-flow&state=" + result.State,
	})
	require.NoError(t, err)
	require.Equal(t, "security-token", tokenInfo.SecurityOauthToken)
	require.Equal(t, "refresh-token", tokenInfo.RefreshToken)
	require.Equal(t, "user-from-info", tokenInfo.UID)
	require.Equal(t, "user-from-info", tokenInfo.AID)
	require.Equal(t, "org-from-tags", tokenInfo.OrganizationID)
	require.Equal(t, "Qoder Org", tokenInfo.OrganizationName)
	require.Equal(t, "Qoder User", tokenInfo.Name)
	require.Equal(t, "personal_pro", tokenInfo.UserType)
	require.Equal(t, "global", tokenInfo.Site)
	require.Equal(t, qoder.RefreshModeDeviceToken, tokenInfo.RefreshMode)
	require.Equal(t, expiresAt.Format(time.RFC3339), tokenInfo.ExpiresAt)
	require.Equal(t, authMachineID, tokenInfo.MachineID)
	require.NotEmpty(t, tokenInfo.MachineToken)
	require.NotEmpty(t, tokenInfo.MachineType)

	sessionAfterComplete, ok := svc.Core.Store.Get(result.SessionID)
	require.True(t, ok, "completed authorization should remain available for idempotent retry")
	require.NotNil(t, sessionAfterComplete.CompletedTokenInfo)

	credentials := svc.Core.BuildProviderCredentials(tokenInfo)
	require.Equal(t, "security-token", credentials["security_oauth_token"])
	require.Equal(t, tokenInfo.MachineID, credentials["machine_id"])
	require.Equal(t, "org-from-tags", credentials["organization_id"])
	require.Equal(t, "Qoder Org", credentials["organization_name"])
	require.Equal(t, expiresAt.Format(time.RFC3339), credentials["expires_at"])

	sessions := &provider.QoderSessions[*qoder.SessionContext]{}
	session, err := sessions.GetSession(context.Background(), &provider.Record{
		ID:          991,
		Name:        "qoder-oauth",
		Platform:    capability.PlatformQoder,
		Type:        capability.ProviderTypeCosy,
		Credentials: credentials,
	}, func(ctx context.Context, value *provider.Record) (*qoder.SessionContext, time.Time, error) {
		// Reconstruct the runtime input from exchanged credential fields,
		// exercising
		// the session builder with the same machine and organization identity
		// returned by authorization.
		return (&qoder.SessionBuilder{}).BuildSession(ctx, &qoder.CredentialInput{
			Name:               value.Name,
			Site:               value.GetCredential("site"),
			RefreshMode:        value.GetCredential("refresh_mode"),
			SecurityOauthToken: value.GetCredential("security_oauth_token"),
			MachineId:          value.GetCredential("machine_id"),
			MachineToken:       value.GetCredential("machine_token"),
			MachineType:        value.GetCredential("machine_type"),
			Uid:                value.GetCredential("uid"),
			Aid:                value.GetCredential("aid"),
			IdentityName:       value.GetCredential("name"),
			UserType:           value.GetCredential("user_type"),
			RefreshToken:       value.GetCredential("refresh_token"),
			OrganizationId:     value.GetCredential("organization_id"),
			OrganizationName:   value.GetCredential("organization_name"),
		})
	})
	require.NoError(t, err)
	require.Equal(t, "security-token", session.Identity.SecurityOauthToken)
	require.Equal(t, "org-from-tags", session.Identity.OrganizationID)
	require.Equal(t, "Qoder Org", session.Identity.OrganizationName)
	require.Equal(t, tokenInfo.MachineID, session.Machine.MachineID)
	require.Equal(t, tokenInfo.MachineToken, session.Machine.MachineToken)
}

// TestQoderOAuthServicePollReturnsPendingAndCompleted changes the fixture from
// pending to ready with metadata failures. It checks both poll states,
// token/user fallback, machine identity and a sanitized userinfo warning.
// No organization lookup is possible without a selected organization ID.
func TestQoderOAuthServicePollReturnsPendingAndCompleted(t *testing.T) {
	svc := NewQoderAuthorization(func(context.Context, *int64) (string, error) { return "", nil }, nil)
	svc.Core.Start()
	t.Cleanup(func() { require.NoError(t, svc.Core.StopContext(context.Background())) })
	client := &fakeQoderOAuthClient{ready: false}
	svc.ClientFactory = func(_ qoder.Profile, proxyURL string) (qoder.OAuthClientPort, error) {
		return client, nil
	}

	result, err := svc.GenerateAuthURLForSite(context.Background(), string(qoder.SiteGlobal), nil)
	require.NoError(t, err)
	authURL, err := url.Parse(result.AuthURL)
	require.NoError(t, err)
	pending, err := svc.Poll(context.Background(), result.SessionID, result.State, nil)
	require.NoError(t, err)
	require.Equal(t, "pending", pending.Status)
	require.Nil(t, pending.TokenInfo)

	client.ready = true
	client.token = &qoder.DeviceTokenResponse{AccessToken: "access-token", RefreshToken: "refresh-token", UserID: "user-1", ExpiresIn: 3600}
	client.userErr = errors.New("userinfo unavailable")
	client.orgErr = errors.New("organization unavailable")
	completed, err := svc.Poll(context.Background(), result.SessionID, result.State, nil)
	require.NoError(t, err)
	require.Equal(t, "completed", completed.Status)
	require.Equal(t, "access-token", completed.TokenInfo.SecurityOauthToken)
	require.Equal(t, authURL.Query().Get("machine_id"), completed.TokenInfo.MachineID)
	require.Equal(t, "user-1", completed.TokenInfo.UID)
	require.Equal(t, map[string]string{
		"code":    "userinfo_unavailable",
		"message": "Qoder user info could not be loaded",
	}, completed.TokenInfo.Extra["userinfo_warning"])
	require.NotContains(t, completed.TokenInfo.Extra, "organization_warning")
	require.Zero(t, client.orgCalls)
}

// TestQoderOAuthServiceCompletedSessionIsIdempotent polls to completion before
// an ExchangeCode call on the same proof. It checks the cached access result
// and one device poll rather than another supplier completion.
func TestQoderOAuthServiceCompletedSessionIsIdempotent(t *testing.T) {
	svc := NewQoderAuthorization(func(context.Context, *int64) (string, error) { return "", nil }, nil)
	svc.Core.Start()
	t.Cleanup(func() { require.NoError(t, svc.Core.StopContext(context.Background())) })
	client := &fakeQoderOAuthClient{
		ready: true,
		token: &qoder.DeviceTokenResponse{
			AccessToken:  "access-token",
			RefreshToken: "refresh-token",
			ExpiresIn:    3600,
			UserID:       "user-1",
		},
		userInfo: &qoder.UserInfo{ID: "user-1", Name: "Qoder User"},
	}
	svc.ClientFactory = func(_ qoder.Profile, proxyURL string) (qoder.OAuthClientPort, error) {
		return client, nil
	}

	result, err := svc.GenerateAuthURLForSite(context.Background(), string(qoder.SiteGlobal), nil)
	require.NoError(t, err)
	completed, err := svc.Poll(context.Background(), result.SessionID, result.State, nil)
	require.NoError(t, err)
	require.Equal(t, "completed", completed.Status)
	require.Equal(t, "access-token", completed.TokenInfo.SecurityOauthToken)

	tokenInfo, err := svc.ExchangeCode(context.Background(), &provider.QoderExchangeCodeInput{
		SessionID: result.SessionID,
		State:     result.State,
		Code:      "ignored-by-device-flow",
	})
	require.NoError(t, err)
	require.Equal(t, "access-token", tokenInfo.SecurityOauthToken)
	require.Equal(t, 1, client.pollCalls, "completed session should return cached token info")
}

// TestQoderOAuthServiceConcurrentCompletionReusesSingleResult holds one device
// poll while another caller exchanges the same session. It checks no second
// result during the observation interval, then successful results and one
// supplier poll after release.
func TestQoderOAuthServiceConcurrentCompletionReusesSingleResult(t *testing.T) {
	svc := NewQoderAuthorization(func(context.Context, *int64) (string, error) { return "", nil }, nil)
	svc.Core.Start()
	t.Cleanup(func() { require.NoError(t, svc.Core.StopContext(context.Background())) })
	client := &blockingQoderOAuthClient{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	svc.ClientFactory = func(_ qoder.Profile, proxyURL string) (qoder.OAuthClientPort, error) {
		return client, nil
	}

	result, err := svc.GenerateAuthURLForSite(context.Background(), string(qoder.SiteGlobal), nil)
	require.NoError(t, err)

	pollDone := make(chan *provider.QoderPollResult, 1)
	pollErr := make(chan error, 1)
	// Start the first completion and wait for actual supplier poll entry before
	// introducing the second caller, keeping the core claim active at that
	// interleaving.
	go func() {
		pollResult, pollErrValue := svc.Poll(context.Background(), result.SessionID, result.State, nil)
		pollDone <- pollResult
		pollErr <- pollErrValue
	}()
	<-client.started

	exchangeDone := make(chan *provider.QoderTokenInfo, 1)
	exchangeErr := make(chan error, 1)
	// Exchange the same proof while polling owns completion. Separate buffered
	// result/error channels let both callers finish after release before
	// assertions join them.
	go func() {
		tokenInfo, exchangeErrValue := svc.ExchangeCode(context.Background(), &provider.QoderExchangeCodeInput{
			SessionID: result.SessionID,
			State:     result.State,
			Code:      "ignored-by-device-flow",
		})
		exchangeDone <- tokenInfo
		exchangeErr <- exchangeErrValue
	}()

	// A short observation interval exposes premature second-caller completion while
	// the supplier is held; the interval itself does not prove the waiter
	// registered.
	select {
	case <-exchangeDone:
		t.Fatal("exchange should wait for the in-flight completion")
	case <-time.After(20 * time.Millisecond):
	}

	// Let the owned supplier completion finish, then collect both caller results
	// before reading the atomic count so result reuse is checked after actual
	// exits.
	close(client.release)
	pollResult := <-pollDone
	require.NoError(t, <-pollErr)
	require.Equal(t, "completed", pollResult.Status)

	tokenInfo := <-exchangeDone
	require.NoError(t, <-exchangeErr)
	require.Equal(t, "access-token", tokenInfo.SecurityOauthToken)
	require.Equal(t, int32(1), client.pollCalls.Load())
}

// TestQoderOAuthServiceWarningsDoNotPersistRawUpstreamErrors encodes optional
// metadata warnings from errors containing secret identity text. It checks
// stable warning codes are retained while token, email and upstream provider
// ID are absent.
func TestQoderOAuthServiceWarningsDoNotPersistRawUpstreamErrors(t *testing.T) {
	rawErr := errors.New(`upstream 500 {"access_token":"secret-token","email":"user@example.com","provider_id":"aid-123"}`)

	tokenInfo := qoder.BuildAuthorizationTokenInfoForSite(&qoder.AuthIdentity{
		SecurityOauthToken: "security-token",
		UID:                "user-1",
	}, &qoder.MachineIdentity{MachineID: "machine-1"}, qoder.SiteGlobal, qoder.RefreshModeCosy, time.Time{}, rawErr, rawErr)
	body, err := json.Marshal(tokenInfo.Extra)
	require.NoError(t, err)

	require.NotContains(t, string(body), "secret-token")
	require.NotContains(t, string(body), "user@example.com")
	require.NotContains(t, string(body), "aid-123")
	require.Contains(t, string(body), "userinfo_unavailable")
	require.Contains(t, string(body), "organization_unavailable")
}

// TestQoderParseCallbackSupportsQueryFragmentAndPlainCode supplies query,
// fragment and bare-code inputs to callback parsing. It checks extracted
// state/code pairs and the empty state associated with a plain authorization
// code.
func TestQoderParseCallbackSupportsQueryFragmentAndPlainCode(t *testing.T) {
	state, code := provider.ParseQoderCallback("http://localhost/callback?code=query-code&state=query-state")
	require.Equal(t, "query-state", state)
	require.Equal(t, "query-code", code)

	state, code = provider.ParseQoderCallback("http://localhost/callback#code=fragment-code&state=fragment-state")
	require.Equal(t, "fragment-state", state)
	require.Equal(t, "fragment-code", code)

	state, code = provider.ParseQoderCallback("plain-code")
	require.Empty(t, state)
	require.Equal(t, "plain-code", code)
}

// PollDeviceToken records nonce/verifier and returns configured token,
// readiness and error. It increments a serial counter and borrows the
// configured token pointer. Context is unused by this fixture; the blocking
// fixture models cancellation separately.
func (f *fakeQoderOAuthClient) PollDeviceToken(ctx context.Context, nonce, verifier string) (*qoder.DeviceTokenResponse, bool, error) {
	f.pollCalls++
	f.gotNonce = nonce
	f.gotVerifier = verifier
	return f.token, f.ready, f.pollErr
}

// GetUserInfo returns the configured metadata pointer or the configured query
// error. A nonnil userErr suppresses userInfo. Context and token are unused,
// and returned metadata remains borrowed from the serial fixture for
// completion assertions.
func (f *fakeQoderOAuthClient) GetUserInfo(ctx context.Context, token string) (*qoder.UserInfo, error) {
	if f.userErr != nil {
		return nil, f.userErr
	}
	return f.userInfo, nil
}

// GetOrganizationTags records lookup UID and returns configured organization
// metadata. Call count and UID are updated even when orgErr is supplied. A
// successful result borrows orgTags, while context/token inputs are unused by
// this serial fixture.
func (f *fakeQoderOAuthClient) GetOrganizationTags(ctx context.Context, token, uid string) (*qoder.OrganizationTags, error) {
	f.orgCalls++
	f.gotOrgID = uid
	if f.orgErr != nil {
		return nil, f.orgErr
	}
	return f.orgTags, nil
}

// CompleteQoderCN20Identity records CN completion and copies a nonnil machine
// argument. It returns the configured identity pointer, expiry and error after
// that capture. Nil machine leaves the prior capture intact; configured
// identity remains borrowed rather than cloned for the caller.
func (f *fakeQoderOAuthClient) CompleteQoderCN20Identity(
	_ context.Context,
	_ *qoder.DeviceTokenResponse,
	_ *qoder.UserInfo,
	machine *qoder.MachineIdentity,
) (*qoder.AuthIdentity, time.Time, error) {
	f.completionCalls++
	if machine != nil {
		f.completedMachine = &qoder.MachineIdentity{
			MachineID:    machine.MachineID,
			MachineToken: machine.MachineToken,
			MachineType:  machine.MachineType,
		}
	}
	return f.completedIdentity, f.completedExpiry, f.completionErr
}

// PollDeviceToken counts entry and waits for the test gate or caller
// cancellation. The first entry closes started through startOnce, allowing
// assertions to begin only after a supplier poll was reached. Release returns
// a fresh ready token; cancellation returns ctx.Err with no token.
//
// When release and cancellation are simultaneously ready, select can choose
// either branch. The atomic counter records all entered polls, while startOnce
// prevents repeated signal closure, allowing the completion-sharing case to
// check supplier call count independently from each caller result.
func (f *blockingQoderOAuthClient) PollDeviceToken(ctx context.Context, nonce, verifier string) (*qoder.DeviceTokenResponse, bool, error) {
	f.pollCalls.Add(1)
	// Several callers can reach this fixture. Close its entry signal once while
	// counting every invocation, so a duplicate completion becomes an
	// assertion rather than a panic.
	f.startOnce.Do(func() { close(f.started) })
	select {
	case <-ctx.Done():
		return nil, false, ctx.Err()
	case <-f.release:
		return &qoder.DeviceTokenResponse{
			AccessToken:  "access-token",
			RefreshToken: "refresh-token",
			ExpiresIn:    3600,
			UserID:       "user-1",
		}, true, nil
	}
}

// GetUserInfo returns a fresh fixed user identity for the completed blocking
// poll.
func (f *blockingQoderOAuthClient) GetUserInfo(ctx context.Context, token string) (*qoder.UserInfo, error) {
	return &qoder.UserInfo{ID: "user-1", Name: "Qoder User"}, nil
}

// GetOrganizationTags returns no optional organization metadata and no query
// error. This keeps the concurrency fixture focused on its completion gate
// while the platform completion logic retains the token's usable user
// identity.
func (f *blockingQoderOAuthClient) GetOrganizationTags(ctx context.Context, token, uid string) (*qoder.OrganizationTags, error) {
	return nil, nil
}

// TestDesktopAuthorizationAcceptsAccessOnly grants a usable access token while
// omitting refresh data or the expiry. Completion retains the verified account
// and preserves just the optional renewal fields actually supplied upstream.
func TestDesktopAuthorizationAcceptsAccessOnly(t *testing.T) {
	for _, token := range []*qoder.DeviceTokenResponse{{Token: "fixture", ExpiresIn: 3600}, {Token: "fixture", RefreshToken: "renew"}, {Token: "fixture"}} {
		client := &fakeQoderOAuthClient{ready: true, token: token, userInfo: &qoder.UserInfo{ID: "user", OrganizationID: "org"}}
		result, pending, err := qoder.CompleteAuthorization(t.Context(), &qoder.AuthorizationFlow{Site: qoder.SiteGlobal}, func(qoder.Profile, string) (qoder.OAuthClientPort, error) { return client, nil })
		require.NoError(t, err)
		require.False(t, pending)
		require.Equal(t, "user", result.UID)
		require.Equal(t, token.RefreshToken, result.RefreshToken)
		require.Equal(t, token.ExpiresIn > 0, result.ExpiresAt != "")
	}
}

// TestQoderNestedOrganizationInvalidatesPriorCatalog authorizes the same user in
// two workspaces. Each completion must retain the profile's organization, enrich
// its name by organization ID, and reject the previous organization's snapshot.
func TestQoderNestedOrganizationInvalidatesPriorCatalog(t *testing.T) {
	for _, site := range []qoder.Site{qoder.SiteCN, qoder.SiteGlobal} {
		row := &provider.Record{ID: 7, Platform: "qoder", Type: "cosy"}
		for _, org := range []string{"org-a", "org-b"} {
			var profile qoder.UserInfo
			raw, err := json.Marshal(map[string]any{"uid": "shared-user", "organization": map[string]string{"id": org}})
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(raw, &profile))
			client := &fakeQoderOAuthClient{ready: true, token: &qoder.DeviceTokenResponse{Token: "fixture", UserID: "shared-user"}, userInfo: &profile, orgTags: &qoder.OrganizationTags{OrganizationID: org, OrganizationName: "Workspace"}}
			token, _, err := qoder.CompleteAuthorization(t.Context(), &qoder.AuthorizationFlow{Site: site}, func(qoder.Profile, string) (qoder.OAuthClientPort, error) { return client, nil })
			require.NoError(t, err)
			require.Equal(t, org, client.gotOrgID)
			require.Equal(t, org, token.OrganizationID)
			require.Equal(t, "Workspace", token.OrganizationName)
			row.Credentials = map[string]any{"uid": token.UID, "site": token.Site, "organization_id": token.OrganizationID}
			if row.Extra == nil {
				row.Extra = map[string]any{provider.QoderCatalogExtraKey: map[string]any{"identity": provider.QoderCatalogIdentity(row), "version": "A", "fetched_at": time.Now(), "models": []any{}}}
				row.PrepareQoderCatalog()
				_, err = row.QoderModelIndex()
				require.NoError(t, err)
			} else {
				_, err = row.QoderModelIndex()
				require.ErrorIs(t, err, provider.ErrQoderCatalogUnavailable)
			}
		}
	}
}
