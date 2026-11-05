package provider

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/pkg/querycache"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// Grok authorization validates state and redirect proof before consuming its
// one-time session, while token conversion selects metadata from access and ID
// tokens according to their roles. These cases exercise grok_authorization.go
// bindings through core authorization and token readers, checking
// refresh-token retention, subscription hints, temporary password/SSO inputs
// and manual probe rules.
//
// Lock-contention cases delay a replacement row to distinguish a valid rotated
// database token from stale or unbounded cached data. The repository mutex
// coordinates row lookup with row replacement; the cache mutex protects read
// and write observations, while configured lock ownership and release counters
// have separate fixture rules. Successful token return observes replacement
// state but is not a join of the writer goroutine. These fixtures model
// selected interleavings without claiming complete database or cache behavior.

// grokOAuthClientStub records serial authorization inputs and returns
// configured exchange response pointers.
type grokOAuthClientStub struct {
	// refreshResponse is returned directly by RefreshToken, including nil. The
	// core conversion path determines whether that configured result is a
	// usable token response.
	refreshResponse *xai.TokenResponse
	// ssoResponse is returned directly by ConvertSSOToBuild, including nil.
	// The fixture retains ownership of the supplied pointer used for
	// claim-conversion assertions.
	ssoResponse *xai.TokenResponse
	// loginResult is returned by password login after email/password capture.
	// The configured pointer can be nil; authorization determines whether it
	// permits SSO conversion.
	loginResult         *providercore.GrokPasswordLoginResult
	loginEmail          string // last password-login email input
	loginPassword       string // last untrimmed password input
	exchangeCalls       int    // serial authorization exchange count
	exchangeRedirectURI string // last exchanged redirect URI
}

// grokTokenCacheForProviderTest separates simulated cache reads from recorded
// writes and lock outcomes.
type grokTokenCacheForProviderTest struct {
	// mu protects token, getCalls, setKey, setToken, setTTL and deletedKeys in
	// fixture methods and explicit writer updates. lockResult and deleteErr are
	// configured before calls; releaseCalls counts serial release notifications
	// without taking this mutex. Recorded writes do not replace the read token.
	token        string        // simulated cache-read token
	setKey       string        // last recorded write key
	setToken     string        // last recorded write token
	setTTL       time.Duration // last recorded token lifetime
	lockResult   bool          // configured refresh-lock ownership result
	releaseCalls int           // serial lock-release notification count
	deletedKeys  []string      // recorded cache deletion keys
	deleteErr    error         // configured cache deletion error
	getCalls     int           // mutex-protected cache read count
	mu           sync.Mutex    // cache observation synchronization mutex
}

// grokCredentialRaceRepo coordinates fixture row replacement with delegated
// lookup during token waiting.
type grokCredentialRaceRepo struct {
	*tokenRefreshProviderRepo // borrowed refresh repository fixture
	// mu excludes row replacement while lookup delegates to the base fixture.
	// Its read lock permits concurrent lookups, so it does not serialize the
	// base fixture getByIDCalls counter or other independently reached base
	// methods.
	mu sync.RWMutex
}

// TestGrokOAuthServiceRefreshTokenPreservesOriginalRefreshTokenWhenNotRotated
// returns access data without a replacement refresh token. It checks the saved
// refresh credential and explicit client ID remain on the converted
// authorization result.
func TestGrokOAuthServiceRefreshTokenPreservesOriginalRefreshTokenWhenNotRotated(t *testing.T) {
	svc := newGrokAuthorizationForTest(nil, &grokOAuthClientStub{
		refreshResponse: &xai.TokenResponse{
			AccessToken: "new-access-token",
			TokenType:   "Bearer",
			ExpiresIn:   3600,
		},
	})
	svc.Start()
	defer stopGrokAuthorizationForTest(t, svc)

	info, err := svc.RefreshToken(context.Background(), "original-refresh-token", "", "client-id")
	require.NoError(t, err)
	require.Equal(t, "new-access-token", info.AccessToken)
	require.Equal(t, "original-refresh-token", info.RefreshToken)
	require.Equal(t, "client-id", info.ClientID)
}

// TestGrokOAuthServiceRefreshTokenRejectsEmptyUpstreamResponse supplies a nil
// token response from the fixture. It checks refresh reports the stable
// invalid-token response error with nil result and no conversion panic.
func TestGrokOAuthServiceRefreshTokenRejectsEmptyUpstreamResponse(t *testing.T) {
	svc := newGrokAuthorizationForTest(nil, &grokOAuthClientStub{})
	svc.Start()
	defer stopGrokAuthorizationForTest(t, svc)

	require.NotPanics(t, func() {
		info, err := svc.RefreshToken(context.Background(), "refresh-token", "", "client-id")
		require.Nil(t, info)
		require.Error(t, err)
		require.Contains(t, err.Error(), "GROK_OAUTH_INVALID_TOKEN_RESPONSE")
	})
}

// TestGrokOAuthServiceExchangeCodeConsumesOnlyAfterValidation submits missing
// state before a valid exchange and replay. It checks validation performs no
// exchange, success exchanges once, and replay fails after session
// consumption.
func TestGrokOAuthServiceExchangeCodeConsumesOnlyAfterValidation(t *testing.T) {
	client := &grokOAuthClientStub{}
	svc := newGrokAuthorizationForTest(nil, client)
	svc.Start()
	defer stopGrokAuthorizationForTest(t, svc)

	auth, err := svc.GenerateAuthURL(context.Background(), nil, "")
	require.NoError(t, err)

	_, err = svc.ExchangeCode(context.Background(), &providercore.GrokExchangeCodeInput{
		SessionID: auth.SessionID,
		Code:      "http://127.0.0.1:56121/callback?code=code-without-state",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "GROK_OAUTH_STATE_REQUIRED")
	require.Zero(t, client.exchangeCalls)

	_, err = svc.ExchangeCode(context.Background(), &providercore.GrokExchangeCodeInput{
		SessionID: auth.SessionID,
		Code:      "code-with-state",
		State:     auth.State,
	})
	require.NoError(t, err)
	require.Equal(t, 1, client.exchangeCalls)

	_, err = svc.ExchangeCode(context.Background(), &providercore.GrokExchangeCodeInput{
		SessionID: auth.SessionID,
		Code:      "replayed-code",
		State:     auth.State,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "GROK_OAUTH_SESSION_NOT_FOUND")
	require.Equal(t, 1, client.exchangeCalls)
}

// TestGrokOAuthServiceExchangeCodeRejectsMissingClientWithoutConsumingSession
// uses a valid session with no exchange client. It checks the configuration
// error and that the session remains stored after that rejected attempt.
func TestGrokOAuthServiceExchangeCodeRejectsMissingClientWithoutConsumingSession(t *testing.T) {
	svc := newGrokAuthorizationForTest(nil, nil)
	svc.Start()
	defer stopGrokAuthorizationForTest(t, svc)
	auth, err := svc.GenerateAuthURL(context.Background(), nil, "")
	require.NoError(t, err)

	_, err = svc.ExchangeCode(context.Background(), &providercore.GrokExchangeCodeInput{
		SessionID: auth.SessionID,
		Code:      "code",
		State:     auth.State,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "GROK_OAUTH_CLIENT_NOT_CONFIGURED")
	_, ok := svc.Store.Get(auth.SessionID)
	require.True(t, ok)
}

// TestGrokOAuthServiceExchangeCodeRequiresStateForBareCode supplies a bare
// code with no state. It checks the required-state diagnostic, zero supplier
// exchanges and retention of the session for a corrected exchange call.
func TestGrokOAuthServiceExchangeCodeRequiresStateForBareCode(t *testing.T) {
	client := &grokOAuthClientStub{}
	svc := newGrokAuthorizationForTest(nil, client)
	svc.Start()
	defer stopGrokAuthorizationForTest(t, svc)
	auth, err := svc.GenerateAuthURL(context.Background(), nil, "")
	require.NoError(t, err)

	_, err = svc.ExchangeCode(context.Background(), &providercore.GrokExchangeCodeInput{
		SessionID: auth.SessionID,
		Code:      "bare-authorization-code",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "GROK_OAUTH_STATE_REQUIRED")
	require.Zero(t, client.exchangeCalls)
	_, ok := svc.Store.Get(auth.SessionID)
	require.True(t, ok)
}

// TestGrokOAuthServiceExchangeCodeRejectsRedirectURIOverride tries another
// callback URI before the expected redirect. It checks mismatch rejection
// before exchange, then the stored/default redirect reaches the one accepted
// client exchange.
func TestGrokOAuthServiceExchangeCodeRejectsRedirectURIOverride(t *testing.T) {
	client := &grokOAuthClientStub{}
	svc := newGrokAuthorizationForTest(nil, client)
	svc.Start()
	defer stopGrokAuthorizationForTest(t, svc)
	auth, err := svc.GenerateAuthURL(context.Background(), nil, "")
	require.NoError(t, err)

	_, err = svc.ExchangeCode(context.Background(), &providercore.GrokExchangeCodeInput{
		SessionID:   auth.SessionID,
		Code:        "authorization-code",
		State:       auth.State,
		RedirectURI: "http://127.0.0.1:9999/callback",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "GROK_OAUTH_REDIRECT_URI_MISMATCH")
	require.Zero(t, client.exchangeCalls)

	_, err = svc.ExchangeCode(context.Background(), &providercore.GrokExchangeCodeInput{
		SessionID:   auth.SessionID,
		Code:        "authorization-code",
		State:       auth.State,
		RedirectURI: xai.DefaultRedirectURI,
	})
	require.NoError(t, err)
	require.Equal(t, xai.DefaultRedirectURI, client.exchangeRedirectURI)
}

// TestGrokOAuthServiceExternalFlowsRejectMissingClient runs refresh and SSO
// validation without a supplier client. It checks both operations return an
// error with the stable missing-client reason for that configuration.
func TestGrokOAuthServiceExternalFlowsRejectMissingClient(t *testing.T) {
	svc := newGrokAuthorizationForTest(nil, nil)
	svc.Start()
	defer stopGrokAuthorizationForTest(t, svc)

	_, err := svc.RefreshToken(context.Background(), "refresh-token", "", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "GROK_OAUTH_CLIENT_NOT_CONFIGURED")

	_, err = svc.ValidateSSOToken(context.Background(), "sso-token", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "GROK_OAUTH_CLIENT_NOT_CONFIGURED")
}

// TestGrokOAuthServiceBuildProviderCredentialsDefaultsToSubscriptionProxy
// builds a credential map from access data with no endpoint override. It
// checks base_url uses the platform CLI subscription endpoint for that OAuth
// result.
func TestGrokOAuthServiceBuildProviderCredentialsDefaultsToSubscriptionProxy(t *testing.T) {
	svc := newGrokAuthorizationForTest(nil, &grokOAuthClientStub{})
	svc.Start()
	defer stopGrokAuthorizationForTest(t, svc)

	credentials := svc.BuildProviderCredentials(&providercore.GrokTokenInfo{
		AccessToken: "access-token",
		ExpiresAt:   time.Now().Add(time.Hour).Unix(),
	})

	require.Equal(t, xai.DefaultCLIBaseURL, credentials["base_url"])
}

// TestGrokOAuthServiceConvertFromSSOExtractsBuildClaims returns access
// user/team/tier claims and an ID-token email hint. It checks converted and
// credential-map metadata, Heavy labeling and omission of the temporary SSO
// input.
func TestGrokOAuthServiceConvertFromSSOExtractsBuildClaims(t *testing.T) {
	svc := newGrokAuthorizationForTest(nil, &grokOAuthClientStub{
		ssoResponse: &xai.TokenResponse{
			AccessToken:  makeGrokOAuthJWT(map[string]any{"sub": "user-sub", "team_id": "team-1", "tier": 5}),
			RefreshToken: "refresh-token",
			IDToken:      makeGrokOAuthJWT(map[string]any{"email": "user@example.com"}),
			ExpiresIn:    3600,
		},
	})
	svc.Start()
	defer stopGrokAuthorizationForTest(t, svc)

	info, err := svc.ConvertFromSSO(context.Background(), "sso-token", nil)
	require.NoError(t, err)
	require.Equal(t, "user@example.com", info.Email)
	require.Equal(t, "user-sub", info.Subject)
	require.Equal(t, "team-1", info.TeamID)
	require.Equal(t, "supergrok_heavy", info.SubscriptionTier)

	credentials := svc.BuildProviderCredentials(info)
	require.Equal(t, "user@example.com", credentials["email"])
	require.Equal(t, "user-sub", credentials["sub"])
	require.Equal(t, "team-1", credentials["team_id"])
	require.Equal(t, "supergrok_heavy", credentials["subscription_tier"])
	require.NotContains(t, credentials, "sso_token")
}

// TestGrokOAuthServiceRefreshProviderTokenOverwritesStaleTierFromNewJWT
// returns a Free access-token tier over stored Heavy metadata. It checks that
// converted and built credentials use the newly observed access-token tier.
func TestGrokOAuthServiceRefreshProviderTokenOverwritesStaleTierFromNewJWT(t *testing.T) {
	svc := newGrokAuthorizationForTest(nil, &grokOAuthClientStub{
		refreshResponse: &xai.TokenResponse{
			AccessToken: makeGrokOAuthJWT(map[string]any{"sub": "user-sub", "tier": 0}),
			TokenType:   "Bearer",
			ExpiresIn:   3600,
		},
	})
	svc.Start()
	defer stopGrokAuthorizationForTest(t, svc)

	provider := &providercore.Record{
		Platform: capability.PlatformGrok,
		Type:     capability.ProviderTypeOAuth,
		Credentials: map[string]any{
			"refresh_token":     "refresh-token",
			"client_id":         "client-id",
			"subscription_tier": "supergrok_heavy",
		},
	}

	info, err := svc.RefreshProviderToken(context.Background(), provider)
	require.NoError(t, err)
	require.Equal(t, "free", info.SubscriptionTier)

	credentials := svc.BuildProviderCredentials(info)
	require.Equal(t, "free", credentials["subscription_tier"])
}

// TestGrokOAuthServiceRefreshProviderTokenIgnoresIDTokenTierWhenAccessTokenHasNone
// uses opaque access input with a Heavy ID-token claim and stored Lite label.
// It checks the stored Lite tier remains the conversion result.
func TestGrokOAuthServiceRefreshProviderTokenIgnoresIDTokenTierWhenAccessTokenHasNone(t *testing.T) {
	svc := newGrokAuthorizationForTest(nil, &grokOAuthClientStub{
		refreshResponse: &xai.TokenResponse{
			AccessToken: "opaque-access-token",
			IDToken:     makeGrokOAuthJWT(map[string]any{"tier": 5}),
			TokenType:   "Bearer",
			ExpiresIn:   3600,
		},
	})
	svc.Start()
	defer stopGrokAuthorizationForTest(t, svc)

	provider := &providercore.Record{
		Platform: capability.PlatformGrok,
		Type:     capability.ProviderTypeOAuth,
		Credentials: map[string]any{
			"refresh_token":     "refresh-token",
			"subscription_tier": "supergrok_lite",
		},
	}

	info, err := svc.RefreshProviderToken(context.Background(), provider)
	require.NoError(t, err)
	require.Equal(t, "supergrok_lite", info.SubscriptionTier)
}

// TestGrokOAuthServiceRefreshProviderTokenKeepsStoredTierWhenJWTHasNoClaim
// returns opaque access data with no tier claim. It checks the existing Lite
// credential tier remains available in the provider refresh conversion result.
func TestGrokOAuthServiceRefreshProviderTokenKeepsStoredTierWhenJWTHasNoClaim(t *testing.T) {
	svc := newGrokAuthorizationForTest(nil, &grokOAuthClientStub{
		refreshResponse: &xai.TokenResponse{
			AccessToken: "opaque-access-token",
			TokenType:   "Bearer",
			ExpiresIn:   3600,
		},
	})
	svc.Start()
	defer stopGrokAuthorizationForTest(t, svc)

	provider := &providercore.Record{
		Platform: capability.PlatformGrok,
		Type:     capability.ProviderTypeOAuth,
		Credentials: map[string]any{
			"refresh_token":     "refresh-token",
			"subscription_tier": "supergrok_lite",
		},
	}

	info, err := svc.RefreshProviderToken(context.Background(), provider)
	require.NoError(t, err)
	require.Equal(t, "supergrok_lite", info.SubscriptionTier)
}

// TestGrokOAuthServiceValidateSSOTokenReturnsOAuthTokensWithoutPersistingSSO
// validates temporary SSO input into access and refresh tokens. It checks the
// built credential map excludes SSO and password fields while retaining
// exchanged tokens.
func TestGrokOAuthServiceValidateSSOTokenReturnsOAuthTokensWithoutPersistingSSO(t *testing.T) {
	svc := newGrokAuthorizationForTest(nil, &grokOAuthClientStub{
		ssoResponse: &xai.TokenResponse{
			AccessToken:  "access-from-sso",
			RefreshToken: "refresh-from-sso",
			TokenType:    "Bearer",
			ExpiresIn:    3600,
		},
	})
	svc.Start()
	defer stopGrokAuthorizationForTest(t, svc)

	info, err := svc.ValidateSSOToken(context.Background(), "sso-token", nil)
	require.NoError(t, err)
	require.Equal(t, "access-from-sso", info.AccessToken)
	require.Equal(t, "refresh-from-sso", info.RefreshToken)

	creds := svc.BuildProviderCredentials(info)
	require.NotContains(t, creds, "sso_token")
	require.NotContains(t, creds, "password")
}

// TestGrokOAuthServiceAuthorizePasswordUsesLoginThenSSOAuthorize enables
// password login with configured SSO conversion. It checks trimmed email,
// untrimmed password input and exchanged access data, while temporary inputs
// stay out of credentials.
func TestGrokOAuthServiceAuthorizePasswordUsesLoginThenSSOAuthorize(t *testing.T) {
	client := &grokOAuthClientStub{
		loginResult: &providercore.GrokPasswordLoginResult{
			Email:    "user@example.com",
			SSOToken: "password-derived-sso",
		},
		ssoResponse: &xai.TokenResponse{
			AccessToken:  "access-from-password",
			RefreshToken: "refresh-from-password",
			ExpiresIn:    3600,
		},
	}
	enabled := true
	svc := newGrokAuthorizationForTest(nil, client, enabled)
	svc.Start()
	defer stopGrokAuthorizationForTest(t, svc)

	require.True(t, svc.GetCapabilities().PasswordAuthEnabled)
	info, err := svc.AuthorizePassword(context.Background(), " user@example.com ", "  super-secret  ", nil)
	require.NoError(t, err)
	require.Equal(t, "user@example.com", info.Email)
	require.Equal(t, "access-from-password", info.AccessToken)
	creds := svc.BuildProviderCredentials(info)
	require.NotContains(t, creds, "password")
	require.NotContains(t, creds, "sso_token")
	require.Equal(t, "user@example.com", client.loginEmail)
	require.Equal(t, "  super-secret  ", client.loginPassword)
}

// TestGrokOAuthServiceAuthorizePasswordDisabledByDefault omits the enabling
// option. It checks disabled capability, the password-auth error and an
// untouched login email capture, demonstrating rejection before supplier login
// is reached.
func TestGrokOAuthServiceAuthorizePasswordDisabledByDefault(t *testing.T) {
	client := &grokOAuthClientStub{}
	svc := newGrokAuthorizationForTest(nil, client)
	svc.Start()
	defer stopGrokAuthorizationForTest(t, svc)

	require.False(t, svc.GetCapabilities().PasswordAuthEnabled)
	_, err := svc.AuthorizePassword(context.Background(), "user@example.com", "secret", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "GROK_OAUTH_PASSWORD_AUTH_DISABLED")
	require.Empty(t, client.loginEmail)
}

// TestGrokTokenProviderRefreshesExpiredTokenOnRequestPath obtains request
// credentials from an expired provider. It checks one credential update,
// retained refresh input/endpoint, positive cache lifetime and one release of
// the acquired refresh lock.
func TestGrokTokenProviderRefreshesExpiredTokenOnRequestPath(t *testing.T) {
	xai.ConfigureOverrides(xai.Overrides{BaseURL: xai.DefaultCLIBaseURL})
	t.Cleanup(func() { xai.ConfigureOverrides(xai.Overrides{}) })

	expiredAt := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	provider := &providercore.Record{
		ID:          54,
		Platform:    capability.PlatformGrok,
		Type:        capability.ProviderTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"access_token":  "expired-access-token",
			"refresh_token": "refresh-token",
			"expires_at":    expiredAt,
			"base_url":      xai.DefaultCLIBaseURL,
			"client_id":     "client-id",
		},
	}
	repo := &tokenRefreshProviderRepo{}
	repo.providersByID = map[int64]*providercore.Record{54: provider}
	cache := &grokTokenCacheForProviderTest{lockResult: true}
	oauthSvc := newGrokAuthorizationForTest(nil, &grokOAuthClientStub{
		refreshResponse: &xai.TokenResponse{
			AccessToken: "new-access-token",
			TokenType:   "Bearer",
			ExpiresIn:   3600,
		},
	})
	oauthSvc.Start()
	defer stopGrokAuthorizationForTest(t, oauthSvc)

	tokenSource := newGrokTokenSourceForTest(repo, cache)
	bindGrokRefreshForTest(tokenSource, newRefreshAPI(repo, cache), providercore.NewGrokTokenRefresher(oauthSvc))

	token, err := tokenSource.GetAccessToken(context.Background(), providercore.CloneRecord(provider))
	require.NoError(t, err)
	require.Equal(t, "new-access-token", token)
	require.Equal(t, 1, repo.updateCredentialsCalls)
	require.Equal(t, "new-access-token", repo.providersByID[54].GetGrokAccessToken())
	require.Equal(t, "refresh-token", repo.providersByID[54].GetGrokRefreshToken())
	require.Equal(t, xai.DefaultCLIBaseURL, GrokProviderBaseURL(repo.providersByID[54]))
	require.Equal(t, "grok:provider:54", cache.setKey)
	require.Equal(t, "new-access-token", cache.setToken)
	require.Greater(t, cache.setTTL, time.Duration(0))
	require.Equal(t, 1, cache.releaseCalls)
}

// TestGrokTokenProviderRefreshFailureUnschedulesWithRedactedReason injects a
// refresh failure containing credential-like text. It checks an error and
// empty token with zero generic temporary/error writes; this case does not
// assert reason redaction.
func TestGrokTokenProviderRefreshFailureUnschedulesWithRedactedReason(t *testing.T) {
	expiredAt := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	provider := &providercore.Record{
		ID:          55,
		Platform:    capability.PlatformGrok,
		Type:        capability.ProviderTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"access_token":  "expired-access-token",
			"refresh_token": "refresh-token",
			"expires_at":    expiredAt,
			"base_url":      xai.DefaultCLIBaseURL,
		},
	}
	repo := &tokenRefreshProviderRepo{}
	repo.providersByID = map[int64]*providercore.Record{55: provider}
	cache := &grokTokenCacheForProviderTest{lockResult: true}
	tokenSource := newGrokTokenSourceForTest(repo, cache)
	bindGrokRefreshForTest(tokenSource, newRefreshAPI(repo, cache), &tokenRefresherStub{
		err: errors.New("temporary refresh failure access_token=leaked-access refresh_token=leaked-refresh"),
	})

	token, err := tokenSource.GetAccessToken(context.Background(), providercore.CloneRecord(provider))
	require.Error(t, err)
	require.Empty(t, token)
	require.Equal(t, 0, repo.setTempUnschedCalls)
	require.Equal(t, 0, repo.setErrorCalls)
}

// TestGrokTokenProviderLockHeldWaitsForRefreshedCacheAndNeverUsesExpiredToken
// delays a valid replacement row/cache value while lock acquisition fails. It
// checks the rotated token, rejected stale token and a non-immediate wait
// result.
func TestGrokTokenProviderLockHeldWaitsForRefreshedCacheAndNeverUsesExpiredToken(t *testing.T) {
	provider := expiredGrokOAuthProviderForCredentialTest(56)
	baseRepo := &tokenRefreshProviderRepo{}
	baseRepo.providersByID = map[int64]*providercore.Record{provider.ID: provider}
	repo := &grokCredentialRaceRepo{tokenRefreshProviderRepo: baseRepo}
	cache := &grokTokenCacheForProviderTest{lockResult: false, token: "expired-access-token"}
	tokenSource := newGrokTokenSourceForTest(repo, cache)
	bindGrokRefreshForTest(tokenSource, newRefreshAPI(repo, cache), &tokenRefresherStub{})

	// Publish valid replacement data after token waiting has had an opportunity
	// to observe the old row/cache pair; the delay models a separate refresh owner.
	go func() {
		time.Sleep(40 * time.Millisecond)
		refreshed := *provider
		refreshed.Credentials = querycache.ShallowMap(provider.Credentials)
		refreshed.Credentials["access_token"] = "refreshed-after-lock"
		refreshed.Credentials["expires_at"] = time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
		refreshed.Credentials["_token_version"] = time.Now().UnixMilli()
		repo.setProvider(&refreshed)
		cache.mu.Lock()
		cache.token = "refreshed-after-lock"
		cache.mu.Unlock()
	}()

	startedAt := time.Now()
	token, err := tokenSource.GetAccessToken(context.Background(), providercore.CloneRecord(provider))
	require.NoError(t, err)
	require.Equal(t, "refreshed-after-lock", token)
	require.NotEqual(t, "expired-access-token", token)
	// A 25ms floor excludes an immediate stale-cache result in this
	// delayed-rotation fixture.
	require.GreaterOrEqual(t, time.Since(startedAt), 25*time.Millisecond,
		"expired provider metadata must prevent returning the old cached token")
}

// TestGrokTokenProviderLockHeldTimeoutDoesNotReturnExpiredToken keeps
// unchanged expired credentials behind a held lock. It checks the short caller
// deadline returns an error and no expired token to the request caller.
func TestGrokTokenProviderLockHeldTimeoutDoesNotReturnExpiredToken(t *testing.T) {
	provider := expiredGrokOAuthProviderForCredentialTest(57)
	repo := &tokenRefreshProviderRepo{}
	repo.providersByID = map[int64]*providercore.Record{provider.ID: provider}
	cache := &grokTokenCacheForProviderTest{lockResult: false}
	tokenSource := newGrokTokenSourceForTest(repo, cache)
	bindGrokRefreshForTest(tokenSource, newRefreshAPI(repo, cache), &tokenRefresherStub{})
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	token, err := tokenSource.GetAccessToken(ctx, providercore.CloneRecord(provider))
	require.Error(t, err)
	require.Empty(t, token)
}

// TestGrokTokenProviderLockHeldRejectsChangedTokenWithoutExpiry publishes
// changed token/version data with no expiry while contention continues. It
// checks the request still fails with no token because rotation alone is
// insufficient.
func TestGrokTokenProviderLockHeldRejectsChangedTokenWithoutExpiry(t *testing.T) {
	provider := expiredGrokOAuthProviderForCredentialTest(58)
	baseRepo := &tokenRefreshProviderRepo{}
	baseRepo.providersByID = map[int64]*providercore.Record{provider.ID: provider}
	repo := &grokCredentialRaceRepo{tokenRefreshProviderRepo: baseRepo}
	cache := &grokTokenCacheForProviderTest{lockResult: false, token: "expired-access-token"}
	tokenSource := newGrokTokenSourceForTest(repo, cache)
	bindGrokRefreshForTest(tokenSource, newRefreshAPI(repo, cache), &tokenRefresherStub{})

	// Rotate identity without its lifetime so waiting can observe a changed token
	// that still fails expiry validation; preserve the original selected map
	// for comparison.
	go func() {
		time.Sleep(30 * time.Millisecond)
		refreshed := *provider
		refreshed.Credentials = querycache.ShallowMap(provider.Credentials)
		refreshed.Credentials["access_token"] = "changed-without-expiry"
		delete(refreshed.Credentials, "expires_at")
		refreshed.Credentials["_token_version"] = time.Now().UnixMilli()
		repo.setProvider(&refreshed)
		cache.mu.Lock()
		cache.token = "changed-without-expiry"
		cache.mu.Unlock()
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	token, err := tokenSource.GetAccessToken(ctx, providercore.CloneRecord(provider))

	require.Error(t, err)
	require.Empty(t, token, "an unbounded credential must not win the lock-held race")
}

// TestGrokTokenProviderLockHeldUsesVersionedDBTokenAndRepairsStaleCache
// publishes a valid versioned row while cache reads remain stale. It checks
// the database token wins and a positive-lifetime cache repair is recorded.
func TestGrokTokenProviderLockHeldUsesVersionedDBTokenAndRepairsStaleCache(t *testing.T) {
	provider := expiredGrokOAuthProviderForCredentialTest(60)
	baseRepo := &tokenRefreshProviderRepo{}
	baseRepo.providersByID = map[int64]*providercore.Record{provider.ID: provider}
	repo := &grokCredentialRaceRepo{tokenRefreshProviderRepo: baseRepo}
	cache := &grokTokenCacheForProviderTest{lockResult: false, token: "expired-access-token"}
	tokenSource := newGrokTokenSourceForTest(repo, cache)
	bindGrokRefreshForTest(tokenSource, newRefreshAPI(repo, cache), &tokenRefresherStub{})

	// Replace only the database row while cache reads stay stale, exposing whether
	// the waiter selects valid versioned storage and records a
	// conflicting-cache repair.
	go func() {
		time.Sleep(30 * time.Millisecond)
		refreshed := *provider
		refreshed.Credentials = querycache.ShallowMap(provider.Credentials)
		refreshed.Credentials["access_token"] = "db-authoritative-token"
		refreshed.Credentials["expires_at"] = time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339)
		refreshed.Credentials["_token_version"] = time.Now().UnixMilli()
		repo.setProvider(&refreshed)
	}()

	token, err := tokenSource.GetAccessToken(context.Background(), providercore.CloneRecord(provider))

	require.NoError(t, err)
	require.Equal(t, "db-authoritative-token", token)
	require.Equal(t, "db-authoritative-token", cache.setToken)
	require.Greater(t, cache.setTTL, time.Duration(0))
}

// TestGrokTokenProviderRejectsStaleDBTokenWithoutExpiry rereads a versioned
// token missing expiry despite valid selected metadata. It checks the specific
// expiry error and empty result for that newer but unbounded database
// identity.
func TestGrokTokenProviderRejectsStaleDBTokenWithoutExpiry(t *testing.T) {
	expiresAt := time.Now().Add(2 * providercore.GrokTokenRefreshSkew).UTC().Format(time.RFC3339)
	provider := &providercore.Record{
		ID:          59,
		Platform:    capability.PlatformGrok,
		Type:        capability.ProviderTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"access_token":  "old-access-token",
			"refresh_token": "refresh-token",
			"expires_at":    expiresAt,
		},
	}
	latest := *provider
	latest.Credentials = querycache.ShallowMap(provider.Credentials)
	latest.Credentials["access_token"] = "new-access-token-without-expiry"
	latest.Credentials["_token_version"] = time.Now().UnixMilli()
	delete(latest.Credentials, "expires_at")
	repo := &tokenRefreshProviderRepo{}
	repo.providersByID = map[int64]*providercore.Record{provider.ID: &latest}
	cache := &grokTokenCacheForProviderTest{}
	tokenSource := newGrokTokenSourceForTest(repo, cache)

	token, err := tokenSource.GetAccessToken(context.Background(), providercore.CloneRecord(provider))

	require.ErrorIs(t, err, providercore.ErrGrokOAuthAccessTokenExpired)
	require.Empty(t, token)
}

// TestGrokTokenProviderManualTestBypassesSchedulingGate varies scheduling,
// temporary, quota, overload and error state with valid credentials. It checks
// ordinary request rejection alongside successful manual access to the saved
// token.
func TestGrokTokenProviderManualTestBypassesSchedulingGate(t *testing.T) {
	future := time.Now().Add(time.Hour)
	tests := []struct {
		name   string                     // subtest display label
		mutate func(*providercore.Record) // selected eligibility-state mutation
	}{
		{name: "not schedulable", mutate: func(provider *providercore.Record) { provider.Schedulable = false }},
		{name: "temporarily unschedulable", mutate: func(provider *providercore.Record) { provider.TempUnschedulableUntil = &future }},
		{name: "rate limited", mutate: func(provider *providercore.Record) { provider.RateLimitResetAt = &future }},
		{name: "overloaded", mutate: func(provider *providercore.Record) { provider.OverloadUntil = &future }},
		{name: "disabled by error", mutate: func(provider *providercore.Record) { provider.Status = providercore.StatusError }},
	}

	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := expiredGrokOAuthProviderForCredentialTest(int64(120 + index))
			provider.Credentials["access_token"] = "still-valid-token"
			provider.Credentials["expires_at"] = time.Now().Add(2 * providercore.GrokTokenRefreshSkew).UTC().Format(time.RFC3339)
			tt.mutate(provider)
			tokenSource := newGrokTokenSourceForTest(&tokenRefreshProviderRepo{}, &grokTokenCacheForProviderTest{})

			_, requestErr := tokenSource.GetAccessToken(context.Background(), providercore.CloneRecord(provider))
			require.ErrorIs(t, requestErr, providercore.ErrRefreshProviderStateChanged)

			token, err := tokenSource.GetAccessTokenForManualTest(context.Background(), providercore.CloneRecord(provider))
			require.NoError(t, err)
			require.Equal(t, "still-valid-token", token)
		})
	}
}

// TestGrokTokenProviderManualTestRefreshesExpiredTokenWhileUnschedulable uses
// an active provider with scheduling disabled and expired access data. It
// checks the manual path refreshes once and returns the exchanged token.
func TestGrokTokenProviderManualTestRefreshesExpiredTokenWhileUnschedulable(t *testing.T) {
	xai.ConfigureOverrides(xai.Overrides{BaseURL: xai.DefaultCLIBaseURL})
	t.Cleanup(func() { xai.ConfigureOverrides(xai.Overrides{}) })

	expiredAt := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	provider := &providercore.Record{
		ID:          130,
		Platform:    capability.PlatformGrok,
		Type:        capability.ProviderTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: false,
		Credentials: map[string]any{
			"access_token":  "expired-access-token",
			"refresh_token": "refresh-token",
			"expires_at":    expiredAt,
			"base_url":      xai.DefaultCLIBaseURL,
			"client_id":     "client-id",
		},
	}
	repo := &tokenRefreshProviderRepo{}
	repo.providersByID = map[int64]*providercore.Record{130: provider}
	cache := &grokTokenCacheForProviderTest{lockResult: true}
	oauthSvc := newGrokAuthorizationForTest(nil, &grokOAuthClientStub{
		refreshResponse: &xai.TokenResponse{
			AccessToken: "manual-test-refreshed-token",
			TokenType:   "Bearer",
			ExpiresIn:   3600,
		},
	})
	oauthSvc.Start()
	defer stopGrokAuthorizationForTest(t, oauthSvc)

	tokenSource := newGrokTokenSourceForTest(repo, cache)
	bindGrokRefreshForTest(tokenSource, newRefreshAPI(repo, cache), providercore.NewGrokTokenRefresher(oauthSvc))

	token, err := tokenSource.GetAccessTokenForManualTest(context.Background(), providercore.CloneRecord(provider))
	require.NoError(t, err)
	require.Equal(t, "manual-test-refreshed-token", token)
	require.Equal(t, 1, repo.updateCredentialsCalls)
}

// TestGrokTokenProviderManualTestFallsBackToValidTokenOnRefreshFailure
// supplies valid near-expiry access data and failed refresh. It checks manual
// acquisition succeeds with that still-valid stored token despite the supplier
// exchange failure.
func TestGrokTokenProviderManualTestFallsBackToValidTokenOnRefreshFailure(t *testing.T) {
	provider := &providercore.Record{
		ID:          131,
		Platform:    capability.PlatformGrok,
		Type:        capability.ProviderTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: false,
		Credentials: map[string]any{
			"access_token":  "near-expiry-token",
			"refresh_token": "refresh-token",

			"expires_at": time.Now().Add(10 * time.Minute).UTC().Format(time.RFC3339),
		},
	}
	repo := &tokenRefreshProviderRepo{}
	repo.providersByID = map[int64]*providercore.Record{131: provider}
	cache := &grokTokenCacheForProviderTest{lockResult: true}
	tokenSource := newGrokTokenSourceForTest(repo, cache)
	bindGrokRefreshForTest(tokenSource, newRefreshAPI(repo, cache), &tokenRefresherStub{
		err: errors.New("upstream refresh unavailable"),
	})

	token, err := tokenSource.GetAccessTokenForManualTest(context.Background(), providercore.CloneRecord(provider))
	require.NoError(t, err)
	require.Equal(t, "near-expiry-token", token)
}

// TestGrokTokenProviderManualTestReportsRefreshFailureWhenTokenExpired injects
// an invalid_client failure for expired manual-test credentials. It checks
// that failure text propagates with an empty result rather than returning
// expired access.
func TestGrokTokenProviderManualTestReportsRefreshFailureWhenTokenExpired(t *testing.T) {
	provider := expiredGrokOAuthProviderForCredentialTest(132)
	provider.Schedulable = false
	repo := &tokenRefreshProviderRepo{}
	repo.providersByID = map[int64]*providercore.Record{provider.ID: provider}
	cache := &grokTokenCacheForProviderTest{lockResult: true}
	tokenSource := newGrokTokenSourceForTest(repo, cache)
	bindGrokRefreshForTest(tokenSource, newRefreshAPI(repo, cache), &tokenRefresherStub{
		err: errors.New("invalid_client: client credentials rejected"),
	})

	token, err := tokenSource.GetAccessTokenForManualTest(context.Background(), providercore.CloneRecord(provider))
	require.Error(t, err)
	require.Empty(t, token)
	require.Contains(t, err.Error(), "invalid_client")
}

// TestGrokTokenProviderManualTestLockHeldWithExpiredTokenReturnsSpecificError
// forces contention for expired manual-test credentials. It checks empty
// output and the refresh-in-progress diagnostic for retrying after another
// worker completes.
func TestGrokTokenProviderManualTestLockHeldWithExpiredTokenReturnsSpecificError(t *testing.T) {
	provider := expiredGrokOAuthProviderForCredentialTest(133)
	repo := &tokenRefreshProviderRepo{}
	repo.providersByID = map[int64]*providercore.Record{provider.ID: provider}
	cache := &grokTokenCacheForProviderTest{lockResult: false}
	tokenSource := newGrokTokenSourceForTest(repo, cache)
	bindGrokRefreshForTest(tokenSource, newRefreshAPI(repo, cache), &tokenRefresherStub{})

	token, err := tokenSource.GetAccessTokenForManualTest(context.Background(), providercore.CloneRecord(provider))
	require.Error(t, err)
	require.Empty(t, token)
	require.Contains(t, err.Error(), "refresh is already in progress")
}

// TestGrokTokenProviderManualTestRequiresRefreshToken removes refresh input
// from an expired OAuth provider. It checks the specific missing-refresh-token
// error and an empty result before unconfigured refresh work can begin.
func TestGrokTokenProviderManualTestRequiresRefreshToken(t *testing.T) {
	provider := expiredGrokOAuthProviderForCredentialTest(134)
	delete(provider.Credentials, "refresh_token")
	tokenSource := newGrokTokenSourceForTest(&tokenRefreshProviderRepo{}, &grokTokenCacheForProviderTest{})

	token, err := tokenSource.GetAccessTokenForManualTest(context.Background(), providercore.CloneRecord(provider))
	require.ErrorIs(t, err, providercore.ErrGrokOAuthRefreshTokenMissing)
	require.Empty(t, token)
}

// TestGrokTokenProviderRejectsIneligibleSelectedProviderBeforeWarmCache varies
// disabled, scheduling, quota and overload state beside a warm token. It
// checks the eligibility error, empty output and zero cache reads for each
// selected record.
func TestGrokTokenProviderRejectsIneligibleSelectedProviderBeforeWarmCache(t *testing.T) {
	future := time.Now().Add(time.Hour)
	tests := []struct {
		name   string                     // subtest display label
		mutate func(*providercore.Record) // selected eligibility-state mutation
	}{
		{name: "disabled", mutate: func(provider *providercore.Record) { provider.Status = billing.StatusDisabled }},
		{name: "not schedulable", mutate: func(provider *providercore.Record) { provider.Schedulable = false }},
		{name: "temporarily unschedulable", mutate: func(provider *providercore.Record) { provider.TempUnschedulableUntil = &future }},
		{name: "rate limited", mutate: func(provider *providercore.Record) { provider.RateLimitResetAt = &future }},
		{name: "overloaded", mutate: func(provider *providercore.Record) { provider.OverloadUntil = &future }},
	}

	for index, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := expiredGrokOAuthProviderForCredentialTest(int64(90 + index))
			provider.Credentials["access_token"] = "warm-cache-token"
			provider.Credentials["expires_at"] = time.Now().Add(2 * providercore.GrokTokenRefreshSkew).UTC().Format(time.RFC3339)
			tt.mutate(provider)
			cache := &grokTokenCacheForProviderTest{token: "warm-cache-token"}
			tokenSource := newGrokTokenSourceForTest(&tokenRefreshProviderRepo{}, cache)

			token, err := tokenSource.GetAccessToken(context.Background(), providercore.CloneRecord(provider))

			require.ErrorIs(t, err, providercore.ErrRefreshProviderStateChanged)
			require.Empty(t, token)
			require.Zero(t, cache.getCalls, "an ineligible selected provider must be rejected before cache lookup")
		})
	}
}

// ExchangeCode records redirect selection and returns a fresh fixed access
// response. It counts serial invocations and ignores the other supplied
// inputs. This fixture lets session-validation cases detect whether supplier
// exchange was reached.
func (s *grokOAuthClientStub) ExchangeCode(_ context.Context, _, _, redirectURI, _, _ string) (*xai.TokenResponse, error) {
	s.exchangeCalls++
	s.exchangeRedirectURI = redirectURI
	return &xai.TokenResponse{AccessToken: "access-token"}, nil
}

// RefreshToken returns the configured response pointer with no fixture
// exchange error. All call arguments are unused. A nil configured response
// deliberately exercises the core's invalid-response conversion instead of
// inventing default credentials.
func (s *grokOAuthClientStub) RefreshToken(context.Context, string, string, string) (*xai.TokenResponse, error) {
	return s.refreshResponse, nil
}

// LoginWithPassword records email and password exactly as passed and returns
// loginResult. It supplies no error or additional normalization. The borrowed
// result lets the authorization test distinguish caller email trimming from
// password preservation.
func (s *grokOAuthClientStub) LoginWithPassword(_ context.Context, email, password, _ string) (*providercore.GrokPasswordLoginResult, error) {
	s.loginEmail = email
	s.loginPassword = password
	return s.loginResult, nil
}

// ConvertSSOToBuild returns the configured SSO exchange response with no
// fixture error. Context, SSO input and proxy are unused. The returned pointer
// remains owned by the fixture and is interpreted by core credential
// conversion.
func (s *grokOAuthClientStub) ConvertSSOToBuild(context.Context, string, string) (*xai.TokenResponse, error) {
	return s.ssoResponse, nil
}

// newGrokAuthorizationForTest binds platform options and the selected fixture
// client. The first optional boolean enables password login; omitted input
// keeps it disabled through the options callback. The caller owns core startup
// and stopping.
func newGrokAuthorizationForTest(proxies egress.ProxyRepository, client providercore.GrokAuthorizationClient, enabled ...bool) *providercore.GrokAuthorization {
	options := GrokAuthorizationOptions(proxies, func() bool { return len(enabled) > 0 && enabled[0] })
	return providercore.NewGrokAuthorization(client, options)
}

// stopGrokAuthorizationForTest stops the supplied core and asserts successful
// shutdown. It uses Background with no deadline, so admitted work is awaited
// until its actual completion. The helper marks failures at the calling test
// through require.
func stopGrokAuthorizationForTest(t *testing.T, authorization *providercore.GrokAuthorization) {
	t.Helper()
	require.NoError(t, authorization.StopContext(context.Background()))
}

// GetByID holds the fixture read lock around the base repository lookup. It
// coordinates with setProvider replacement and returns the base result/copy
// behavior unchanged. Concurrent readers can still share that lock, including
// the base fixture's separately incremented read counter.
func (r *grokCredentialRaceRepo) GetByID(ctx context.Context, id int64) (*providercore.Record, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.tokenRefreshProviderRepo.GetByID(ctx, id)
}

// setProvider replaces the selected map entry under the fixture's write lock.
// The supplied provider pointer is retained rather than cloned. Callers
// prepare its credential map before publishing it so this method only replaces
// row ownership.
func (r *grokCredentialRaceRepo) setProvider(provider *providercore.Record) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Publish the completed row pointer while excluding delegated lookups; later
	// readers see either the prior entry or this replacement rather than a
	// map-write interleaving.
	r.providersByID[provider.ID] = provider
}

// GetAccessToken counts a cache read under mu and returns the simulated token.
// An empty token returns the fixture cache-miss error. The key and context are
// unused, so this substitute exercises availability rather than per-key or
// deadline behavior.
func (c *grokTokenCacheForProviderTest) GetAccessToken(context.Context, string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.getCalls++
	if c.token == "" {
		return "", errors.New("not cached")
	}
	return c.token, nil
}

// SetAccessToken records key, token and lifetime under the cache observation
// mutex. It returns nil and leaves the simulated read token unchanged.
// Assertions use the recorded write independently of any test-owned concurrent
// token replacement.
func (c *grokTokenCacheForProviderTest) SetAccessToken(_ context.Context, key string, token string, ttl time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.setKey = key
	c.setToken = token
	c.setTTL = ttl
	return nil
}

// DeleteAccessToken records the attempted key under mu and returns deleteErr.
// Recording occurs even on configured failure. The simulated token is
// retained, so this fixture reports deletion attempts rather than implementing
// token eviction.
func (c *grokTokenCacheForProviderTest) DeleteAccessToken(_ context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.deletedKeys = append(c.deletedKeys, key)
	return c.deleteErr
}

// AcquireRefreshLock returns the configured ownership result for every key and
// lifetime. It performs no mutex or lease operation and supplies no error.
// Tests configure lockResult before use to select coordinated exchange or
// contention handling.
func (c *grokTokenCacheForProviderTest) AcquireRefreshLock(context.Context, string, time.Duration) (bool, error) {
	return c.lockResult, nil
}

// ReleaseRefreshLock counts a serial release notification with no key
// validation. It returns nil and changes no ownership state. Callers keep
// these release calls serial because releaseCalls is updated outside the cache
// observation mutex.
func (c *grokTokenCacheForProviderTest) ReleaseRefreshLock(context.Context, string) error {
	c.releaseCalls++
	return nil
}

// newGrokTokenSourceForTest binds repository, cache and production Grok
// refresh policy. It retains those dependencies and leaves Refresh unset.
// Cases install coordinated exchange separately, allowing eligibility-only
// cases to observe rejection before refresh work.
func newGrokTokenSourceForTest(repo providercore.RefreshRepository, cache providercore.AccessTokenCache) *providercore.GrokTokenSource {
	return &providercore.GrokTokenSource{Repository: repo, Cache: cache, Policy: providercore.GrokProviderRefreshPolicy()}
}

// bindGrokRefreshForTest installs coordinated refresh on the supplied token
// source. The closure delegates lock, reread and persistence handling to
// RefreshIfNeeded with the selected executor and window. Configure this
// binding before concurrent token-source use.
func bindGrokRefreshForTest(source *providercore.GrokTokenSource, refresh *providercore.OAuthRefreshAPI, executor providercore.OAuthRefreshExecutor) {
	source.Refresh = func(ctx context.Context, value *providercore.Record, window time.Duration) (*providercore.OAuthRefreshResult, error) {
		return refresh.RefreshIfNeeded(ctx, value, executor, window)
	}
}

// expiredGrokOAuthProviderForCredentialTest creates an active schedulable
// OAuth fixture. It includes expired access data, refresh token and the CLI
// endpoint for the supplied ID. Each call owns a newly constructed outer
// credential map.
func expiredGrokOAuthProviderForCredentialTest(id int64) *providercore.Record {
	return &providercore.Record{
		ID:          id,
		Platform:    capability.PlatformGrok,
		Type:        capability.ProviderTypeOAuth,
		Status:      billing.StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"access_token":  "expired-access-token",
			"refresh_token": "refresh-token",
			"expires_at":    time.Now().Add(-time.Minute).UTC().Format(time.RFC3339),
			"base_url":      xai.DefaultCLIBaseURL,
		},
	}
}
