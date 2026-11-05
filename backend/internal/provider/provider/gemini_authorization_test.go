package provider

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	geminicli "github.com/TokenFlux/TokenRouter/internal/upstream/gemini/codeassist"
)

// Gemini OAuth variants choose different effective clients, callback addresses
// and project requirements. These cases exercise gemini_authorization.go
// option bindings through core URL generation, refresh, project discovery and
// credential construction, checking built-in CLI selection, custom AI Studio
// eligibility, canonical tiers and omission of invalid or absent optional
// fields.
//
// Injected client functions distinguish terminal token rejection from
// transient refresh retry and allow missing-project/Drive failures to be
// tested without supplier HTTP. Proxy lookup and configuration are retained in
// the helper's callbacks, so a selected provider carries its own proxy and
// OAuth variant into refresh. The URL strategy parent fixes its process secret
// before parallel rows inspect client/scope/state data; other fixtures use
// per-case mutable state and explicit service lifecycle helpers.

// mockGeminiOAuthClient routes exchange and refresh calls through configured
// test callbacks.
type mockGeminiOAuthClient struct {
	// exchangeCodeFunc receives stored variant, code/proof, redirect and proxy
	// inputs. A missing callback makes ExchangeCode panic so an unexpected
	// exchange is visible.
	exchangeCodeFunc func(ctx context.Context, oauthType, code, codeVerifier, redirectURI, proxyURL string) (*geminicli.TokenResponse, error)
	// refreshTokenFunc receives the variant and refresh credential selected by
	// the core. A missing callback makes RefreshToken panic rather than
	// producing default access data.
	refreshTokenFunc func(ctx context.Context, oauthType, refreshToken, proxyURL string) (*geminicli.TokenResponse, error)
}

// mockGeminiCodeAssistClient supplies configured project/tier discovery and
// onboarding responses for tests.
type mockGeminiCodeAssistClient struct {
	// loadCodeAssistFunc supplies project and tier metadata for the request
	// pointer. A missing callback makes that query panic, exposing discovery
	// not configured by the case.
	loadCodeAssistFunc func(ctx context.Context, accessToken, proxyURL string, req *geminicli.LoadCodeAssistRequest) (*geminicli.LoadCodeAssistResponse, error)
	// onboardUserFunc supplies onboarding responses for the requested
	// project/tier. A missing callback makes OnboardUser panic when discovery
	// unexpectedly reaches that path.
	onboardUserFunc func(ctx context.Context, accessToken, proxyURL string, req *geminicli.OnboardUserRequest) (*geminicli.OnboardUserResponse, error)
}

// mockGeminiProxyRepo supports exact configured lookup and rejects
// unconfigured proxy inventory operations.
type mockGeminiProxyRepo struct {
	// getByIDFunc supplies proxy lookup with the selected ID and context. A
	// nil callback returns the fixture not-found error; proxy inventory/writes
	// are unsupported.
	getByIDFunc func(ctx context.Context, id int64) (*egress.Proxy, error)
}

// mockDriveClient supplies configured storage metadata or a stable
// unavailable-query error.
type mockDriveClient struct {
	// getStorageQuotaFunc supplies Drive quota with access and proxy inputs. A
	// nil callback returns the stable fixture unavailable error used for tier
	// defaulting.
	getStorageQuotaFunc func(ctx context.Context, accessToken, proxyURL string) (*geminicli.DriveStorageInfo, error)
}

// TestGeminiOAuthService_GenerateAuthURL_RedirectURIStrategy varies CLI and AI
// Studio configuration with a supplied callback hint. It checks effective
// client, redirect, scope, state/project fields and rejection when AI Studio
// lacks custom credentials.
func TestGeminiOAuthService_GenerateAuthURL_RedirectURIStrategy(t *testing.T) {
	// Fix the process-wide CLI secret before parallel rows read effective settings.
	// The parent owns the environment restoration, while rows keep this shared
	// value immutable.

	// testCase describes variant inputs and the expected browser URL or
	// validation error.
	type testCase struct {
		name string // subtest display label
		// cfg is retained by the configuration callback for this row. Keep its
		// client fields stable while that parallel row reads effective OAuth
		// settings.
		cfg           *geminicli.OAuthConfig
		oauthType     string // selected OAuth variant name
		projectID     string // supplied project identity hint
		wantClientID  string // expected effective client identity
		wantRedirect  string // expected authorization callback URI
		wantScope     string // expected authorization scope string
		wantProjectID string // expected URL project hint
		// wantErrSubstr selects a rejected configuration row. A nonempty value
		// checks the error and returns before the successful URL fields are
		// inspected.
		wantErrSubstr string
	}

	tests := []testCase{
		{
			name: "google_one uses built-in client when not configured and redirects to upstream",
			cfg:  &geminicli.OAuthConfig{},

			oauthType:     "google_one",
			wantClientID:  geminicli.GeminiCLIOAuthClientID,
			wantRedirect:  geminicli.GeminiCLIRedirectURI,
			wantScope:     geminicli.DefaultCodeAssistScopes,
			wantProjectID: "",
		},
		{
			name: "google_one always forces built-in client even when custom client configured",
			cfg: &geminicli.OAuthConfig{
				ClientID:     "custom-client-id",
				ClientSecret: "custom-client-secret",
			},

			oauthType:     "google_one",
			wantClientID:  geminicli.GeminiCLIOAuthClientID,
			wantRedirect:  geminicli.GeminiCLIRedirectURI,
			wantScope:     geminicli.DefaultCodeAssistScopes,
			wantProjectID: "",
		},
		{
			name: "code_assist always forces built-in client even when custom client configured",
			cfg: &geminicli.OAuthConfig{
				ClientID:     "custom-client-id",
				ClientSecret: "custom-client-secret",
			},

			oauthType:     "code_assist",
			projectID:     "my-gcp-project",
			wantClientID:  geminicli.GeminiCLIOAuthClientID,
			wantRedirect:  geminicli.GeminiCLIRedirectURI,
			wantScope:     geminicli.DefaultCodeAssistScopes,
			wantProjectID: "my-gcp-project",
		},
		{
			name: "ai_studio requires custom client",
			cfg:  &geminicli.OAuthConfig{},

			oauthType:     "ai_studio",
			wantErrSubstr: "AI Studio OAuth requires a custom OAuth Client",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			svc := newGeminiAuthorizationForTest(nil, nil, nil, nil, tt.cfg)
			svc.Start()
			got, err := svc.GenerateAuthURL(context.Background(), nil, "https://example.com/auth/callback", tt.projectID, tt.oauthType, "")
			if tt.wantErrSubstr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tt.wantErrSubstr)
				}
				if !strings.Contains(err.Error(), tt.wantErrSubstr) {
					t.Fatalf("expected error containing %q, got: %v", tt.wantErrSubstr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("GenerateAuthURL returned error: %v", err)
			}

			parsed, err := url.Parse(got.AuthURL)
			if err != nil {
				t.Fatalf("failed to parse auth_url: %v", err)
			}
			q := parsed.Query()

			if gotState := q.Get("state"); gotState != got.State {
				t.Fatalf("state mismatch: query=%q result=%q", gotState, got.State)
			}
			if gotClientID := q.Get("client_id"); gotClientID != tt.wantClientID {
				t.Fatalf("client_id mismatch: got=%q want=%q", gotClientID, tt.wantClientID)
			}
			if gotRedirect := q.Get("redirect_uri"); gotRedirect != tt.wantRedirect {
				t.Fatalf("redirect_uri mismatch: got=%q want=%q", gotRedirect, tt.wantRedirect)
			}
			if gotScope := q.Get("scope"); gotScope != tt.wantScope {
				t.Fatalf("scope mismatch: got=%q want=%q", gotScope, tt.wantScope)
			}
			if gotProjectID := q.Get("project_id"); gotProjectID != tt.wantProjectID {
				t.Fatalf("project_id mismatch: got=%q want=%q", gotProjectID, tt.wantProjectID)
			}
		})
	}
}

// TestGeminiOAuthService_BuildProviderCredentials constructs complete and
// minimal token maps plus invalid tier inputs. It checks decimal expiry
// encoding, optional field omission, Extra-field inclusion and exclusion of
// invalid/overlong tier identifiers.
func TestGeminiOAuthService_BuildProviderCredentials(t *testing.T) {
	t.Parallel()

	svc := newGeminiAuthorizationForTest(nil, nil, nil, nil, &geminicli.OAuthConfig{})
	svc.Start()
	defer stopGeminiAuthorization(t, svc)

	t.Run("完整字段", func(t *testing.T) {
		t.Parallel()
		tokenInfo := &providercore.GeminiTokenInfo{
			AccessToken:  "access-123",
			RefreshToken: "refresh-456",
			ExpiresIn:    3600,
			ExpiresAt:    1700000000,
			TokenType:    "Bearer",
			Scope:        "openid email",
			ProjectID:    "my-project",
			TierID:       "gcp_standard",
			OAuthType:    "code_assist",
			Extra: map[string]any{
				"drive_storage_limit": int64(2199023255552),
			},
		}

		creds := svc.BuildProviderCredentials(tokenInfo)

		assertCredStr(t, creds, "access_token", "access-123")
		assertCredStr(t, creds, "refresh_token", "refresh-456")
		assertCredStr(t, creds, "token_type", "Bearer")
		assertCredStr(t, creds, "scope", "openid email")
		assertCredStr(t, creds, "project_id", "my-project")
		assertCredStr(t, creds, "tier_id", "gcp_standard")
		assertCredStr(t, creds, "oauth_type", "code_assist")
		assertCredStr(t, creds, "expires_at", "1700000000")

		if _, ok := creds["drive_storage_limit"]; !ok {
			t.Fatal("extra 字段 drive_storage_limit 未包含在 creds 中")
		}
	})

	t.Run("最小字段（仅 access_token 和 expires_at）", func(t *testing.T) {
		t.Parallel()
		tokenInfo := &providercore.GeminiTokenInfo{
			AccessToken: "token-only",
			ExpiresAt:   1700000000,
		}

		creds := svc.BuildProviderCredentials(tokenInfo)

		assertCredStr(t, creds, "access_token", "token-only")
		assertCredStr(t, creds, "expires_at", "1700000000")

		for _, key := range []string{"refresh_token", "token_type", "scope", "project_id", "tier_id", "oauth_type"} {
			if _, ok := creds[key]; ok {
				t.Fatalf("不应包含空字段 %q", key)
			}
		}
	})

	t.Run("无效 tier_id 被静默跳过", func(t *testing.T) {
		t.Parallel()
		tokenInfo := &providercore.GeminiTokenInfo{
			AccessToken: "token",
			ExpiresAt:   1700000000,
			TierID:      "tier with spaces",
		}

		creds := svc.BuildProviderCredentials(tokenInfo)

		if _, ok := creds["tier_id"]; ok {
			t.Fatal("无效 tier_id 不应被存入 creds")
		}
	})

	t.Run("超长 tier_id 被静默跳过", func(t *testing.T) {
		t.Parallel()
		tokenInfo := &providercore.GeminiTokenInfo{
			AccessToken: "token",
			ExpiresAt:   1700000000,
			// Sixty-five ASCII bytes exceed the validated 64-byte tier
			// identifier limit.
			TierID: strings.Repeat("x", 65),
		}

		creds := svc.BuildProviderCredentials(tokenInfo)

		if _, ok := creds["tier_id"]; ok {
			t.Fatal("超长 tier_id 不应被存入 creds")
		}
	})

	t.Run("无 extra 字段", func(t *testing.T) {
		t.Parallel()
		tokenInfo := &providercore.GeminiTokenInfo{
			AccessToken:  "token",
			ExpiresAt:    1700000000,
			RefreshToken: "rt",
		}

		creds := svc.BuildProviderCredentials(tokenInfo)

		if len(creds) != 3 { // access_token, expires_at, refresh_token
			t.Fatalf("creds 字段数量不匹配: got=%d want=3, keys=%v", len(creds), credKeys(creds))
		}
	})
}

// TestGeminiOAuthService_GetOAuthConfig compares absent, partial, built-in and
// custom client settings. It checks trimmed custom-pair eligibility and the
// required AI Studio callback address for each capability result.
func TestGeminiOAuthService_GetOAuthConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string // subtest display label
		// cfg supplies the capability row settings by a retained pointer. The
		// helper returns its current value whenever GetOAuthConfig reads
		// configuration.
		cfg         *geminicli.OAuthConfig
		wantEnabled bool // expected AI Studio eligibility
	}{
		{
			name: "无自定义 OAuth 客户端",
			cfg:  &geminicli.OAuthConfig{},

			wantEnabled: false,
		},
		{
			name: "仅 ClientID 无 ClientSecret",
			cfg:  &geminicli.OAuthConfig{ClientID: "custom-id"},

			wantEnabled: false,
		},
		{
			name: "仅 ClientSecret 无 ClientID",
			cfg:  &geminicli.OAuthConfig{ClientSecret: "custom-secret"},

			wantEnabled: false,
		},
		{
			name: "使用内置 Gemini CLI ClientID（不算自定义）",
			cfg: &geminicli.OAuthConfig{
				ClientID:     geminicli.GeminiCLIOAuthClientID,
				ClientSecret: "some-secret",
			},

			wantEnabled: false,
		},
		{
			name: "自定义 OAuth 客户端（非内置 ID）",
			cfg: &geminicli.OAuthConfig{
				ClientID:     "my-custom-client-id",
				ClientSecret: "my-custom-client-secret",
			},

			wantEnabled: true,
		},
		{
			name: "带空白的自定义客户端",
			cfg: &geminicli.OAuthConfig{
				ClientID:     "  my-custom-client-id  ",
				ClientSecret: "  my-custom-client-secret  ",
			},

			wantEnabled: true,
		},
		{
			name: "纯空白字符串不算配置",
			cfg: &geminicli.OAuthConfig{
				ClientID:     "   ",
				ClientSecret: "   ",
			},

			wantEnabled: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			svc := newGeminiAuthorizationForTest(nil, nil, nil, nil, tt.cfg)
			svc.Start()
			defer stopGeminiAuthorization(t, svc)

			result := svc.GetOAuthConfig()
			if result.AIStudioOAuthEnabled != tt.wantEnabled {
				t.Fatalf("AIStudioOAuthEnabled = %v, want %v", result.AIStudioOAuthEnabled, tt.wantEnabled)
			}
			if len(result.RequiredRedirectURIs) != 1 || result.RequiredRedirectURIs[0] != geminicli.AIStudioOAuthRedirectURI {
				t.Fatalf("RequiredRedirectURIs 不匹配: got=%v", result.RequiredRedirectURIs)
			}
		})
	}
}

// TestGeminiOAuthService_Stop_NoPanic starts one core and invokes its stop
// helper twice. It checks repeated shutdown returns with no lifecycle error or
// duplicate stop panic for that service instance.
func TestGeminiOAuthService_Stop_NoPanic(t *testing.T) {
	t.Parallel()

	svc := newGeminiAuthorizationForTest(nil, nil, nil, nil, &geminicli.OAuthConfig{})
	svc.Start()

	stopGeminiAuthorization(t, svc)
	// Stopping an already stopped service is safe.
	stopGeminiAuthorization(t, svc)
}

// TestGeminiOAuthService_RefreshToken_Success supplies a successful refresh
// response. It checks replacement access/refresh fields and nonzero computed
// expiry through the core's converted token result for the selected OAuth
// variant.
func TestGeminiOAuthService_RefreshToken_Success(t *testing.T) {
	t.Parallel()

	client := &mockGeminiOAuthClient{
		refreshTokenFunc: func(ctx context.Context, oauthType, refreshToken, proxyURL string) (*geminicli.TokenResponse, error) {
			return &geminicli.TokenResponse{
				AccessToken:  "new-access",
				RefreshToken: "new-refresh",
				TokenType:    "Bearer",
				ExpiresIn:    3600,
				Scope:        "openid",
			}, nil
		},
	}

	svc := newGeminiAuthorizationForTest(nil, client, nil, nil, &geminicli.OAuthConfig{})
	svc.Start()
	defer stopGeminiAuthorization(t, svc)

	info, err := svc.RefreshToken(context.Background(), "code_assist", "old-refresh", "")
	if err != nil {
		t.Fatalf("RefreshToken 返回错误: %v", err)
	}
	if info.AccessToken != "new-access" {
		t.Fatalf("AccessToken 不匹配: got=%q", info.AccessToken)
	}
	if info.RefreshToken != "new-refresh" {
		t.Fatalf("RefreshToken 不匹配: got=%q", info.RefreshToken)
	}
	if info.ExpiresAt == 0 {
		t.Fatal("ExpiresAt 不应为 0")
	}
}

// TestGeminiOAuthService_RefreshToken_NonRetryableError returns invalid_grant
// from the configured refresh client. It checks the terminal rejection remains
// an error carrying invalid_grant after that rejected supplier exchange.
func TestGeminiOAuthService_RefreshToken_NonRetryableError(t *testing.T) {
	t.Parallel()

	client := &mockGeminiOAuthClient{
		refreshTokenFunc: func(ctx context.Context, oauthType, refreshToken, proxyURL string) (*geminicli.TokenResponse, error) {
			return nil, fmt.Errorf("invalid_grant: token revoked")
		},
	}

	svc := newGeminiAuthorizationForTest(nil, client, nil, nil, &geminicli.OAuthConfig{})
	svc.Start()
	defer stopGeminiAuthorization(t, svc)

	_, err := svc.RefreshToken(context.Background(), "code_assist", "revoked-token", "")
	if err == nil {
		t.Fatal("RefreshToken 应返回错误（不可重试的 invalid_grant）")
	}
	if !strings.Contains(err.Error(), "invalid_grant") {
		t.Fatalf("错误应包含 invalid_grant: got=%q", err.Error())
	}
}

// TestGeminiOAuthService_RefreshToken_RetryableError fails two client calls
// with temporary network errors before success. It checks the recovered access
// result and at least three invocations through transient retry handling.
func TestGeminiOAuthService_RefreshToken_RetryableError(t *testing.T) {
	t.Parallel()

	callCount := 0
	client := &mockGeminiOAuthClient{
		refreshTokenFunc: func(ctx context.Context, oauthType, refreshToken, proxyURL string) (*geminicli.TokenResponse, error) {
			callCount++
			// Fail the first two exchanges so the core must exercise transient
			// recovery
			// before the third response can supply the access token asserted below.
			if callCount <= 2 {
				return nil, fmt.Errorf("temporary network error")
			}
			return &geminicli.TokenResponse{
				AccessToken: "recovered",
				ExpiresIn:   3600,
			}, nil
		},
	}

	svc := newGeminiAuthorizationForTest(nil, client, nil, nil, &geminicli.OAuthConfig{})
	svc.Start()
	defer stopGeminiAuthorization(t, svc)

	info, err := svc.RefreshToken(context.Background(), "code_assist", "rt", "")
	if err != nil {
		t.Fatalf("RefreshToken 应在重试后成功: %v", err)
	}
	if info.AccessToken != "recovered" {
		t.Fatalf("AccessToken 不匹配: got=%q", info.AccessToken)
	}
	if callCount < 3 {
		t.Fatalf("应至少调用 3 次（2 次失败 + 1 次成功）: got=%d", callCount)
	}
}

// TestGeminiOAuthService_RefreshProviderToken_NotGeminiOAuth supplies an
// Anthropic OAuth record to Gemini refresh. It checks the platform/kind
// diagnostic before unconfigured supplier work can return a token result.
func TestGeminiOAuthService_RefreshProviderToken_NotGeminiOAuth(t *testing.T) {
	t.Parallel()

	svc := newGeminiAuthorizationForTest(nil, nil, nil, nil, &geminicli.OAuthConfig{})
	svc.Start()
	defer stopGeminiAuthorization(t, svc)

	provider := &providercore.Record{
		Platform: capability.PlatformAnthropic,
		Type:     capability.ProviderTypeOAuth,
	}

	_, err := svc.RefreshProviderToken(context.Background(), provider)
	if err == nil {
		t.Fatal("应返回错误（非 Gemini OAuth 提供商）")
	}
	if !strings.Contains(err.Error(), "not a Gemini OAuth provider") {
		t.Fatalf("错误信息不匹配: got=%q", err.Error())
	}
}

// TestGeminiOAuthService_RefreshProviderToken_NoRefreshToken supplies Gemini
// access metadata without a refresh credential. It checks the missing-refresh
// diagnostic rather than accepting that incomplete authentication input for
// provider exchange.
func TestGeminiOAuthService_RefreshProviderToken_NoRefreshToken(t *testing.T) {
	t.Parallel()

	svc := newGeminiAuthorizationForTest(nil, nil, nil, nil, &geminicli.OAuthConfig{})
	svc.Start()
	defer stopGeminiAuthorization(t, svc)

	provider := &providercore.Record{
		Platform: capability.PlatformGemini,
		Type:     capability.ProviderTypeOAuth,
		Credentials: map[string]any{
			"access_token": "at",
			"oauth_type":   "code_assist",
		},
	}

	_, err := svc.RefreshProviderToken(context.Background(), provider)
	if err == nil {
		t.Fatal("应返回错误（无 refresh_token）")
	}
	if !strings.Contains(err.Error(), "no refresh token") {
		t.Fatalf("错误信息不匹配: got=%q", err.Error())
	}
}

// TestGeminiOAuthService_RefreshProviderToken_AIStudio refreshes stored AI
// Studio credentials with no project. It checks replacement access input and
// the retained AI Studio variant, whose project field is optional on this
// route.
func TestGeminiOAuthService_RefreshProviderToken_AIStudio(t *testing.T) {
	t.Parallel()

	client := &mockGeminiOAuthClient{
		refreshTokenFunc: func(ctx context.Context, oauthType, refreshToken, proxyURL string) (*geminicli.TokenResponse, error) {
			return &geminicli.TokenResponse{
				AccessToken:  "refreshed-at",
				RefreshToken: "refreshed-rt",
				ExpiresIn:    3600,
				TokenType:    "Bearer",
			}, nil
		},
	}

	svc := newGeminiAuthorizationForTest(&mockGeminiProxyRepo{}, client, nil, nil, &geminicli.OAuthConfig{})
	svc.Start()
	defer stopGeminiAuthorization(t, svc)

	provider := &providercore.Record{
		Platform: capability.PlatformGemini,
		Type:     capability.ProviderTypeOAuth,
		Credentials: map[string]any{
			"access_token":  "old-at",
			"refresh_token": "old-rt",
			"oauth_type":    "ai_studio",
			"tier_id":       "aistudio_free",
		},
	}

	info, err := svc.RefreshProviderToken(context.Background(), provider)
	if err != nil {
		t.Fatalf("RefreshProviderToken 返回错误: %v", err)
	}
	if info.AccessToken != "refreshed-at" {
		t.Fatalf("AccessToken 不匹配: got=%q", info.AccessToken)
	}
	if info.OAuthType != "ai_studio" {
		t.Fatalf("OAuthType 不匹配: got=%q", info.OAuthType)
	}
}

// TestGeminiOAuthService_RefreshProviderToken_CodeAssist_WithProjectID
// refreshes a Code Assist record with stored project/tier. It checks that
// project and variant remain available alongside canonical GCP Standard tier
// metadata.
func TestGeminiOAuthService_RefreshProviderToken_CodeAssist_WithProjectID(t *testing.T) {
	t.Parallel()

	client := &mockGeminiOAuthClient{
		refreshTokenFunc: func(ctx context.Context, oauthType, refreshToken, proxyURL string) (*geminicli.TokenResponse, error) {
			return &geminicli.TokenResponse{
				AccessToken:  "refreshed",
				RefreshToken: "new-rt",
				ExpiresIn:    3600,
			}, nil
		},
	}

	svc := newGeminiAuthorizationForTest(&mockGeminiProxyRepo{}, client, nil, nil, &geminicli.OAuthConfig{})
	svc.Start()
	defer stopGeminiAuthorization(t, svc)

	provider := &providercore.Record{
		Platform: capability.PlatformGemini,
		Type:     capability.ProviderTypeOAuth,
		Credentials: map[string]any{
			"access_token":  "old-at",
			"refresh_token": "old-rt",
			"oauth_type":    "code_assist",
			"project_id":    "my-project",
			"tier_id":       "gcp_standard",
		},
	}

	info, err := svc.RefreshProviderToken(context.Background(), provider)
	if err != nil {
		t.Fatalf("RefreshProviderToken 返回错误: %v", err)
	}
	if info.ProjectID != "my-project" {
		t.Fatalf("ProjectID 应保留: got=%q", info.ProjectID)
	}
	if info.TierID != providercore.GeminiTierGCPStandard {
		t.Fatalf("TierID 不匹配: got=%q want=%q", info.TierID, providercore.GeminiTierGCPStandard)
	}
	if info.OAuthType != "code_assist" {
		t.Fatalf("OAuthType 不匹配: got=%q", info.OAuthType)
	}
}

// TestGeminiOAuthService_RefreshProviderToken_DefaultOAuthType omits
// oauth_type from legacy credentials while retaining project data. It checks
// the client receives Code Assist and the converted token reports that default
// variant.
func TestGeminiOAuthService_RefreshProviderToken_DefaultOAuthType(t *testing.T) {
	t.Parallel()

	client := &mockGeminiOAuthClient{
		refreshTokenFunc: func(ctx context.Context, oauthType, refreshToken, proxyURL string) (*geminicli.TokenResponse, error) {
			if oauthType != "code_assist" {
				t.Errorf("默认 oauthType 应为 code_assist: got=%q", oauthType)
			}
			return &geminicli.TokenResponse{
				AccessToken: "refreshed",
				ExpiresIn:   3600,
			}, nil
		},
	}

	svc := newGeminiAuthorizationForTest(&mockGeminiProxyRepo{}, client, nil, nil, &geminicli.OAuthConfig{})
	svc.Start()
	defer stopGeminiAuthorization(t, svc)

	// Legacy credentials omit oauth_type.
	provider := &providercore.Record{
		Platform: capability.PlatformGemini,
		Type:     capability.ProviderTypeOAuth,
		Credentials: map[string]any{
			"refresh_token": "old-rt",
			"project_id":    "proj",
			"tier_id":       "STANDARD",
		},
	}

	info, err := svc.RefreshProviderToken(context.Background(), provider)
	if err != nil {
		t.Fatalf("RefreshProviderToken 返回错误: %v", err)
	}
	if info.OAuthType != "code_assist" {
		t.Fatalf("OAuthType 应默认为 code_assist: got=%q", info.OAuthType)
	}
}

// TestGeminiOAuthService_RefreshProviderToken_WithProxy supplies a selected
// HTTP proxy and configured refresh client. It checks the resolved proxy URL
// reaches provider refresh and the operation returns without an exchange
// error.
func TestGeminiOAuthService_RefreshProviderToken_WithProxy(t *testing.T) {
	t.Parallel()

	proxyRepo := &mockGeminiProxyRepo{
		getByIDFunc: func(ctx context.Context, id int64) (*egress.Proxy, error) {
			return &egress.Proxy{
				Protocol: "http",
				Host:     "proxy.test",
				Port:     3128,
			}, nil
		},
	}

	client := &mockGeminiOAuthClient{
		refreshTokenFunc: func(ctx context.Context, oauthType, refreshToken, proxyURL string) (*geminicli.TokenResponse, error) {
			if proxyURL != "http://proxy.test:3128" {
				t.Errorf("proxyURL 不匹配: got=%q", proxyURL)
			}
			return &geminicli.TokenResponse{
				AccessToken: "refreshed",
				ExpiresIn:   3600,
			}, nil
		},
	}

	svc := newGeminiAuthorizationForTest(proxyRepo, client, nil, nil, &geminicli.OAuthConfig{})
	svc.Start()
	defer stopGeminiAuthorization(t, svc)

	proxyID := int64(5)
	provider := &providercore.Record{
		Platform: capability.PlatformGemini,
		Type:     capability.ProviderTypeOAuth,
		ProxyID:  &proxyID,
		Credentials: map[string]any{
			"refresh_token": "rt",
			"oauth_type":    "code_assist",
			"project_id":    "proj",
		},
	}

	_, err := svc.RefreshProviderToken(context.Background(), provider)
	if err != nil {
		t.Fatalf("RefreshProviderToken 返回错误: %v", err)
	}
}

// TestGeminiOAuthService_RefreshProviderToken_CodeAssist_NoProjectID_AutoDetect
// omits project input while discovery returns a Code Assist project and tier.
// It checks the converted result contains that detected project and canonical
// tier.
func TestGeminiOAuthService_RefreshProviderToken_CodeAssist_NoProjectID_AutoDetect(t *testing.T) {
	t.Parallel()

	client := &mockGeminiOAuthClient{
		refreshTokenFunc: func(ctx context.Context, oauthType, refreshToken, proxyURL string) (*geminicli.TokenResponse, error) {
			return &geminicli.TokenResponse{
				AccessToken: "at",
				ExpiresIn:   3600,
			}, nil
		},
	}

	codeAssist := &mockGeminiCodeAssistClient{
		loadCodeAssistFunc: func(ctx context.Context, accessToken, proxyURL string, req *geminicli.LoadCodeAssistRequest) (*geminicli.LoadCodeAssistResponse, error) {
			return &geminicli.LoadCodeAssistResponse{
				CloudAICompanionProject: "auto-project-123",
				CurrentTier:             &geminicli.TierInfo{ID: "STANDARD"},
			}, nil
		},
	}

	svc := newGeminiAuthorizationForTest(&mockGeminiProxyRepo{}, client, codeAssist, nil, &geminicli.OAuthConfig{})

	// Keep Resource Manager discovery unavailable so the Code Assist response
	// determines whether this fixture returns its project or a missing-project error.
	svc.Options.FetchProject = func(context.Context, string, string) (string, error) {
		return "", errors.New("fixture resource manager unavailable")
	}
	svc.Start()
	defer stopGeminiAuthorization(t, svc)

	provider := &providercore.Record{
		Platform: capability.PlatformGemini,
		Type:     capability.ProviderTypeOAuth,
		Credentials: map[string]any{
			"refresh_token": "rt",
			"oauth_type":    "code_assist",
			// Omitting project_id triggers project discovery.
		},
	}

	info, err := svc.RefreshProviderToken(context.Background(), provider)
	if err != nil {
		t.Fatalf("RefreshProviderToken 返回错误: %v", err)
	}
	if info.ProjectID != "auto-project-123" {
		t.Fatalf("ProjectID 应为自动检测值: got=%q", info.ProjectID)
	}
	if info.TierID != providercore.GeminiTierGCPStandard {
		t.Fatalf("TierID 不匹配: got=%q", info.TierID)
	}
}

// TestGeminiOAuthService_RefreshProviderToken_CodeAssist_NoProjectID_FailsEmpty
// returns a current tier without project while Resource Manager fails. It
// checks provider refresh reports missing project instead of accepting an
// otherwise exchanged token with no required Code Assist project.
func TestGeminiOAuthService_RefreshProviderToken_CodeAssist_NoProjectID_FailsEmpty(t *testing.T) {
	t.Parallel()

	client := &mockGeminiOAuthClient{
		refreshTokenFunc: func(ctx context.Context, oauthType, refreshToken, proxyURL string) (*geminicli.TokenResponse, error) {
			return &geminicli.TokenResponse{
				AccessToken: "at",
				ExpiresIn:   3600,
			}, nil
		},
	}

	// A current tier with no project exercises discovery for an already
	// onboarded user.
	// This reaches the failing resource-manager lookup without waiting for
	// onboarding retries.
	codeAssist := &mockGeminiCodeAssistClient{
		loadCodeAssistFunc: func(ctx context.Context, accessToken, proxyURL string, req *geminicli.LoadCodeAssistRequest) (*geminicli.LoadCodeAssistResponse, error) {
			return &geminicli.LoadCodeAssistResponse{
				CurrentTier: &geminicli.TierInfo{ID: "STANDARD"},
			}, nil
		},
	}

	svc := newGeminiAuthorizationForTest(&mockGeminiProxyRepo{}, client, codeAssist, nil, &geminicli.OAuthConfig{})

	// Keep Resource Manager discovery unavailable so the Code Assist response
	// determines whether this fixture returns its project or a missing-project error.
	svc.Options.FetchProject = func(context.Context, string, string) (string, error) {
		return "", errors.New("fixture resource manager unavailable")
	}
	svc.Start()
	defer stopGeminiAuthorization(t, svc)

	provider := &providercore.Record{
		Platform: capability.PlatformGemini,
		Type:     capability.ProviderTypeOAuth,
		Credentials: map[string]any{
			"refresh_token": "rt",
			"oauth_type":    "code_assist",
		},
	}

	_, err := svc.RefreshProviderToken(context.Background(), provider)
	if err == nil {
		t.Fatal("应返回错误（无法检测 project_id）")
	}
	if !strings.Contains(err.Error(), "project_id") {
		t.Fatalf("错误信息应包含 project_id: got=%q", err.Error())
	}
}

// TestGeminiOAuthService_RefreshProviderToken_GoogleOne_FreshCache supplies a
// recently timestamped saved Drive tier and project. It checks Google One
// refresh retains the canonical Pro tier without requiring a configured Drive
// query client.
func TestGeminiOAuthService_RefreshProviderToken_GoogleOne_FreshCache(t *testing.T) {
	t.Parallel()

	client := &mockGeminiOAuthClient{
		refreshTokenFunc: func(ctx context.Context, oauthType, refreshToken, proxyURL string) (*geminicli.TokenResponse, error) {
			return &geminicli.TokenResponse{
				AccessToken: "at",
				ExpiresIn:   3600,
			}, nil
		},
	}

	svc := newGeminiAuthorizationForTest(&mockGeminiProxyRepo{}, client, nil, nil, &geminicli.OAuthConfig{})
	svc.Start()
	defer stopGeminiAuthorization(t, svc)

	provider := &providercore.Record{
		Platform: capability.PlatformGemini,
		Type:     capability.ProviderTypeOAuth,
		Credentials: map[string]any{
			"refresh_token": "rt",
			"oauth_type":    "google_one",
			"project_id":    "proj",
			"tier_id":       "google_ai_pro",
		},
		Extra: map[string]any{
			// The tier cache is within its 24-hour freshness window.
			"drive_tier_updated_at": time.Now().Add(-1 * time.Hour).Format(time.RFC3339),
		},
	}

	info, err := svc.RefreshProviderToken(context.Background(), provider)
	if err != nil {
		t.Fatalf("RefreshProviderToken 返回错误: %v", err)
	}
	if info.TierID != providercore.GeminiTierGoogleAIPro {
		t.Fatalf("TierID 应使用缓存值: got=%q want=%q", info.TierID, providercore.GeminiTierGoogleAIPro)
	}
}

// TestGeminiOAuthService_RefreshProviderToken_GoogleOne_NoTierID_DefaultsFree
// omits saved tier and supplies an unavailable Drive fixture. It checks
// refresh still succeeds with the canonical Google One Free tier after
// metadata discovery fails.
func TestGeminiOAuthService_RefreshProviderToken_GoogleOne_NoTierID_DefaultsFree(t *testing.T) {
	t.Parallel()

	client := &mockGeminiOAuthClient{
		refreshTokenFunc: func(ctx context.Context, oauthType, refreshToken, proxyURL string) (*geminicli.TokenResponse, error) {
			return &geminicli.TokenResponse{
				AccessToken: "at",
				ExpiresIn:   3600,
			}, nil
		},
	}

	svc := newGeminiAuthorizationForTest(&mockGeminiProxyRepo{}, client, nil, &mockDriveClient{}, &geminicli.OAuthConfig{})
	svc.Start()
	defer stopGeminiAuthorization(t, svc)

	provider := &providercore.Record{
		Platform: capability.PlatformGemini,
		Type:     capability.ProviderTypeOAuth,
		Credentials: map[string]any{
			"refresh_token": "rt",
			"oauth_type":    "google_one",
			"project_id":    "proj",
		},
	}

	info, err := svc.RefreshProviderToken(context.Background(), provider)
	if err != nil {
		t.Fatalf("RefreshProviderToken 返回错误: %v", err)
	}
	// With no stored tier and an unavailable Drive lookup, use the free tier.
	if info.TierID != providercore.GeminiTierGoogleOneFree {
		t.Fatalf("TierID 应为默认 free: got=%q", info.TierID)
	}
}

// TestGeminiOAuthService_RefreshProviderToken_UnauthorizedClient_Fallback
// rejects Code Assist client input with a custom AI Studio pair configured. It
// checks the alternate exchange produces recovered access data for the
// provider result.
func TestGeminiOAuthService_RefreshProviderToken_UnauthorizedClient_Fallback(t *testing.T) {
	t.Parallel()

	callCount := 0
	client := &mockGeminiOAuthClient{
		refreshTokenFunc: func(ctx context.Context, oauthType, refreshToken, proxyURL string) (*geminicli.TokenResponse, error) {
			callCount++
			if oauthType == "code_assist" {
				return nil, fmt.Errorf("unauthorized_client: client mismatch")
			}
			return &geminicli.TokenResponse{
				AccessToken: "recovered",
				ExpiresIn:   3600,
			}, nil
		},
	}

	// A custom OAuth client enables recovery through AI Studio.
	cfg := &geminicli.OAuthConfig{
		ClientID:     "custom-id",
		ClientSecret: "custom-secret",
	}

	svc := newGeminiAuthorizationForTest(&mockGeminiProxyRepo{}, client, nil, nil, cfg)
	svc.Start()
	defer stopGeminiAuthorization(t, svc)

	provider := &providercore.Record{
		Platform: capability.PlatformGemini,
		Type:     capability.ProviderTypeOAuth,
		Credentials: map[string]any{
			"refresh_token": "rt",
			"oauth_type":    "code_assist",
			"project_id":    "proj",
			"tier_id":       "gcp_standard",
		},
	}

	info, err := svc.RefreshProviderToken(context.Background(), provider)
	if err != nil {
		t.Fatalf("RefreshProviderToken 应在 fallback 后成功: %v", err)
	}
	if info.AccessToken != "recovered" {
		t.Fatalf("AccessToken 不匹配: got=%q", info.AccessToken)
	}
}

// TestGeminiOAuthService_RefreshProviderToken_UnauthorizedClient_NoFallback
// rejects client input without a custom alternate pair. It checks the returned
// error includes the OAuth client mismatch explanation after the configured
// refresh rejection.
func TestGeminiOAuthService_RefreshProviderToken_UnauthorizedClient_NoFallback(t *testing.T) {
	t.Parallel()

	client := &mockGeminiOAuthClient{
		refreshTokenFunc: func(ctx context.Context, oauthType, refreshToken, proxyURL string) (*geminicli.TokenResponse, error) {
			return nil, fmt.Errorf("unauthorized_client: client mismatch")
		},
	}

	// Without a custom OAuth client, the alternate exchange is unavailable.
	svc := newGeminiAuthorizationForTest(&mockGeminiProxyRepo{}, client, nil, nil, &geminicli.OAuthConfig{})
	svc.Start()
	defer stopGeminiAuthorization(t, svc)

	provider := &providercore.Record{
		Platform: capability.PlatformGemini,
		Type:     capability.ProviderTypeOAuth,
		Credentials: map[string]any{
			"refresh_token": "rt",
			"oauth_type":    "code_assist",
			"project_id":    "proj",
		},
	}

	_, err := svc.RefreshProviderToken(context.Background(), provider)
	if err == nil {
		t.Fatal("应返回错误（无 fallback）")
	}
	if !strings.Contains(err.Error(), "OAuth client mismatch") {
		t.Fatalf("错误应包含 OAuth client mismatch: got=%q", err.Error())
	}
}

// TestGeminiOAuthService_ExchangeCode_SessionNotFound supplies code/state for
// a missing session. It checks the lookup diagnostic before any configured
// token client or stored OAuth proof can be used for exchange.
func TestGeminiOAuthService_ExchangeCode_SessionNotFound(t *testing.T) {
	t.Parallel()

	svc := newGeminiAuthorizationForTest(nil, nil, nil, nil, &geminicli.OAuthConfig{})
	svc.Start()
	defer stopGeminiAuthorization(t, svc)

	_, err := svc.ExchangeCode(context.Background(), &providercore.GeminiExchangeCodeInput{
		SessionID: "nonexistent",
		State:     "some-state",
		Code:      "some-code",
	})
	if err == nil {
		t.Fatal("应返回错误（session 不存在）")
	}
	if !strings.Contains(err.Error(), "session not found") {
		t.Fatalf("错误信息不匹配: got=%q", err.Error())
	}
}

// TestGeminiOAuthService_ExchangeCode_InvalidState stores a fresh session then
// supplies a different state. It checks state-validation rejection,
// distinguishing the bad proof from a session rejected because its timestamp
// is absent or expired.
func TestGeminiOAuthService_ExchangeCode_InvalidState(t *testing.T) {
	t.Parallel()

	svc := newGeminiAuthorizationForTest(nil, nil, nil, nil, &geminicli.OAuthConfig{})
	svc.Start()
	defer stopGeminiAuthorization(t, svc)

	// Give the manual session a live creation timestamp so state mismatch reaches
	// proof validation rather than failing first as an absent or expired session.
	svc.Store.Set("test-session", &providercore.GeminiOAuthSession{
		State:        "correct-state",
		CodeVerifier: "verifier",
		OAuthType:    "ai_studio",
		CreatedAt:    time.Now(),
	})

	_, err := svc.ExchangeCode(context.Background(), &providercore.GeminiExchangeCodeInput{
		SessionID: "test-session",
		State:     "wrong-state",
		Code:      "code",
	})
	if err == nil {
		t.Fatal("应返回错误（state 不匹配）")
	}
	if !strings.Contains(err.Error(), "invalid state") {
		t.Fatalf("错误信息不匹配: got=%q", err.Error())
	}
}

// TestGeminiOAuthService_ExchangeCode_EmptyState stores a fresh session then
// supplies empty state with code. It checks exchange returns an error for
// this incomplete proof. The returned token result is not inspected.
func TestGeminiOAuthService_ExchangeCode_EmptyState(t *testing.T) {
	t.Parallel()

	svc := newGeminiAuthorizationForTest(nil, nil, nil, nil, &geminicli.OAuthConfig{})
	svc.Start()
	defer stopGeminiAuthorization(t, svc)

	svc.Store.Set("test-session", &providercore.GeminiOAuthSession{
		State:        "correct-state",
		CodeVerifier: "verifier",
		CreatedAt:    time.Now(),
	})

	_, err := svc.ExchangeCode(context.Background(), &providercore.GeminiExchangeCodeInput{
		SessionID: "test-session",
		State:     "",
		Code:      "code",
	})
	if err == nil {
		t.Fatal("应返回错误（空 state）")
	}
}

// ExchangeCode delegates variant, code, verifier, redirect and proxy to its
// callback. A missing callback panics at this operation. Configured responses
// and errors are returned directly for the core to convert and validate.
func (m *mockGeminiOAuthClient) ExchangeCode(ctx context.Context, oauthType, code, codeVerifier, redirectURI, proxyURL string) (*geminicli.TokenResponse, error) {
	if m.exchangeCodeFunc != nil {
		return m.exchangeCodeFunc(ctx, oauthType, code, codeVerifier, redirectURI, proxyURL)
	}
	panic("ExchangeCode not implemented")
}

// RefreshToken delegates variant, refresh input and proxy to the fixture
// callback. A missing callback panics. Cases use callback state for repeated
// transient failures or return an explicit terminal rejection through this
// unchanged result path.
func (m *mockGeminiOAuthClient) RefreshToken(ctx context.Context, oauthType, refreshToken, proxyURL string) (*geminicli.TokenResponse, error) {
	if m.refreshTokenFunc != nil {
		return m.refreshTokenFunc(ctx, oauthType, refreshToken, proxyURL)
	}
	panic("RefreshToken not implemented")
}

// LoadCodeAssist delegates access, proxy and request pointer to configured
// discovery. A missing callback panics instead of returning an empty project
// observation. The callback controls result ownership and whether discovery
// metadata is available.
func (m *mockGeminiCodeAssistClient) LoadCodeAssist(ctx context.Context, accessToken, proxyURL string, req *geminicli.LoadCodeAssistRequest) (*geminicli.LoadCodeAssistResponse, error) {
	if m.loadCodeAssistFunc != nil {
		return m.loadCodeAssistFunc(ctx, accessToken, proxyURL, req)
	}
	panic("LoadCodeAssist not implemented")
}

// OnboardUser delegates the onboarding request to its configured callback. A
// missing callback panics when discovery reaches unconfigured onboarding. The
// result and error are passed directly to the core's retry and project
// handling.
func (m *mockGeminiCodeAssistClient) OnboardUser(ctx context.Context, accessToken, proxyURL string, req *geminicli.OnboardUserRequest) (*geminicli.OnboardUserResponse, error) {
	if m.onboardUserFunc != nil {
		return m.onboardUserFunc(ctx, accessToken, proxyURL, req)
	}
	panic("OnboardUser not implemented")
}

// Create panics on proxy persistence requested through this Gemini lookup
// fixture. The cases configure selected proxy reads for authorization and
// refresh; a creation call identifies a write dependency the fixture did not
// supply.
func (m *mockGeminiProxyRepo) Create(ctx context.Context, proxy *egress.Proxy) error {
	panic("not impl")
}

// GetByID performs configured exact proxy lookup or returns the fixture
// not-found error. The callback receives selected ID/context unchanged. The
// resolver consumes its borrowed proxy pointer to obtain the URL used by
// provider refresh.
func (m *mockGeminiProxyRepo) GetByID(ctx context.Context, id int64) (*egress.Proxy, error) {
	if m.getByIDFunc != nil {
		return m.getByIDFunc(ctx, id)
	}
	return nil, fmt.Errorf("proxy not found")
}

// ListByIDs panics on batch proxy resolution during these Gemini cases.
// Provider refresh resolves one optional saved ProxyID through GetByID.
// Callers need another fixture when their workflow requires a multi-ID lookup
// result.
func (m *mockGeminiProxyRepo) ListByIDs(ctx context.Context, ids []int64) ([]egress.Proxy, error) {
	panic("not impl")
}

// Update panics when Gemini authorization attempts to modify proxy
// configuration. The supplied lookup pointer represents the selected egress
// row. This repository substitute provides no configuration persistence
// behavior for proxy edit callers.
func (m *mockGeminiProxyRepo) Update(ctx context.Context, proxy *egress.Proxy) error {
	panic("not impl")
}

// Delete panics on attempted removal of a configured proxy row. These Gemini
// cases consume egress selection through lookup and do not configure proxy
// ownership deletion, so the repository method exposes an unexpected mutation.
func (m *mockGeminiProxyRepo) Delete(ctx context.Context, id int64) error { panic("not impl") }

// List panics when a Gemini case requests paginated proxy inventory.
// Authorization resolves a saved selection directly rather than selecting from
// inventory. This fixture has no page result or pagination metadata to return.
func (m *mockGeminiProxyRepo) List(ctx context.Context, params pagination.PaginationParams) ([]egress.Proxy, *pagination.PaginationResult, error) {
	panic("not impl")
}

// ListWithFilters panics on protocol/status/search-based proxy inventory
// queries. The fixture only configures the selected ID lookup used during
// authorization. Filter callers require separately supplied row and pagination
// observations for their workflow.
func (m *mockGeminiProxyRepo) ListWithFilters(ctx context.Context, params pagination.PaginationParams, protocol, status, search string) ([]egress.Proxy, *pagination.PaginationResult, error) {
	panic("not impl")
}

// ListWithFiltersAndProviderCount panics on filtered proxy rows with
// attachment counts. No aggregate management data is configured here. Gemini
// refresh consumes an exact optional proxy ID instead of this
// inventory-enrichment operation.
func (m *mockGeminiProxyRepo) ListWithFiltersAndProviderCount(ctx context.Context, params pagination.PaginationParams, protocol, status, search string) ([]egress.ProxyWithProviderCount, *pagination.PaginationResult, error) {
	panic("not impl")
}

// ListActive panics on enabled-proxy inventory enumeration through the OAuth
// fixture. Its selected proxy is returned by GetByID when configured.
// Active-pool selection needs another repository substitute with the
// appropriate inventory data.
func (m *mockGeminiProxyRepo) ListActive(ctx context.Context) ([]egress.Proxy, error) {
	panic("not impl")
}

// ListActiveWithProviderCount panics on active-proxy inventory and membership
// counting. The fixture carries configured exact lookup data rather than
// management totals. Calling this method exposes an unsupported dependency in
// a Gemini authorization case.
func (m *mockGeminiProxyRepo) ListActiveWithProviderCount(ctx context.Context) ([]egress.ProxyWithProviderCount, error) {
	panic("not impl")
}

// ExistsByHostPortAuth panics on host/port/credential deduplication through
// this fixture. Gemini sessions refer to a proxy by its saved ID.
// Identity-existence queries need a repository configured for proxy creation
// or import checks.
func (m *mockGeminiProxyRepo) ExistsByHostPortAuth(ctx context.Context, host string, port int, username, password string) (bool, error) {
	panic("not impl")
}

// CountProvidersByProxyID panics on provider membership counting for a
// selected proxy. This fixture returns the selected egress row without
// attached-provider statistics. An aggregate query is unsupported by the
// authorization/refresh observations configured in these cases.
func (m *mockGeminiProxyRepo) CountProvidersByProxyID(ctx context.Context, proxyID int64) (int64, error) {
	panic("not impl")
}

// ListProviderSummariesByProxyID panics on related-provider summary
// enumeration. The fixture supplies egress details for one selected ID, with
// no provider collection configured for callers performing proxy-management or
// attachment diagnostics.
func (m *mockGeminiProxyRepo) ListProviderSummariesByProxyID(ctx context.Context, proxyID int64) ([]egress.ProxyProviderSummary, error) {
	panic("not impl")
}

// SweepExpiredProxies panics on proxy-expiry cleanup requested through the
// Gemini fixture. The cases supply lookup values for current authorization
// selection. Expiry removal and affected-row counting require a separately
// configured repository implementation.
func (m *mockGeminiProxyRepo) SweepExpiredProxies(ctx context.Context, now time.Time) (int64, error) {
	panic("not impl")
}

// ListAllForFallback panics on alternate-proxy inventory discovery through
// this fixture. Gemini refresh resolves its optional saved selection by exact
// ID. The substitute has no pool of fallback rows available to a selection
// caller.
func (m *mockGeminiProxyRepo) ListAllForFallback(ctx context.Context) ([]egress.Proxy, error) {
	panic("not impl")
}

// CountExpired panics on expired-proxy aggregation through this authorization
// fixture. It returns configured individual lookup data and carries no expiry
// inventory. A statistics caller cannot use this substitute to infer expired
// row counts.
func (m *mockGeminiProxyRepo) CountExpired(ctx context.Context) (int64, error) {
	panic("not impl")
}

// CountExpiringSoon panics on time-based near-expiry proxy counting in these
// cases. The fixture holds exact selected-ID lookup configuration. The
// supplied time has no aggregate consumer because proxy-expiry inventory is
// not configured.
func (m *mockGeminiProxyRepo) CountExpiringSoon(ctx context.Context, now time.Time) (int64, error) {
	panic("not impl")
}

// GetStorageQuota delegates the access/proxy query when a fixture callback is
// present. Otherwise it returns the stable Drive-unavailable error with no
// quota data. Core tier handling decides which stored or default tier remains
// after that failure.
func (m *mockDriveClient) GetStorageQuota(ctx context.Context, accessToken, proxyURL string) (*geminicli.DriveStorageInfo, error) {
	if m.getStorageQuotaFunc != nil {
		return m.getStorageQuotaFunc(ctx, accessToken, proxyURL)
	}
	return nil, fmt.Errorf("drive API not available in test")
}

// assertCredStr checks that a credential key exists and holds the expected
// string. Missing key, nonstring value or mismatched spelling terminates the
// calling test. It marks itself as a helper so diagnostics identify the case
// assertion.
func assertCredStr(t *testing.T, creds map[string]any, key, want string) {
	t.Helper()
	raw, ok := creds[key]
	if !ok {
		t.Fatalf("creds 缺少 key=%q", key)
	}
	got, ok := raw.(string)
	if !ok {
		t.Fatalf("creds[%q] 不是 string: %T", key, raw)
	}
	if got != want {
		t.Fatalf("creds[%q] = %q, want %q", key, got, want)
	}
}

// credKeys returns a newly allocated slice of the map's current key strings.
// Map iteration leaves the order unspecified. Tests use the slice for
// diagnostics, so callers that need a stable display order must sort their
// copy.
func credKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// newGeminiAuthorizationForTest binds fixture clients and a live configuration
// callback. Cfg must be nonnil and remains borrowed for each callback read.
// Proxies may be nil, leaving resolution absent; a configured lookup failure
// returns empty/false. The caller owns startup and shutdown of the constructed
// core.
func newGeminiAuthorizationForTest(proxies *mockGeminiProxyRepo, client providercore.GeminiOAuthClient, discovery providercore.GeminiCodeAssistClient, drive providercore.GeminiDriveClient, cfg *geminicli.OAuthConfig) *providercore.GeminiAuthorization {
	var resolve func(context.Context, int64) (string, bool)
	if proxies != nil {
		resolve = func(ctx context.Context, id int64) (string, bool) {
			proxy, err := proxies.GetByID(ctx, id)
			// Unavailable fixture proxies remain unresolved. Convert the
			// lookup outcome here
			// so core provider refresh observes empty/false rather than a
			// repository-specific error.
			if err != nil || proxy == nil {
				return "", false
			}
			return proxy.URL(), true
		}
	}
	return providercore.NewGeminiAuthorization(client, discovery, drive, GeminiAuthorizationOptions(func() geminicli.OAuthConfig { return *cfg }, resolve))
}

// stopGeminiAuthorization stops the supplied core and reports lifecycle errors
// to the test. It passes Background without a deadline and waits for actual
// admitted completion. The helper flags shutdown failure at the caller rather
// than returning an ignored error.
func stopGeminiAuthorization(t *testing.T, value *providercore.GeminiAuthorization) {
	t.Helper()
	if err := value.StopContext(context.Background()); err != nil {
		t.Fatal(err)
	}
}
