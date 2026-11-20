package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/protocol/gemini"
	"github.com/TokenFlux/TokenRouter/internal/protocol/google"
)

// gemini_authorization_test.go exercises Gemini OAuth lifecycle bounds, tier
// and project validation, GetOperation onboarding polling, ineligible-tier
// diagnostics, and session-cached idempotent Project ID completion.

// stoppingGeminiClient counts refresh attempts and blocks each until context
// cancellation.
type stoppingGeminiClient struct {
	// entered is initialized before refresh and closes on its first attempt.
	// Later retries share that signal while calls atomically counts each
	// invocation.
	entered chan struct{}
	calls   atomic.Int32 // atomic refresh attempt count
}

// scriptedGeminiOAuthClient counts ExchangeCode and RefreshToken calls and
// returns configured token responses.
type scriptedGeminiOAuthClient struct {
	exchangeCalls int                   // number of ExchangeCode calls observed
	refreshCalls  int                   // number of RefreshToken calls observed
	tokenResp     *google.TokenResponse // returned OAuth token payload
}

// scriptedGeminiCodeAssistClient records Code Assist discovery, onboarding,
// and GetOperation polling calls with configurable responses.
type scriptedGeminiCodeAssistClient struct {
	loadCalls    []*gemini.LoadCodeAssistRequest // captured LoadCodeAssist requests
	onboardCalls []*gemini.OnboardUserRequest    // captured OnboardUser requests
	pollCalls    []string                        // captured GetOperation names
	loadFn       func(*gemini.LoadCodeAssistRequest) (*gemini.LoadCodeAssistResponse, error)
	onboardFn    func(*gemini.OnboardUserRequest) (*gemini.OnboardUserResponse, error)
	pollFn       func(string) (*gemini.OnboardUserResponse, error)
}

// ExchangeCode returns the unused-operation error for this refresh-only
// protocol fixture.
func (c *stoppingGeminiClient) ExchangeCode(context.Context, string, string, string, string, string) (*google.TokenResponse, error) {
	return nil, errors.New("not used")
}

// RefreshToken counts attempts, signals first entry, and waits for
// cancellation. Initialize entered before use; canceled calls return
// immediately but still count each retry invocation made by the authorization
// workflow.
func (c *stoppingGeminiClient) RefreshToken(ctx context.Context, _ string, _ string, _ string) (*google.TokenResponse, error) {
	if c.calls.Add(1) == 1 {
		// Publish first entry once through the atomic count so the test can
		// request shutdown after registered cooperative refresh work has actually
		// begun.
		close(c.entered)
	}
	// Stay active until cancellation so shutdown accounts for this request.
	// Later retries can immediately observe that same canceled context and count
	// another invocation.
	<-ctx.Done()
	return nil, ctx.Err()
}

// ExchangeCode increments exchangeCalls and returns the configured Google
// token payload.
func (c *scriptedGeminiOAuthClient) ExchangeCode(context.Context, string, string, string, string, string) (*google.TokenResponse, error) {
	c.exchangeCalls++
	return c.tokenResp, nil
}

// RefreshToken increments refreshCalls and returns the configured Google token
// payload.
func (c *scriptedGeminiOAuthClient) RefreshToken(context.Context, string, string, string) (*google.TokenResponse, error) {
	c.refreshCalls++
	return c.tokenResp, nil
}

// LoadCodeAssist records req and delegates to loadFn when configured.
func (c *scriptedGeminiCodeAssistClient) LoadCodeAssist(_ context.Context, _, _ string, req *gemini.LoadCodeAssistRequest) (*gemini.LoadCodeAssistResponse, error) {
	c.loadCalls = append(c.loadCalls, req)
	if c.loadFn != nil {
		return c.loadFn(req)
	}
	return &gemini.LoadCodeAssistResponse{}, nil
}

// OnboardUser records req and delegates to onboardFn when configured.
func (c *scriptedGeminiCodeAssistClient) OnboardUser(_ context.Context, _, _ string, req *gemini.OnboardUserRequest) (*gemini.OnboardUserResponse, error) {
	c.onboardCalls = append(c.onboardCalls, req)
	if c.onboardFn != nil {
		return c.onboardFn(req)
	}
	return &gemini.OnboardUserResponse{Done: true}, nil
}

// GetOperation records operationName and delegates to pollFn when configured.
func (c *scriptedGeminiCodeAssistClient) GetOperation(_ context.Context, _, _, operationName string) (*gemini.OnboardUserResponse, error) {
	c.pollCalls = append(c.pollCalls, operationName)
	if c.pollFn != nil {
		return c.pollFn(operationName)
	}
	return &gemini.OnboardUserResponse{Done: true}, nil
}

// newScriptedGeminiAuthOptions builds deterministic PKCE and URL callbacks for
// Gemini ExchangeCode unit tests.
func newScriptedGeminiAuthOptions(fetchProject func(context.Context, string, string) (string, error)) GeminiAuthorizationOptions {
	var sessionSeq int64
	return GeminiAuthorizationOptions{
		Config:                func() google.OAuthConfig { return google.OAuthConfig{} },
		CLIRedirectURI:        "https://codeassist.google.com/authcode",
		AIStudioRedirectURI:   "http://localhost:1455/auth/callback",
		BuiltinClientID:       "builtin-cli-client",
		EffectiveOAuthConfig:  func(cfg google.OAuthConfig, _ string) (google.OAuthConfig, error) { return cfg, nil },
		GenerateState:         func() (string, error) { return "state-1", nil },
		GenerateCodeVerifier:  func() (string, error) { return "verifier-1", nil },
		GenerateCodeChallenge: func(v string) string { return v },
		GenerateSessionID: func() (string, error) {
			sessionSeq++
			return fmt.Sprintf("session-%d", sessionSeq), nil
		},
		BuildAuthorizationURL: func(_ google.OAuthConfig, state, _, redirectURI, projectID, oauthType string) (string, error) {
			return fmt.Sprintf("https://accounts.google.com/o/oauth2/v2/auth?state=%s&redirect_uri=%s&project_id=%s&type=%s", state, redirectURI, projectID, oauthType), nil
		},
		FetchProject: fetchProject,
		Logf:         func(string, ...any) {},
		Printf:       func(string, ...any) {},
	}
}

// TestGeminiAuthorizationStopBoundsInFlightRefresh cancels a registered
// refresh while unconditional retries remain. It checks bounded stop
// reporting, stopped admission, eventual return, four calls, and retention of
// the initial shutdown timeout.
func TestGeminiAuthorizationStopBoundsInFlightRefresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		client := &stoppingGeminiClient{entered: make(chan struct{})}
		authorization := NewGeminiAuthorization(client, nil, GeminiAuthorizationOptions{})
		require.False(t, authorization.Store.runtimeStarted, "constructor must not start background cleanup")
		authorization.Start()
		authorization.Start()
		done := make(chan error, 1)
		go func() {
			_, err := authorization.RefreshToken(context.Background(), "code_assist", "fixture-token", "")
			done <- err
		}()
		select {
		case <-client.entered:
		case <-time.After(time.Second):
			t.Fatal("refresh did not start")
		}
		budget, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		start := time.Now()
		err := authorization.StopContext(budget)
		require.ErrorIs(t, err, context.DeadlineExceeded)
		require.Less(t, time.Since(start), time.Second)
		_, err = authorization.RefreshToken(context.Background(), "code_assist", "fixture-token", "")
		require.ErrorContains(t, err, "stopped")
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Fatal("bounded refresh flow did not finish")
		}
		// Fixed retry sleeps outlast the short stop budget. Later completion leaves
		// the retained first timeout visible to repeated shutdown callers.
		require.ErrorIs(t, authorization.StopContext(context.Background()), context.DeadlineExceeded)
		require.EqualValues(t, 4, client.calls.Load(), "in-flight retry count must remain unchanged")
	})
}

// TestValidateTierID covers empty, permitted ASCII, exact-length, overlong,
// and forbidden character input. Each case checks whether structural
// validation returns an error for the supplied tier text.
func TestValidateTierID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string // authorization rule case name
		tierID  string // input tier identifier text
		wantErr bool   // expected structural validation failure
	}{
		{name: "empty_valid", tierID: "", wantErr: false},
		{name: "canonical_tier", tierID: "gcp_standard", wantErr: false},
		{name: "with_slash", tierID: "tier/sub", wantErr: false},
		{name: "with_hyphen", tierID: "legacy-tier", wantErr: false},
		{name: "digits_only", tierID: "12345", wantErr: false},
		{name: "overlong_65_chars", tierID: strings.Repeat("a", 65), wantErr: true},
		{name: "exact_64_chars", tierID: strings.Repeat("b", 64), wantErr: false},
		{name: "invalid_space", tierID: "tier id", wantErr: true},
		{name: "invalid_non_ascii", tierID: "tier_中文", wantErr: true},
		{name: "invalid_at_sign", tierID: "tier@id", wantErr: true},
		{name: "invalid_bang", tierID: "tier!id", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateTierID(tt.tierID)
			if tt.wantErr && err == nil {
				t.Fatalf("expected error for tier %q, got nil", tt.tierID)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error for tier %q: %v", tt.tierID, err)
			}
		})
	}
}

// TestValidateGoogleProjectID verifies blank acceptance, valid GCP Project ID
// shapes, numeric Project Number rejection, and malformed character rejection.
func TestValidateGoogleProjectID(t *testing.T) {
	t.Parallel()

	require.NoError(t, ValidateGoogleProjectID(""))
	require.NoError(t, ValidateGoogleProjectID("  "))
	require.NoError(t, ValidateGoogleProjectID("my-gcp-project-123"))
	require.NoError(t, ValidateGoogleProjectID("google.com:workspace-proj_1"))

	err := ValidateGoogleProjectID("412345678901")
	require.Error(t, err)
	require.ErrorContains(t, err, "numeric GCP Project Number")

	require.Error(t, ValidateGoogleProjectID("bad project!"))
	require.Error(t, ValidateGoogleProjectID(strings.Repeat("a", 129)))
}

// TestExtractTierIDFromAllowedTiers varies default flags, missing IDs, empty
// arrays, and padded text. It checks the configured preferred ID and
// legacy-tier fallback from these allowed-tier examples.
func TestExtractTierIDFromAllowedTiers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string               // authorization rule case name
		allowedTiers []gemini.AllowedTier // input upstream allowed tiers
		want         string               // expected configured rule result
	}{
		{
			name:         "nil_returns_legacy_tier",
			allowedTiers: nil,
			want:         "legacy-tier",
		},
		{
			name:         "empty_returns_legacy_tier",
			allowedTiers: []gemini.AllowedTier{},
			want:         "legacy-tier",
		},
		{
			name: "default_tier_wins",
			allowedTiers: []gemini.AllowedTier{
				{ID: "standard-tier", IsDefault: false},
				{ID: "pro-tier", IsDefault: true},
				{ID: "enterprise-tier", IsDefault: false},
			},
			want: "pro-tier",
		},
		{
			name: "first_nonempty_when_no_default",
			allowedTiers: []gemini.AllowedTier{
				{ID: "standard-tier", IsDefault: false},
				{ID: "enterprise-tier", IsDefault: false},
			},
			want: "standard-tier",
		},
		{
			name: "blank_default_falls_back_to_first_nonempty",
			allowedTiers: []gemini.AllowedTier{
				{ID: "", IsDefault: true},
				{ID: "pro-tier", IsDefault: false},
			},
			want: "pro-tier",
		},
		{
			name: "all_blank_returns_legacy_tier",
			allowedTiers: []gemini.AllowedTier{
				{ID: "", IsDefault: false},
				{ID: "   ", IsDefault: false},
			},
			want: "legacy-tier",
		},
		{
			name: "trims_whitespace",
			allowedTiers: []gemini.AllowedTier{
				{ID: "  standard-tier  ", IsDefault: true},
			},
			want: "standard-tier",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ExtractTierIDFromAllowedTiers(tt.allowedTiers)
			if got != tt.want {
				t.Fatalf("ExtractTierIDFromAllowedTiers() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestGeminiAuthorizationExchangeCodeIdempotentProjectCompletion exercises a
// registered standard-tier account without an auto-provisioned companion
// project. The first ExchangeCode caches the exchanged token and prompts for a
// GCP Project ID; the second ExchangeCode reuses that token without calling
// Google token exchange again, passes project hints, and consumes the session.
func TestGeminiAuthorizationExchangeCodeIdempotentProjectCompletion(t *testing.T) {
	oauthClient := &scriptedGeminiOAuthClient{
		tokenResp: &google.TokenResponse{
			AccessToken:  "access-1",
			RefreshToken: "refresh-1",
			ExpiresIn:    3600,
			TokenType:    "Bearer",
		},
	}
	codeAssist := &scriptedGeminiCodeAssistClient{
		loadFn: func(req *gemini.LoadCodeAssistRequest) (*gemini.LoadCodeAssistResponse, error) {
			require.Equal(t, "IDE_UNSPECIFIED", req.Metadata.IDEType)
			return &gemini.LoadCodeAssistResponse{
				CurrentTier: &gemini.TierInfo{ID: "standard-tier"},
			}, nil
		},
	}

	svc := NewGeminiAuthorization(oauthClient, codeAssist, newScriptedGeminiAuthOptions(nil))
	defer func() { require.NoError(t, svc.StopContext(context.Background())) }()

	_, err := svc.GenerateAuthURL(context.Background(), nil, "", "", "google_one", "")
	require.ErrorContains(t, err, "must be 'code_assist' or 'ai_studio'")

	authRes, err := svc.GenerateAuthURL(context.Background(), nil, "", "", "code_assist", "")
	require.NoError(t, err)

	_, err = svc.ExchangeCode(context.Background(), &GeminiExchangeCodeInput{
		SessionID: authRes.SessionID,
		State:     authRes.State,
		Code:      "auth-code-xyz",
	})
	require.Error(t, err)
	require.ErrorContains(t, err, "no re-login needed")
	require.Equal(t, 1, oauthClient.exchangeCalls)

	_, stillStored := svc.Store.Get(authRes.SessionID)
	require.True(t, stillStored, "recoverable missing-project error must retain session token cache")

	// Numeric Project Number must fail validation without consuming the session.
	_, err = svc.ExchangeCode(context.Background(), &GeminiExchangeCodeInput{
		SessionID: authRes.SessionID,
		State:     authRes.State,
		Code:      "auth-code-xyz",
		ProjectID: "987654321012",
	})
	require.ErrorContains(t, err, "numeric GCP Project Number")
	require.Equal(t, 1, oauthClient.exchangeCalls)

	// Supplying a valid GCP Project ID reuses the cached token and completes setup.
	tokenInfo, err := svc.ExchangeCode(context.Background(), &GeminiExchangeCodeInput{
		SessionID: authRes.SessionID,
		State:     authRes.State,
		Code:      "auth-code-xyz",
		ProjectID: "my-gcp-project-42",
	})
	require.NoError(t, err)
	require.Equal(t, 1, oauthClient.exchangeCalls, "second ExchangeCode must reuse cached Google token")
	require.Equal(t, "my-gcp-project-42", tokenInfo.ProjectID)
	require.Equal(t, GeminiTierGCPStandard, tokenInfo.TierID)
	require.Len(t, codeAssist.loadCalls, 2)
	require.Equal(t, "my-gcp-project-42", codeAssist.loadCalls[1].CloudAICompanionProject)
	require.Equal(t, "my-gcp-project-42", codeAssist.loadCalls[1].Metadata.DuetProject)

	_, stillStored = svc.Store.Get(authRes.SessionID)
	require.False(t, stillStored, "completed ExchangeCode must delete the session")
}

// TestGeminiAuthorizationExchangeCodePollsGetOperationAndHandlesIneligibleTiers
// verifies pending OnboardUser operation polling via GetOperation and checks
// fatal UNSUPPORTED_CLIENT deletion versus recoverable VALIDATION_REQUIRED
// retention.
func TestGeminiAuthorizationExchangeCodePollsGetOperationAndHandlesIneligibleTiers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		oauthClient := &scriptedGeminiOAuthClient{
			tokenResp: &google.TokenResponse{
				AccessToken:  "access-op",
				RefreshToken: "refresh-op",
				ExpiresIn:    3600,
				TokenType:    "Bearer",
			},
		}
		pollAttempt := 0
		codeAssist := &scriptedGeminiCodeAssistClient{
			loadFn: func(*gemini.LoadCodeAssistRequest) (*gemini.LoadCodeAssistResponse, error) {
				return &gemini.LoadCodeAssistResponse{
					AllowedTiers: []gemini.AllowedTier{{ID: "free-tier", IsDefault: true}},
				}, nil
			},
			onboardFn: func(req *gemini.OnboardUserRequest) (*gemini.OnboardUserResponse, error) {
				require.Equal(t, "free-tier", req.TierID)
				require.Empty(t, req.CloudAICompanionProject, "free-tier onboarding must omit cloudaicompanionProject")
				return &gemini.OnboardUserResponse{Name: "operations/op-99", Done: false}, nil
			},
			pollFn: func(name string) (*gemini.OnboardUserResponse, error) {
				require.Equal(t, "operations/op-99", name)
				pollAttempt++
				if pollAttempt == 1 {
					return &gemini.OnboardUserResponse{Name: name, Done: false}, nil
				}
				return &gemini.OnboardUserResponse{
					Name: name,
					Done: true,
					Response: &gemini.OnboardUserResultData{
						CloudAICompanionProject: map[string]any{"id": "gen-lang-client-99"},
					},
				}, nil
			},
		}

		svc := NewGeminiAuthorization(oauthClient, codeAssist, newScriptedGeminiAuthOptions(nil))
		defer func() { require.NoError(t, svc.StopContext(context.Background())) }()

		authRes, err := svc.GenerateAuthURL(context.Background(), nil, "", "ignored-free-hint", "code_assist", "")
		require.NoError(t, err)
		info, err := svc.ExchangeCode(context.Background(), &GeminiExchangeCodeInput{
			SessionID: authRes.SessionID,
			State:     authRes.State,
			Code:      "code-op",
		})
		require.NoError(t, err)
		require.Equal(t, "gen-lang-client-99", info.ProjectID)
		require.Equal(t, []string{"operations/op-99", "operations/op-99"}, codeAssist.pollCalls)

		// VALIDATION_REQUIRED retains the session and surfaces validationUrl.
		codeAssist.loadFn = func(*gemini.LoadCodeAssistRequest) (*gemini.LoadCodeAssistResponse, error) {
			return &gemini.LoadCodeAssistResponse{
				IneligibleTiers: []gemini.IneligibleTier{{
					ReasonCode:    "VALIDATION_REQUIRED",
					ReasonMessage: "Verify your account",
					ValidationURL: "https://accounts.google.com/verify",
				}},
			}, nil
		}
		valRes, err := svc.GenerateAuthURL(context.Background(), nil, "", "", "code_assist", "")
		require.NoError(t, err)
		_, err = svc.ExchangeCode(context.Background(), &GeminiExchangeCodeInput{
			SessionID: valRes.SessionID,
			State:     valRes.State,
			Code:      "code-val",
		})
		require.ErrorContains(t, err, "https://accounts.google.com/verify")
		_, exists := svc.Store.Get(valRes.SessionID)
		require.True(t, exists)

		// UNSUPPORTED_CLIENT deletes the session immediately and directs to Antigravity.
		codeAssist.loadFn = func(*gemini.LoadCodeAssistRequest) (*gemini.LoadCodeAssistResponse, error) {
			return &gemini.LoadCodeAssistResponse{
				IneligibleTiers: []gemini.IneligibleTier{{
					ReasonCode:    "UNSUPPORTED_CLIENT",
					ReasonMessage: "Client disabled for personal accounts",
				}},
			}, nil
		}
		unsupRes, err := svc.GenerateAuthURL(context.Background(), nil, "", "", "code_assist", "")
		require.NoError(t, err)
		_, err = svc.ExchangeCode(context.Background(), &GeminiExchangeCodeInput{
			SessionID: unsupRes.SessionID,
			State:     unsupRes.State,
			Code:      "code-unsup",
		})
		require.ErrorContains(t, err, "Antigravity")
		_, exists = svc.Store.Get(unsupRes.SessionID)
		require.False(t, exists)
	})
}

// TestIsNonRetryableGeminiOAuthError supplies four permanent OAuth markers and
// other diagnostic text. It checks permanent classification and retryable
// outcomes for network, server, empty, and incomplete marker examples.
func TestIsNonRetryableGeminiOAuthError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string // authorization rule case name
		err  error  // input OAuth failure diagnostic
		want bool   // expected configured rule result
	}{
		{name: "invalid_grant", err: fmt.Errorf("error: invalid_grant"), want: true},
		{name: "invalid_client", err: fmt.Errorf("oauth error: invalid_client"), want: true},
		{name: "unauthorized_client", err: fmt.Errorf("unauthorized_client: mismatch"), want: true},
		{name: "access_denied", err: fmt.Errorf("access_denied by user"), want: true},
		{name: "network_timeout", err: fmt.Errorf("connection timeout"), want: false},
		{name: "http_500", err: fmt.Errorf("server error 500"), want: false},
		{name: "empty_error", err: fmt.Errorf(""), want: false},
		{name: "partial_invalid_word", err: fmt.Errorf("invalid request"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := IsNonRetryableGeminiOAuthError(tt.err)
			if got != tt.want {
				t.Fatalf("IsNonRetryableGeminiOAuthError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
