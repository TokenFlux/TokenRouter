//go:build unit

package provider

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	geminicli "github.com/TokenFlux/TokenRouter/internal/upstream/gemini/codeassist"
)

// =====================
// 保留原有测试
// =====================

func TestGeminiOAuthService_GenerateAuthURL_RedirectURIStrategy(t *testing.T) {
	// NOTE: This test sets process env; it must not run in parallel.
	// The built-in Gemini CLI client secret is not embedded in this repository.
	// Tests set a dummy secret via env to simulate operator-provided configuration.
	t.Setenv(geminicli.GeminiCLIOAuthClientSecretEnv, "test-built-in-secret")

	type testCase struct {
		name          string
		cfg           *geminicli.OAuthConfig
		oauthType     string
		projectID     string
		wantClientID  string
		wantRedirect  string
		wantScope     string
		wantProjectID string
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
			cfg: &geminicli.OAuthConfig{ClientID: "custom-client-id",
				ClientSecret: "custom-client-secret"},

			oauthType:     "google_one",
			wantClientID:  geminicli.GeminiCLIOAuthClientID,
			wantRedirect:  geminicli.GeminiCLIRedirectURI,
			wantScope:     geminicli.DefaultCodeAssistScopes,
			wantProjectID: "",
		},
		{
			name: "code_assist always forces built-in client even when custom client configured",
			cfg: &geminicli.OAuthConfig{ClientID: "custom-client-id",
				ClientSecret: "custom-client-secret"},

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
		tt := tt
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

// =====================
// 新增测试：validateTierID
// =====================

// =====================
// 新增测试：canonicalGeminiTierID
// =====================

// =====================
// 新增测试：canonicalGeminiTierIDForOAuthType
// =====================

// =====================
// 新增测试：extractTierIDFromAllowedTiers
// =====================

// =====================
// 新增测试：inferGoogleOneTier
// =====================

// =====================
// 新增测试：isNonRetryableGeminiOAuthError
// =====================

// =====================
// 新增测试：BuildAccountCredentials
// =====================

func TestGeminiOAuthService_BuildAccountCredentials(t *testing.T) {
	t.Parallel()

	svc := newGeminiAuthorizationForTest(nil, nil, nil, nil, &geminicli.OAuthConfig{})
	svc.Start()
	defer stopGeminiAuthorization(t, svc)

	t.Run("完整字段", func(t *testing.T) {
		t.Parallel()
		tokenInfo := &accountcore.GeminiTokenInfo{
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

		creds := svc.BuildAccountCredentials(tokenInfo)

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
		tokenInfo := &accountcore.GeminiTokenInfo{
			AccessToken: "token-only",
			ExpiresAt:   1700000000,
		}

		creds := svc.BuildAccountCredentials(tokenInfo)

		assertCredStr(t, creds, "access_token", "token-only")
		assertCredStr(t, creds, "expires_at", "1700000000")

		// 可选字段不应存在
		for _, key := range []string{"refresh_token", "token_type", "scope", "project_id", "tier_id", "oauth_type"} {
			if _, ok := creds[key]; ok {
				t.Fatalf("不应包含空字段 %q", key)
			}
		}
	})

	t.Run("无效 tier_id 被静默跳过", func(t *testing.T) {
		t.Parallel()
		tokenInfo := &accountcore.GeminiTokenInfo{
			AccessToken: "token",
			ExpiresAt:   1700000000,
			TierID:      "tier with spaces",
		}

		creds := svc.BuildAccountCredentials(tokenInfo)

		if _, ok := creds["tier_id"]; ok {
			t.Fatal("无效 tier_id 不应被存入 creds")
		}
	})

	t.Run("超长 tier_id 被静默跳过", func(t *testing.T) {
		t.Parallel()
		tokenInfo := &accountcore.GeminiTokenInfo{
			AccessToken: "token",
			ExpiresAt:   1700000000,
			TierID:      strings.Repeat("x", 65),
		}

		creds := svc.BuildAccountCredentials(tokenInfo)

		if _, ok := creds["tier_id"]; ok {
			t.Fatal("超长 tier_id 不应被存入 creds")
		}
	})

	t.Run("无 extra 字段", func(t *testing.T) {
		t.Parallel()
		tokenInfo := &accountcore.GeminiTokenInfo{
			AccessToken:  "token",
			ExpiresAt:    1700000000,
			RefreshToken: "rt",
		}

		creds := svc.BuildAccountCredentials(tokenInfo)

		// 仅包含基础字段
		if len(creds) != 3 { // access_token, expires_at, refresh_token
			t.Fatalf("creds 字段数量不匹配: got=%d want=3, keys=%v", len(creds), credKeys(creds))
		}
	})
}

// =====================
// 新增测试：GetOAuthConfig
// =====================

func TestGeminiOAuthService_GetOAuthConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		cfg         *geminicli.OAuthConfig
		wantEnabled bool
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
			cfg: &geminicli.OAuthConfig{ClientID: geminicli.GeminiCLIOAuthClientID,
				ClientSecret: "some-secret"},

			wantEnabled: false,
		},
		{
			name: "自定义 OAuth 客户端（非内置 ID）",
			cfg: &geminicli.OAuthConfig{ClientID: "my-custom-client-id",
				ClientSecret: "my-custom-client-secret"},

			wantEnabled: true,
		},
		{
			name: "带空白的自定义客户端",
			cfg: &geminicli.OAuthConfig{ClientID: "  my-custom-client-id  ",
				ClientSecret: "  my-custom-client-secret  "},

			wantEnabled: true,
		},
		{
			name: "纯空白字符串不算配置",
			cfg: &geminicli.OAuthConfig{ClientID: "   ",
				ClientSecret: "   "},

			wantEnabled: false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			svc := newGeminiAuthorizationForTest(nil, nil, nil, nil, tt.cfg)
			svc.Start()
			defer stopGeminiAuthorization(t, svc)

			result := svc.GetOAuthConfig()
			if result.AIStudioOAuthEnabled != tt.wantEnabled {
				t.Fatalf("AIStudioOAuthEnabled = %v, want %v", result.AIStudioOAuthEnabled, tt.wantEnabled)
			}
			// RequiredRedirectURIs 始终包含 AI Studio redirect URI
			if len(result.RequiredRedirectURIs) != 1 || result.RequiredRedirectURIs[0] != geminicli.AIStudioOAuthRedirectURI {
				t.Fatalf("RequiredRedirectURIs 不匹配: got=%v", result.RequiredRedirectURIs)
			}
		})
	}
}

// =====================
// 新增测试：GeminiOAuthService.Stop
// =====================

func TestGeminiOAuthService_Stop_NoPanic(t *testing.T) {
	t.Parallel()

	svc := newGeminiAuthorizationForTest(nil, nil, nil, nil, &geminicli.OAuthConfig{})
	svc.Start()

	// 调用 Stop 不应 panic
	stopGeminiAuthorization(t, svc)
	// 多次调用也不应 panic
	stopGeminiAuthorization(t, svc)
}

// =====================
// mock: GeminiOAuthClient
// =====================

type mockGeminiOAuthClient struct {
	exchangeCodeFunc func(ctx context.Context, oauthType, code, codeVerifier, redirectURI, proxyURL string) (*geminicli.TokenResponse, error)
	refreshTokenFunc func(ctx context.Context, oauthType, refreshToken, proxyURL string) (*geminicli.TokenResponse, error)
}

func (m *mockGeminiOAuthClient) ExchangeCode(ctx context.Context, oauthType, code, codeVerifier, redirectURI, proxyURL string) (*geminicli.TokenResponse, error) {
	if m.exchangeCodeFunc != nil {
		return m.exchangeCodeFunc(ctx, oauthType, code, codeVerifier, redirectURI, proxyURL)
	}
	panic("ExchangeCode not implemented")
}

func (m *mockGeminiOAuthClient) RefreshToken(ctx context.Context, oauthType, refreshToken, proxyURL string) (*geminicli.TokenResponse, error) {
	if m.refreshTokenFunc != nil {
		return m.refreshTokenFunc(ctx, oauthType, refreshToken, proxyURL)
	}
	panic("RefreshToken not implemented")
}

// =====================
// mock: GeminiCliCodeAssistClient
// =====================

type mockGeminiCodeAssistClient struct {
	loadCodeAssistFunc func(ctx context.Context, accessToken, proxyURL string, req *geminicli.LoadCodeAssistRequest) (*geminicli.LoadCodeAssistResponse, error)
	onboardUserFunc    func(ctx context.Context, accessToken, proxyURL string, req *geminicli.OnboardUserRequest) (*geminicli.OnboardUserResponse, error)
}

func (m *mockGeminiCodeAssistClient) LoadCodeAssist(ctx context.Context, accessToken, proxyURL string, req *geminicli.LoadCodeAssistRequest) (*geminicli.LoadCodeAssistResponse, error) {
	if m.loadCodeAssistFunc != nil {
		return m.loadCodeAssistFunc(ctx, accessToken, proxyURL, req)
	}
	panic("LoadCodeAssist not implemented")
}

func (m *mockGeminiCodeAssistClient) OnboardUser(ctx context.Context, accessToken, proxyURL string, req *geminicli.OnboardUserRequest) (*geminicli.OnboardUserResponse, error) {
	if m.onboardUserFunc != nil {
		return m.onboardUserFunc(ctx, accessToken, proxyURL, req)
	}
	panic("OnboardUser not implemented")
}

// =====================
// mock: ProxyRepository (最小实现)
// =====================

type mockGeminiProxyRepo struct {
	getByIDFunc func(ctx context.Context, id int64) (*egress.Proxy, error)
}

func (m *mockGeminiProxyRepo) Create(ctx context.Context, proxy *egress.Proxy) error {
	panic("not impl")
}
func (m *mockGeminiProxyRepo) GetByID(ctx context.Context, id int64) (*egress.Proxy, error) {
	if m.getByIDFunc != nil {
		return m.getByIDFunc(ctx, id)
	}
	return nil, fmt.Errorf("proxy not found")
}
func (m *mockGeminiProxyRepo) ListByIDs(ctx context.Context, ids []int64) ([]egress.Proxy, error) {
	panic("not impl")
}
func (m *mockGeminiProxyRepo) Update(ctx context.Context, proxy *egress.Proxy) error {
	panic("not impl")
}
func (m *mockGeminiProxyRepo) Delete(ctx context.Context, id int64) error { panic("not impl") }
func (m *mockGeminiProxyRepo) List(ctx context.Context, params pagination.PaginationParams) ([]egress.Proxy, *pagination.PaginationResult, error) {
	panic("not impl")
}
func (m *mockGeminiProxyRepo) ListWithFilters(ctx context.Context, params pagination.PaginationParams, protocol, status, search string) ([]egress.Proxy, *pagination.PaginationResult, error) {
	panic("not impl")
}
func (m *mockGeminiProxyRepo) ListWithFiltersAndAccountCount(ctx context.Context, params pagination.PaginationParams, protocol, status, search string) ([]egress.ProxyWithAccountCount, *pagination.PaginationResult, error) {
	panic("not impl")
}
func (m *mockGeminiProxyRepo) ListActive(ctx context.Context) ([]egress.Proxy, error) {
	panic("not impl")
}
func (m *mockGeminiProxyRepo) ListActiveWithAccountCount(ctx context.Context) ([]egress.ProxyWithAccountCount, error) {
	panic("not impl")
}
func (m *mockGeminiProxyRepo) ExistsByHostPortAuth(ctx context.Context, host string, port int, username, password string) (bool, error) {
	panic("not impl")
}
func (m *mockGeminiProxyRepo) CountAccountsByProxyID(ctx context.Context, proxyID int64) (int64, error) {
	panic("not impl")
}
func (m *mockGeminiProxyRepo) ListAccountSummariesByProxyID(ctx context.Context, proxyID int64) ([]egress.ProxyAccountSummary, error) {
	panic("not impl")
}
func (m *mockGeminiProxyRepo) SweepExpiredProxies(ctx context.Context, now time.Time) (int64, error) {
	panic("not impl")
}
func (m *mockGeminiProxyRepo) ListAllForFallback(ctx context.Context) ([]egress.Proxy, error) {
	panic("not impl")
}
func (m *mockGeminiProxyRepo) CountExpired(ctx context.Context) (int64, error) {
	panic("not impl")
}
func (m *mockGeminiProxyRepo) CountExpiringSoon(ctx context.Context, now time.Time) (int64, error) {
	panic("not impl")
}

// mockDriveClient implements geminicli.DriveClient for tests.
type mockDriveClient struct {
	getStorageQuotaFunc func(ctx context.Context, accessToken, proxyURL string) (*geminicli.DriveStorageInfo, error)
}

func (m *mockDriveClient) GetStorageQuota(ctx context.Context, accessToken, proxyURL string) (*geminicli.DriveStorageInfo, error) {
	if m.getStorageQuotaFunc != nil {
		return m.getStorageQuotaFunc(ctx, accessToken, proxyURL)
	}
	return nil, fmt.Errorf("drive API not available in test")
}

// =====================
// 新增测试：GeminiOAuthService.RefreshToken（含重试逻辑）
// =====================

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

func TestGeminiOAuthService_RefreshToken_RetryableError(t *testing.T) {
	t.Parallel()

	callCount := 0
	client := &mockGeminiOAuthClient{
		refreshTokenFunc: func(ctx context.Context, oauthType, refreshToken, proxyURL string) (*geminicli.TokenResponse, error) {
			callCount++
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

// =====================
// 新增测试：GeminiOAuthService.RefreshAccountToken
// =====================

func TestGeminiOAuthService_RefreshAccountToken_NotGeminiOAuth(t *testing.T) {
	t.Parallel()

	svc := newGeminiAuthorizationForTest(nil, nil, nil, nil, &geminicli.OAuthConfig{})
	svc.Start()
	defer stopGeminiAuthorization(t, svc)

	account := &accountcore.Record{
		Platform: capability.PlatformAnthropic,
		Type:     capability.AccountTypeOAuth,
	}

	_, err := svc.RefreshAccountToken(context.Background(), account)
	if err == nil {
		t.Fatal("应返回错误（非 Gemini OAuth 账号）")
	}
	if !strings.Contains(err.Error(), "not a Gemini OAuth account") {
		t.Fatalf("错误信息不匹配: got=%q", err.Error())
	}
}

func TestGeminiOAuthService_RefreshAccountToken_NoRefreshToken(t *testing.T) {
	t.Parallel()

	svc := newGeminiAuthorizationForTest(nil, nil, nil, nil, &geminicli.OAuthConfig{})
	svc.Start()
	defer stopGeminiAuthorization(t, svc)

	account := &accountcore.Record{
		Platform: capability.PlatformGemini,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token": "at",
			"oauth_type":   "code_assist",
		},
	}

	_, err := svc.RefreshAccountToken(context.Background(), account)
	if err == nil {
		t.Fatal("应返回错误（无 refresh_token）")
	}
	if !strings.Contains(err.Error(), "no refresh token") {
		t.Fatalf("错误信息不匹配: got=%q", err.Error())
	}
}

func TestGeminiOAuthService_RefreshAccountToken_AIStudio(t *testing.T) {
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

	account := &accountcore.Record{
		Platform: capability.PlatformGemini,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":  "old-at",
			"refresh_token": "old-rt",
			"oauth_type":    "ai_studio",
			"tier_id":       "aistudio_free",
		},
	}

	info, err := svc.RefreshAccountToken(context.Background(), account)
	if err != nil {
		t.Fatalf("RefreshAccountToken 返回错误: %v", err)
	}
	if info.AccessToken != "refreshed-at" {
		t.Fatalf("AccessToken 不匹配: got=%q", info.AccessToken)
	}
	if info.OAuthType != "ai_studio" {
		t.Fatalf("OAuthType 不匹配: got=%q", info.OAuthType)
	}
}

func TestGeminiOAuthService_RefreshAccountToken_CodeAssist_WithProjectID(t *testing.T) {
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

	account := &accountcore.Record{
		Platform: capability.PlatformGemini,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"access_token":  "old-at",
			"refresh_token": "old-rt",
			"oauth_type":    "code_assist",
			"project_id":    "my-project",
			"tier_id":       "gcp_standard",
		},
	}

	info, err := svc.RefreshAccountToken(context.Background(), account)
	if err != nil {
		t.Fatalf("RefreshAccountToken 返回错误: %v", err)
	}
	if info.ProjectID != "my-project" {
		t.Fatalf("ProjectID 应保留: got=%q", info.ProjectID)
	}
	if info.TierID != accountcore.GeminiTierGCPStandard {
		t.Fatalf("TierID 不匹配: got=%q want=%q", info.TierID, accountcore.GeminiTierGCPStandard)
	}
	if info.OAuthType != "code_assist" {
		t.Fatalf("OAuthType 不匹配: got=%q", info.OAuthType)
	}
}

func TestGeminiOAuthService_RefreshAccountToken_DefaultOAuthType(t *testing.T) {
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

	// 无 oauth_type 凭据的旧账号
	account := &accountcore.Record{
		Platform: capability.PlatformGemini,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"refresh_token": "old-rt",
			"project_id":    "proj",
			"tier_id":       "STANDARD",
		},
	}

	info, err := svc.RefreshAccountToken(context.Background(), account)
	if err != nil {
		t.Fatalf("RefreshAccountToken 返回错误: %v", err)
	}
	if info.OAuthType != "code_assist" {
		t.Fatalf("OAuthType 应默认为 code_assist: got=%q", info.OAuthType)
	}
}

func TestGeminiOAuthService_RefreshAccountToken_WithProxy(t *testing.T) {
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
	account := &accountcore.Record{
		Platform: capability.PlatformGemini,
		Type:     capability.AccountTypeOAuth,
		ProxyID:  &proxyID,
		Credentials: map[string]any{
			"refresh_token": "rt",
			"oauth_type":    "code_assist",
			"project_id":    "proj",
		},
	}

	_, err := svc.RefreshAccountToken(context.Background(), account)
	if err != nil {
		t.Fatalf("RefreshAccountToken 返回错误: %v", err)
	}
}

func TestGeminiOAuthService_RefreshAccountToken_CodeAssist_NoProjectID_AutoDetect(t *testing.T) {
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

	svc := newGeminiAuthorizationForTest(&mockGeminiProxyRepo{}, client, codeAssist, nil, &geminicli.OAuthConfig{
		// 账号用例只验证发现失败的原回退，网络解析由平台本地 HTTP 契约覆盖。
	})

	svc.Options.FetchProject = func(context.Context, string, string) (string, error) {
		return "", errors.New("fixture resource manager unavailable")
	}
	svc.Start()
	defer stopGeminiAuthorization(t, svc)

	account := &accountcore.Record{
		Platform: capability.PlatformGemini,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"refresh_token": "rt",
			"oauth_type":    "code_assist",
			// 无 project_id，触发 fetchProjectID
		},
	}

	info, err := svc.RefreshAccountToken(context.Background(), account)
	if err != nil {
		t.Fatalf("RefreshAccountToken 返回错误: %v", err)
	}
	if info.ProjectID != "auto-project-123" {
		t.Fatalf("ProjectID 应为自动检测值: got=%q", info.ProjectID)
	}
	if info.TierID != accountcore.GeminiTierGCPStandard {
		t.Fatalf("TierID 不匹配: got=%q", info.TierID)
	}
}

func TestGeminiOAuthService_RefreshAccountToken_CodeAssist_NoProjectID_FailsEmpty(t *testing.T) {
	t.Parallel()

	client := &mockGeminiOAuthClient{
		refreshTokenFunc: func(ctx context.Context, oauthType, refreshToken, proxyURL string) (*geminicli.TokenResponse, error) {
			return &geminicli.TokenResponse{
				AccessToken: "at",
				ExpiresIn:   3600,
			}, nil
		},
	}

	// 返回有 currentTier 但无 cloudaicompanionProject 的响应，
	// 使 fetchProjectID 走"已注册用户"路径（尝试 Cloud Resource Manager -> 失败 -> 返回错误），
	// 避免走 onboardUser 路径（5 次重试 x 2 秒 = 10 秒超时）
	codeAssist := &mockGeminiCodeAssistClient{
		loadCodeAssistFunc: func(ctx context.Context, accessToken, proxyURL string, req *geminicli.LoadCodeAssistRequest) (*geminicli.LoadCodeAssistResponse, error) {
			return &geminicli.LoadCodeAssistResponse{
				CurrentTier: &geminicli.TierInfo{ID: "STANDARD"},
				// 无 CloudAICompanionProject
			}, nil
		},
	}

	svc := newGeminiAuthorizationForTest(&mockGeminiProxyRepo{}, client, codeAssist, nil, &geminicli.OAuthConfig{
		// 账号用例只验证发现失败的原回退，网络解析由平台本地 HTTP 契约覆盖。
	})

	svc.Options.FetchProject = func(context.Context, string, string) (string, error) {
		return "", errors.New("fixture resource manager unavailable")
	}
	svc.Start()
	defer stopGeminiAuthorization(t, svc)

	account := &accountcore.Record{
		Platform: capability.PlatformGemini,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"refresh_token": "rt",
			"oauth_type":    "code_assist",
		},
	}

	_, err := svc.RefreshAccountToken(context.Background(), account)
	if err == nil {
		t.Fatal("应返回错误（无法检测 project_id）")
	}
	if !strings.Contains(err.Error(), "project_id") {
		t.Fatalf("错误信息应包含 project_id: got=%q", err.Error())
	}
}

func TestGeminiOAuthService_RefreshAccountToken_GoogleOne_FreshCache(t *testing.T) {
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

	account := &accountcore.Record{
		Platform: capability.PlatformGemini,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"refresh_token": "rt",
			"oauth_type":    "google_one",
			"project_id":    "proj",
			"tier_id":       "google_ai_pro",
		},
		Extra: map[string]any{
			// 缓存刷新时间在 24 小时内
			"drive_tier_updated_at": time.Now().Add(-1 * time.Hour).Format(time.RFC3339),
		},
	}

	info, err := svc.RefreshAccountToken(context.Background(), account)
	if err != nil {
		t.Fatalf("RefreshAccountToken 返回错误: %v", err)
	}
	// 缓存新鲜，应使用已有的 tier_id
	if info.TierID != accountcore.GeminiTierGoogleAIPro {
		t.Fatalf("TierID 应使用缓存值: got=%q want=%q", info.TierID, accountcore.GeminiTierGoogleAIPro)
	}
}

func TestGeminiOAuthService_RefreshAccountToken_GoogleOne_NoTierID_DefaultsFree(t *testing.T) {
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

	account := &accountcore.Record{
		Platform: capability.PlatformGemini,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"refresh_token": "rt",
			"oauth_type":    "google_one",
			"project_id":    "proj",
			// 无 tier_id
		},
	}

	info, err := svc.RefreshAccountToken(context.Background(), account)
	if err != nil {
		t.Fatalf("RefreshAccountToken 返回错误: %v", err)
	}
	// FetchGoogleOneTier 会被调用但 oauthClient（此处 mock）不实现 Drive API，
	// svc.FetchGoogleOneTier 使用真实 DriveClient 会失败，最终回退到默认值。
	// 由于没有 tier_id 且 FetchGoogleOneTier 失败，应默认为 google_one_free
	if info.TierID != accountcore.GeminiTierGoogleOneFree {
		t.Fatalf("TierID 应为默认 free: got=%q", info.TierID)
	}
}

func TestGeminiOAuthService_RefreshAccountToken_UnauthorizedClient_Fallback(t *testing.T) {
	t.Parallel()

	callCount := 0
	client := &mockGeminiOAuthClient{
		refreshTokenFunc: func(ctx context.Context, oauthType, refreshToken, proxyURL string) (*geminicli.TokenResponse, error) {
			callCount++
			if oauthType == "code_assist" {
				return nil, fmt.Errorf("unauthorized_client: client mismatch")
			}
			// ai_studio 路径成功
			return &geminicli.TokenResponse{
				AccessToken: "recovered",
				ExpiresIn:   3600,
			}, nil
		},
	}

	// 启用自定义 OAuth 客户端以触发 fallback 路径
	cfg := &geminicli.OAuthConfig{ClientID: "custom-id",
		ClientSecret: "custom-secret"}

	svc := newGeminiAuthorizationForTest(&mockGeminiProxyRepo{}, client, nil, nil, cfg)
	svc.Start()
	defer stopGeminiAuthorization(t, svc)

	account := &accountcore.Record{
		Platform: capability.PlatformGemini,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"refresh_token": "rt",
			"oauth_type":    "code_assist",
			"project_id":    "proj",
			"tier_id":       "gcp_standard",
		},
	}

	info, err := svc.RefreshAccountToken(context.Background(), account)
	if err != nil {
		t.Fatalf("RefreshAccountToken 应在 fallback 后成功: %v", err)
	}
	if info.AccessToken != "recovered" {
		t.Fatalf("AccessToken 不匹配: got=%q", info.AccessToken)
	}
}

func TestGeminiOAuthService_RefreshAccountToken_UnauthorizedClient_NoFallback(t *testing.T) {
	t.Parallel()

	client := &mockGeminiOAuthClient{
		refreshTokenFunc: func(ctx context.Context, oauthType, refreshToken, proxyURL string) (*geminicli.TokenResponse, error) {
			return nil, fmt.Errorf("unauthorized_client: client mismatch")
		},
	}

	// 无自定义 OAuth 客户端，无法 fallback
	svc := newGeminiAuthorizationForTest(&mockGeminiProxyRepo{}, client, nil, nil, &geminicli.OAuthConfig{})
	svc.Start()
	defer stopGeminiAuthorization(t, svc)

	account := &accountcore.Record{
		Platform: capability.PlatformGemini,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"refresh_token": "rt",
			"oauth_type":    "code_assist",
			"project_id":    "proj",
		},
	}

	_, err := svc.RefreshAccountToken(context.Background(), account)
	if err == nil {
		t.Fatal("应返回错误（无 fallback）")
	}
	if !strings.Contains(err.Error(), "OAuth client mismatch") {
		t.Fatalf("错误应包含 OAuth client mismatch: got=%q", err.Error())
	}
}

// =====================
// 新增测试：GeminiOAuthService.ExchangeCode
// =====================

func TestGeminiOAuthService_ExchangeCode_SessionNotFound(t *testing.T) {
	t.Parallel()

	svc := newGeminiAuthorizationForTest(nil, nil, nil, nil, &geminicli.OAuthConfig{})
	svc.Start()
	defer stopGeminiAuthorization(t, svc)

	_, err := svc.ExchangeCode(context.Background(), &accountcore.GeminiExchangeCodeInput{
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

func TestGeminiOAuthService_ExchangeCode_InvalidState(t *testing.T) {
	t.Parallel()

	svc := newGeminiAuthorizationForTest(nil, nil, nil, nil, &geminicli.OAuthConfig{})
	svc.Start()
	defer stopGeminiAuthorization(t, svc)

	// 手动创建 session（必须设置 CreatedAt，否则会因 TTL 过期被拒绝）
	svc.Store.Set("test-session", &accountcore.GeminiOAuthSession{
		State:        "correct-state",
		CodeVerifier: "verifier",
		OAuthType:    "ai_studio",
		CreatedAt:    time.Now(),
	})

	_, err := svc.ExchangeCode(context.Background(), &accountcore.GeminiExchangeCodeInput{
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

func TestGeminiOAuthService_ExchangeCode_EmptyState(t *testing.T) {
	t.Parallel()

	svc := newGeminiAuthorizationForTest(nil, nil, nil, nil, &geminicli.OAuthConfig{})
	svc.Start()
	defer stopGeminiAuthorization(t, svc)

	svc.Store.Set("test-session", &accountcore.GeminiOAuthSession{
		State:        "correct-state",
		CodeVerifier: "verifier",
		CreatedAt:    time.Now(),
	})

	_, err := svc.ExchangeCode(context.Background(), &accountcore.GeminiExchangeCodeInput{
		SessionID: "test-session",
		State:     "",
		Code:      "code",
	})
	if err == nil {
		t.Fatal("应返回错误（空 state）")
	}
}

// =====================
// 辅助函数
// =====================

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

func credKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}
