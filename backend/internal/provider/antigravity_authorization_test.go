package provider

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/protocol/google"
)

// antigravity_authorization_test.go exercises Antigravity OAuth session
// consumption, idempotent Project ID completion, ineligible-tier diagnostics,
// default tier extraction, and shutdown budget reporting.

// antigravityAuthFixture records serial protocol events and supplies
// configured refresh behavior.
type antigravityAuthFixture struct {
	AntigravityAuthorizationClient // unused embedded protocol operations
	// events is borrowed and appended without locking by serial protocol calls.
	// Tests initialize the slice before invoking the authorization workflow.
	events      *[]string
	exchangeErr error // configured code exchange failure
	// loadFn overrides LoadCodeAssist and LoadCodeAssistWithProject when nonnil.
	loadFn func(string) (*google.AntigravityLoadCodeAssistResponse, map[string]any, error)
	// refresh is required by the shutdown scenario and controls completion.
	// The test retains its channels until the callback returns after explicit
	// release.
	refresh func(context.Context) (*google.AntigravityTokenResponse, error)
}

// ExchangeCode appends its serial event and returns fixture tokens or
// exchangeErr. The event slice is borrowed and needs initialization; input
// proof is ignored by this protocol fixture.
func (f *antigravityAuthFixture) ExchangeCode(context.Context, string, string) (*google.AntigravityTokenResponse, error) {
	*f.events = append(*f.events, "exchange")
	if f.exchangeErr != nil {
		return nil, f.exchangeErr
	}
	return &google.AntigravityTokenResponse{AccessToken: "access", RefreshToken: "refresh", ExpiresIn: 3600, TokenType: "Bearer"}, nil
}

// RefreshToken delegates to the configured nonnil refresh callback using ctx.
// Tests choose cancellation-sensitive or deliberately blocked behavior through
// that callback, while this fixture imposes no additional retry policy.
func (f *antigravityAuthFixture) RefreshToken(ctx context.Context, _ string) (*google.AntigravityTokenResponse, error) {
	return f.refresh(ctx)
}

// GetUserInfo appends the user event and returns the fixed fixture email. The
// borrowed events pointer must be initialized, and callers serialize protocol
// operations while inspecting this slice.
func (f *antigravityAuthFixture) GetUserInfo(context.Context, string) (*google.AntigravityUserInfo, error) {
	*f.events = append(*f.events, "user")
	return &google.AntigravityUserInfo{Email: "fixture@example.invalid"}, nil
}

// LoadCodeAssist appends its event and returns the fixed project and paid
// tier unless loadFn overrides discovery for the test case.
func (f *antigravityAuthFixture) LoadCodeAssist(context.Context, string) (*google.AntigravityLoadCodeAssistResponse, map[string]any, error) {
	*f.events = append(*f.events, "load")
	if f.loadFn != nil {
		return f.loadFn("")
	}
	return &google.AntigravityLoadCodeAssistResponse{CloudAICompanionProject: "project", PaidTier: &google.AntigravityPaidTierInfo{ID: "g1-pro-tier"}}, nil, nil
}

// LoadCodeAssistWithProject appends the hinted load event and passes
// projectID into loadFn when configured.
func (f *antigravityAuthFixture) LoadCodeAssistWithProject(_ context.Context, _, projectID string) (*google.AntigravityLoadCodeAssistResponse, map[string]any, error) {
	*f.events = append(*f.events, "load:"+projectID)
	if f.loadFn != nil {
		return f.loadFn(projectID)
	}
	return &google.AntigravityLoadCodeAssistResponse{CloudAICompanionProject: projectID, PaidTier: &google.AntigravityPaidTierInfo{ID: "g1-pro-tier"}}, nil, nil
}

// OnboardUserWithProject appends the hinted onboard event and returns
// projectID so antigravityAuthFixture satisfies antigravityProjectHintClient.
func (f *antigravityAuthFixture) OnboardUserWithProject(_ context.Context, _, _, projectID string) (string, error) {
	*f.events = append(*f.events, "onboard:"+projectID)
	return projectID, nil
}

// SetUserSettings records its event and returns settings without
// telemetryEnabled. Initialize the borrowed event slice before this serial
// call, whose successful response lets the real privacy predicate proceed.
func (f *antigravityAuthFixture) SetUserSettings(context.Context, string) (*google.AntigravitySetUserSettingsResponse, error) {
	*f.events = append(*f.events, "set-privacy")
	return &google.AntigravitySetUserSettingsResponse{UserSettings: map[string]any{}}, nil
}

// FetchUserInfo records verification and returns settings without
// telemetryEnabled. Its initialized event pointer is shared with earlier
// protocol calls, and the response exercises accepted privacy verification for
// the supplied project.
func (f *antigravityAuthFixture) FetchUserInfo(context.Context, string, string) (*google.AntigravityFetchUserInfoResponse, error) {
	*f.events = append(*f.events, "verify-privacy")
	return &google.AntigravityFetchUserInfoResponse{UserSettings: map[string]any{}}, nil
}

// authFixtureOptions binds fixed PKCE proof, URL, and one borrowed protocol
// client. Proxy lookup is unavailable and diagnostics are no-ops; tests
// configure the client before serial use.
func authFixtureOptions(client AntigravityAuthorizationClient) AntigravityAuthorizationOptions {
	return AntigravityAuthorizationOptions{NewClient: func(string) (AntigravityAuthorizationClient, error) { return client, nil }, ResolveProxy: func(context.Context, int64) (string, bool) { return "", false }, GenerateState: func() (string, error) { return "state", nil }, GenerateCodeVerifier: func() (string, error) { return "verifier", nil }, GenerateSessionID: func() (string, error) { return "session", nil }, GenerateCodeChallenge: func(v string) string { return v }, BuildAuthorizationURL: func(string, string) string { return "https://fixture.invalid/authorize" }, IsConnectionError: func(error) bool { return false }, RetryBackoff: func(int) time.Duration { return 0 }, Printf: func(string, ...any) {}, Warn: func(string, ...any) {}, Info: func(string, ...any) {}}
}

// TestAntigravityAuthorizationConsumptionAndPrivacyOrder rejects wrong state
// and preserves sessions after exchange failure. It checks successful protocol
// event order, plan/project/privacy metadata, expiry margin, and consumed
// proof rejection.
func TestAntigravityAuthorizationConsumptionAndPrivacyOrder(t *testing.T) {
	events := []string{}
	client := &antigravityAuthFixture{events: &events}
	core := NewAntigravityAuthorization(authFixtureOptions(client))
	defer func() { require.NoError(t, core.StopContext(context.Background())) }()
	require.False(t, core.Store.runtimeStarted, "构造不得启动清理")
	generated, err := core.GenerateAuthURL(context.Background(), nil)
	require.NoError(t, err)
	_, err = core.ExchangeCode(context.Background(), &AntigravityExchangeCodeInput{SessionID: generated.SessionID, State: "wrong", Code: "code"})
	require.Error(t, err)
	require.Empty(t, events)
	client.exchangeErr = errors.New("exchange failed")
	_, err = core.ExchangeCode(context.Background(), &AntigravityExchangeCodeInput{SessionID: generated.SessionID, State: "state", Code: "code"})
	require.Error(t, err)
	_, exists := core.Store.Get(generated.SessionID)
	require.True(t, exists, "交换失败必须保留原会话")
	events = nil
	client.exchangeErr = nil
	got, err := core.ExchangeCode(context.Background(), &AntigravityExchangeCodeInput{SessionID: generated.SessionID, State: "state", Code: "code"})
	require.NoError(t, err)
	require.Equal(t, []string{"exchange", "user", "load", "set-privacy", "verify-privacy"}, events)
	require.Equal(t, "Pro", got.PlanType)
	require.Equal(t, "project", got.ProjectID)
	require.Equal(t, AntigravityPrivacySet, got.PrivacyMode)
	require.InDelta(t, time.Now().Add(55*time.Minute).Unix(), got.ExpiresAt, 2)
	_, exists = core.Store.Get(generated.SessionID)
	require.False(t, exists)
	_, err = core.ExchangeCode(context.Background(), &AntigravityExchangeCodeInput{SessionID: generated.SessionID, State: "state", Code: "code"})
	require.Error(t, err)
}

// TestAntigravityAuthorizationStopWaitsAndReportsBudget blocks a refresh
// callback beyond shutdown budget. It checks deadline reporting, stopped
// admission, eventual refresh failure, stopped cleanup, and retention of the
// first shutdown result.
func TestAntigravityAuthorizationStopWaitsAndReportsBudget(t *testing.T) {
	entered, finish := make(chan struct{}), make(chan struct{})
	client := &antigravityAuthFixture{refresh: func(context.Context) (*google.AntigravityTokenResponse, error) {
		// Keep refresh active after shutdown cancellation so the budget reports
		// unfinished work. Explicit release lets the original registered operation
		// finally return.
		close(entered)
		<-finish
		return nil, errors.New("invalid_grant: fixture")
	}}
	core := NewAntigravityAuthorization(authFixtureOptions(client))
	core.Start()
	core.Start()
	done := make(chan error, 1)
	go func() { _, err := core.RefreshToken(context.Background(), "refresh", ""); done <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("刷新未进入")
	}
	budget, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := core.StopContext(budget)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	_, err = core.GenerateAuthURL(context.Background(), nil)
	require.ErrorContains(t, err, "stopped")
	core.Start()
	require.True(t, core.Store.runtimeStopped)
	// Release the noncooperative callback after checking stopped admission so
	// its later return cannot hide the first shutdown deadline result.
	close(finish)
	select {
	case err := <-done:
		require.ErrorContains(t, err, "invalid_grant")
	case <-time.After(time.Second):
		t.Fatal("刷新未收尾")
	}
	require.ErrorIs(t, core.StopContext(context.Background()), context.DeadlineExceeded, "重复 Stop 共用第一次结果")
}

// TestResolveDefaultTierID supplies absent, incomplete, nondefault, and
// whitespace-padded allowed-tier objects. It checks empty unresolved results
// and the trimmed ID selected from the configured default entries.
func TestResolveDefaultTierID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string         // default tier case name
		loadRaw map[string]any // input raw tier response
		want    string         // expected selected tier identifier
	}{
		{
			name:    "nil loadRaw",
			loadRaw: nil,
			want:    "",
		},
		{
			name: "missing allowedTiers",
			loadRaw: map[string]any{
				"paidTier": map[string]any{"id": "g1-pro-tier"},
			},
			want: "",
		},
		{
			name:    "empty allowedTiers",
			loadRaw: map[string]any{"allowedTiers": []any{}},
			want:    "",
		},
		{
			name: "tier missing id field",
			loadRaw: map[string]any{
				"allowedTiers": []any{
					map[string]any{"isDefault": true},
				},
			},
			want: "",
		},
		{
			name: "allowedTiers but no default",
			loadRaw: map[string]any{
				"allowedTiers": []any{
					map[string]any{"id": "free-tier", "isDefault": false},
					map[string]any{"id": "standard-tier", "isDefault": false},
				},
			},
			want: "",
		},
		{
			name: "default tier found",
			loadRaw: map[string]any{
				"allowedTiers": []any{
					map[string]any{"id": "free-tier", "isDefault": true},
					map[string]any{"id": "standard-tier", "isDefault": false},
				},
			},
			want: "free-tier",
		},
		{
			name: "default tier id with spaces",
			loadRaw: map[string]any{
				"allowedTiers": []any{
					map[string]any{"id": "  standard-tier  ", "isDefault": true},
				},
			},
			want: "standard-tier",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := ResolveAntigravityDefaultTierID(tc.loadRaw)
			if got != tc.want {
				t.Fatalf("resolveDefaultTierID() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestAntigravityAuthorizationExchangeCodeIdempotentProjectCompletion verifies
// that standard-tier accounts missing an auto-provisioned companion project
// retain the exchanged session token, reject numeric Project Numbers, and
// complete on retry with a valid ProjectID without repeating Google token
// exchange.
func TestAntigravityAuthorizationExchangeCodeIdempotentProjectCompletion(t *testing.T) {
	events := []string{}
	client := &antigravityAuthFixture{
		events: &events,
		loadFn: func(string) (*google.AntigravityLoadCodeAssistResponse, map[string]any, error) {
			return &google.AntigravityLoadCodeAssistResponse{
				CurrentTier: &google.AntigravityTierInfo{ID: "standard-tier"},
			}, nil, nil
		},
	}
	core := NewAntigravityAuthorization(authFixtureOptions(client))
	defer func() { require.NoError(t, core.StopContext(context.Background())) }()

	generated, err := core.GenerateAuthURL(context.Background(), nil)
	require.NoError(t, err)

	_, err = core.ExchangeCode(context.Background(), &AntigravityExchangeCodeInput{
		SessionID: generated.SessionID,
		State:     generated.State,
		Code:      "ag-code-1",
	})
	require.ErrorContains(t, err, "missing project_id")
	require.ErrorContains(t, err, "no re-login needed")

	_, stillStored := core.Store.Get(generated.SessionID)
	require.True(t, stillStored)

	// Numeric GCP Project Numbers must fail before token reuse consumes the session.
	_, err = core.ExchangeCode(context.Background(), &AntigravityExchangeCodeInput{
		SessionID: generated.SessionID,
		State:     generated.State,
		Code:      "ag-code-1",
		ProjectID: "1234567890",
	})
	require.ErrorContains(t, err, "numeric GCP Project Number")

	events = nil
	got, err := core.ExchangeCode(context.Background(), &AntigravityExchangeCodeInput{
		SessionID: generated.SessionID,
		State:     generated.State,
		Code:      "ag-code-1",
		ProjectID: "my-ag-gcp-project",
	})
	require.NoError(t, err)
	require.Equal(t, "my-ag-gcp-project", got.ProjectID)
	require.NotContains(t, events, "exchange", "retry must reuse cached session token without repeating Google exchange")
	require.Contains(t, events, "load:my-ag-gcp-project")

	_, stillStored = core.Store.Get(generated.SessionID)
	require.False(t, stillStored)

	// VALIDATION_REQUIRED surfaces validationUrl while keeping the session alive.
	client.loadFn = func(string) (*google.AntigravityLoadCodeAssistResponse, map[string]any, error) {
		return &google.AntigravityLoadCodeAssistResponse{
			IneligibleTiers: []*google.AntigravityIneligibleTier{{
				ReasonCode:    "VALIDATION_REQUIRED",
				ReasonMessage: "Phone verification required",
				ValidationURL: "https://accounts.google.com/verify-ag",
			}},
		}, nil, nil
	}
	valSession, err := core.GenerateAuthURL(context.Background(), nil)
	require.NoError(t, err)
	_, err = core.ExchangeCode(context.Background(), &AntigravityExchangeCodeInput{
		SessionID: valSession.SessionID,
		State:     valSession.State,
		Code:      "ag-code-val",
	})
	require.ErrorContains(t, err, "https://accounts.google.com/verify-ag")
	_, stillStored = core.Store.Get(valSession.SessionID)
	require.True(t, stillStored)

	// ValidateRefreshTokenWithProject rejects missing project_id with 400 and accepts explicit hint.
	client.refresh = func(context.Context) (*google.AntigravityTokenResponse, error) {
		return &google.AntigravityTokenResponse{AccessToken: "rt-access", RefreshToken: "rt-new", ExpiresIn: 3600, TokenType: "Bearer"}, nil
	}
	client.loadFn = func(string) (*google.AntigravityLoadCodeAssistResponse, map[string]any, error) {
		return &google.AntigravityLoadCodeAssistResponse{
			CurrentTier: &google.AntigravityTierInfo{ID: "standard-tier"},
		}, nil, nil
	}
	_, err = core.ValidateRefreshTokenWithProject(context.Background(), "rt-input", "", nil)
	require.ErrorContains(t, err, "missing project_id")

	validated, err := core.ValidateRefreshTokenWithProject(context.Background(), "rt-input", "my-rt-gcp-project", nil)
	require.NoError(t, err)
	require.Equal(t, "my-rt-gcp-project", validated.ProjectID)
}
