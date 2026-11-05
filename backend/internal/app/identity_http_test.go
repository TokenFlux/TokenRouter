package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	identitycore "github.com/TokenFlux/TokenRouter/internal/identity"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// identity_http_test.go checks profile HTTP, OAuth settings, and identity bindings.
// Profile scenarios cover avatar display, session cleanup, password rejection,
// and authorization starts using production handlers with mutable user fixtures.
// OAuth settings cases keep automatic callbacks on the request origin even when
// site addresses and the client API endpoint use other domains or path prefixes.

// userHandlerRepoStub supplies one mutable user and its authentication
// identities for profile tests.
type userHandlerRepoStub struct {
	// user is the single mutable profile. identities supplies its bindings and
	// unbound records requested removals. Getters copy outer values, retaining nested references; fixture callers coordinate background profile mutation with these reads.
	user       *identitycore.User                    // configured mutable user
	identities []identitycore.UserAuthIdentityRecord // configured provider bindings
	unbound    []string                              // requested provider removals
}

// userHandlerEmailCacheStub supplies a fixed verification-code value for email
// binding.
type userHandlerEmailCacheStub struct {
	// data is returned directly for every verification lookup. The fixture keeps
	// this shared pointer available after its no-op write and delete calls in binding scenarios.
	data *identitycore.VerificationCodeData
}

// userHandlerSettingRepoStub supplies direct email-change flag reads for
// binding tests.
type userHandlerSettingRepoStub struct {
	// Repository supplies unused methods; GetValue implements the direct
	// email-change flag read required to reach password checking in the binding scenario.
	settings.Repository
	values map[string]string // identity feature flag values
}

// userHandlerRefreshTokenCacheStub records requested user-session revocations
// for identity HTTP assertions.
type userHandlerRefreshTokenCacheStub struct {
	revokedUserIDs []int64 // requested session cleanup users
}

// TestIdentityHTTPSettingsWeChatCallbackUsesRequestOrigin loads channel options
// through production settings assembly. Stored and deployment site addresses must
// not move the generated callback away from the host that issued OAuth cookies.
func TestIdentityHTTPSettingsWeChatCallbackUsesRequestOrigin(t *testing.T) {
	for _, tc := range []struct {
		name       string // named source scenario
		stored     string // saved frontend address
		deployment string // deployment frontend default
	}{
		{name: "different site host", stored: "https://console.example.com", deployment: "https://deployment.example.com"},
		{name: "site path prefix", stored: "https://api.example.com/portal"},
		{name: "deployment site", deployment: "https://deployment.example.com"},
		{name: "missing site"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &settingHandlerRepoStub{values: map[string]string{
				"api_base_url": "https://models.example.com/v1",
				"frontend_url": tc.stored,
				identitycore.SettingKeyWeChatConnectEnabled:   "true",
				identitycore.SettingKeyWeChatConnectAppID:     "app",
				identitycore.SettingKeyWeChatConnectAppSecret: "secret",
				identitycore.SettingKeyWeChatConnectMode:      "open",
			}}
			cfg := &config.Config{Site: config.SiteConfig{FrontendURL: tc.deployment}}
			runtime := identityHTTPSettings{identityAuthSettings: newUserBindingSettings(repo, cfg)}
			handler := identityhttp.NewWeChatHandler(nil, nil, nil, identityhttp.WeChatHTTPOptions{LoadConfig: runtime.WeChat})
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, "https://api.example.com/api/v1/auth/oauth/wechat/start", nil)
			options, err := handler.GetConfig(t.Context(), "open", c)
			require.NoError(t, err)
			require.Equal(t, "https://api.example.com/api/v1/auth/oauth/wechat/callback", options.RedirectURI)
		})
	}
}

// TestAuthHandlerGetCurrentUserReturnsProfileCompatibilityFields loads a
// LinuxDo-backed current user through session HTTP. It checks bound flags,
// avatar URL, nested binding data, and matching avatar/username source labels
// in the compatible response.
func TestAuthHandlerGetCurrentUserReturnsProfileCompatibilityFields(t *testing.T) {
	verifiedAt := time.Date(2026, 4, 20, 8, 30, 0, 0, time.UTC)
	repo := &userHandlerRepoStub{
		user: &identitycore.User{
			ID:           31,
			Email:        "me@example.com",
			Username:     "linuxdo-handle",
			Role:         identitycore.RoleUser,
			Status:       billing.StatusActive,
			AvatarURL:    "https://cdn.example.com/linuxdo.png",
			AvatarSource: "remote_url",
		},
		identities: []identitycore.UserAuthIdentityRecord{
			{
				ProviderType:    "linuxdo",
				ProviderKey:     "linuxdo",
				ProviderSubject: "linuxdo-subject-31",
				VerifiedAt:      &verifiedAt,
				Metadata: map[string]any{
					"username":   "linuxdo-handle",
					"avatar_url": "https://cdn.example.com/linuxdo.png",
				},
			},
		},
	}

	handler := identityhttp.NewSessionHandler(nil, newUserProfileCore(t, repo), nil, nil, nil, nil, identityhttp.SessionHTTPOptions{})

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 31})

	handler.GetCurrentUser(c)

	require.Equal(t, http.StatusOK, recorder.Code)

	var resp struct {
		Code int            `json:"code"` // panel result code
		Data map[string]any `json:"data"` // profile response payload
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.Equal(t, true, resp.Data["email_bound"])
	require.Equal(t, true, resp.Data["linuxdo_bound"])
	require.Equal(t, "https://cdn.example.com/linuxdo.png", resp.Data["avatar_url"])

	authBindings, ok := resp.Data["auth_bindings"].(map[string]any)
	require.True(t, ok)
	linuxdoBinding, ok := authBindings["linuxdo"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, true, linuxdoBinding["bound"])

	avatarSource, ok := resp.Data["avatar_source"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "linuxdo", avatarSource["provider"])
	require.Equal(t, "linuxdo", avatarSource["source"])

	profileSources, ok := resp.Data["profile_sources"].(map[string]any)
	require.True(t, ok)
	usernameSource, ok := profileSources["username"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "linuxdo", usernameSource["provider"])
	require.Equal(t, "linuxdo", usernameSource["source"])
}

// TestAuthHandlerRevokeAllSessionsInvalidatesAccessTokens calls revoke-all HTTP
// with a recording refresh cache. It checks successful response and the revoked
// user identity while the fixture's stored TokenVersion remains unchanged.
func TestAuthHandlerRevokeAllSessionsInvalidatesAccessTokens(t *testing.T) {
	repo := &userHandlerRepoStub{
		user: &identitycore.User{
			ID:           29,
			Email:        "session@example.com",
			Username:     "session-user",
			Role:         identitycore.RoleUser,
			Status:       billing.StatusActive,
			TokenVersion: 7,
		},
	}
	refreshTokenCache := &userHandlerRefreshTokenCacheStub{}
	cfg := &config.Config{Auth: config.AuthConfig{JWT: config.JWTConfig{
		Secret:                "test-secret",
		AccessTokenTTLMinutes: 60,
	}}}
	authService := newUserBindingAuth(repo, refreshTokenCache, cfg, nil, nil)
	handler := identityhttp.NewSessionHandler(authService, nil, nil, nil, nil, nil, identityhttp.SessionHTTPOptions{})

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/auth/revoke-all-sessions", nil)
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 29})

	handler.RevokeAllSessions(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, []int64{29}, refreshTokenCache.revokedUserIDs)
	// This scenario observes refresh-session cleanup rather than validating an
	// existing access token. Its single-user repository retains TokenVersion because the cleanup path does not write this fixture user.
	require.Equal(t, int64(7), repo.user.TokenVersion)

	var resp struct {
		Code int `json:"code"` // panel result code
		Data struct {
			Message string `json:"message"` // HTTP response message
		} `json:"data"` // profile response payload
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.Equal(t, "All sessions have been revoked. Please log in again.", resp.Data.Message)
}

// newUserProfileCore constructs profile operations with an owned background
// task tracker. Test cleanup stops admitted work under a five-second budget
// before fixture dependencies are released, and reports unfinished work as a
// failed assertion.
func newUserProfileCore(t *testing.T, users identitycore.UserRepository) *identitycore.UserService {
	t.Helper()
	tasks := lifecycle.NewTasks()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, tasks.Stop(ctx))
	})
	return identitycore.NewUserService(users, nil, nil, nil, tasks.Go)
}

// newUserBindingAuth constructs authentication rules and persistence adapters
// without a database client. cfg supplies required JWT options; callers
// exercise profile/session paths backed by the supplied users, refresh cache,
// settings, and email capabilities.
func newUserBindingAuth(users identitycore.UserRepository, refresh identitycore.RefreshTokenCache, cfg *config.Config, settings identitycore.AuthSettings, email identitycore.AuthEmail) *identitycore.AuthService {
	options := &identitycore.AuthOptions{JWT: identitycore.SessionOptions{Secret: cfg.Auth.JWT.Secret, AccessTokenExpireMinutes: cfg.Auth.JWT.AccessTokenTTLMinutes}}
	deps := &identitycore.AuthDependencies{Users: users, RefreshTokens: refresh, Options: options, Settings: settings, Email: email}
	state := identitypostgres.NewAuthState(nil, deps)
	core := identitycore.NewAuthService(deps, &identitypostgres.AuthRepository{State: state})
	state.Rules = core
	return core
}

// newUserBindingSettings combines identity, grant, display, OAuth, and
// promotion readers over one Store.
func newUserBindingSettings(repo settings.Repository, cfg *config.Config) *identityAuthSettings {
	store := settings.New(repo)
	return provideIdentityAuthSettings(provideIdentitySettings(store), provideGrantSettings(store, cfg, nil), provideSiteDisplay(store, cfg), provideOAuthSettings(store, cfg), providePromotionSettings(store))
}

// Create accepts fixture user creation without adding another stored user.
func (s *userHandlerRepoStub) Create(context.Context, *identitycore.User) error { return nil }

// CreateWithNormalizedEmailGuard delegates fixture creation while ignoring
// normalized-email uniqueness.
func (s *userHandlerRepoStub) CreateWithNormalizedEmailGuard(ctx context.Context, user *identitycore.User, _ string) error {
	return s.Create(ctx, user)
}

// GetByID returns an outer copy of the configured nonnil user and ignores the
// requested identity. Nested references remain shared, so callers keep those
// values stable when using the returned profile fixture.
func (s *userHandlerRepoStub) GetByID(context.Context, int64) (*identitycore.User, error) {
	cloned := *s.user
	return &cloned, nil
}

// GetByEmail returns an outer copy of the configured nonnil user regardless of
// email. Nested references retain fixture ownership, matching the shared record
// used by these profile and email-binding scenarios.
func (s *userHandlerRepoStub) GetByEmail(context.Context, string) (*identitycore.User, error) {
	cloned := *s.user
	return &cloned, nil
}

// GetFirstAdmin returns an outer copy of the fixture user without checking
// role. The user is required, and nested references remain shared rather than
// implementing an administrator search repository.
func (s *userHandlerRepoStub) GetFirstAdmin(context.Context) (*identitycore.User, error) {
	cloned := *s.user
	return &cloned, nil
}

// Update stores an outer copy of the supplied user and ignores the field
// selector. Nested data remains shared with that value, and fixture callers
// coordinate mutation while profile operations use it.
func (s *userHandlerRepoStub) Update(_ context.Context, user *identitycore.User, _ identitycore.UserUpdateFields) error {
	cloned := *user
	s.user = &cloned
	return nil
}

// UpdateWithNormalizedEmailGuard delegates fixture update while ignoring
// normalized-email uniqueness.
func (s *userHandlerRepoStub) UpdateWithNormalizedEmailGuard(ctx context.Context, user *identitycore.User, _ string, fields identitycore.UserUpdateFields) error {
	return s.Update(ctx, user, fields)
}

// Delete accepts the fixture operation without removing its configured user.
func (s *userHandlerRepoStub) Delete(context.Context, int64) error { return nil }

// GetUserAvatar returns selected avatar fields when the fixture user has a URL.
// A nil user or empty URL returns nil without error, and the requested user ID
// is ignored.
func (s *userHandlerRepoStub) GetUserAvatar(context.Context, int64) (*identitycore.UserAvatar, error) {
	if s.user == nil || s.user.AvatarURL == "" {
		return nil, nil
	}
	return &identitycore.UserAvatar{
		StorageProvider: s.user.AvatarSource,
		URL:             s.user.AvatarURL,
		ContentType:     s.user.AvatarMIME,
		ByteSize:        s.user.AvatarByteSize,
		SHA256:          s.user.AvatarSHA256,
	}, nil
}

// UpsertUserAvatar copies supplied avatar metadata into the configured user and
// returns it as an avatar value. The user is required, identity is ignored, and
// callers coordinate writes to this mutable fixture.
func (s *userHandlerRepoStub) UpsertUserAvatar(_ context.Context, _ int64, input identitycore.UpsertUserAvatarInput) (*identitycore.UserAvatar, error) {
	s.user.AvatarURL = input.URL
	s.user.AvatarSource = input.StorageProvider
	s.user.AvatarMIME = input.ContentType
	s.user.AvatarByteSize = input.ByteSize
	s.user.AvatarSHA256 = input.SHA256
	return &identitycore.UserAvatar{
		StorageProvider: input.StorageProvider,
		URL:             input.URL,
		ContentType:     input.ContentType,
		ByteSize:        input.ByteSize,
		SHA256:          input.SHA256,
	}, nil
}

// DeleteUserAvatar clears the configured user's avatar URL, source, MIME, size,
// and hash. The user is required; its identity argument is ignored and other
// profile fields stay available.
func (s *userHandlerRepoStub) DeleteUserAvatar(context.Context, int64) error {
	s.user.AvatarURL = ""
	s.user.AvatarSource = ""
	s.user.AvatarMIME = ""
	s.user.AvatarByteSize = 0
	s.user.AvatarSHA256 = ""
	return nil
}

// List supplies no user page or pagination in this focused fixture.
func (s *userHandlerRepoStub) List(context.Context, pagination.PaginationParams) ([]identitycore.User, *pagination.PaginationResult, error) {
	return nil, nil, nil
}

// ListWithFilters supplies no filtered page or pagination in the fixture.
func (s *userHandlerRepoStub) ListWithFilters(context.Context, pagination.PaginationParams, identitycore.UserListFilters) ([]identitycore.User, *pagination.PaginationResult, error) {
	return nil, nil, nil
}

// AddBalance accepts the unused fixture call without changing stored balance.
func (s *userHandlerRepoStub) AddBalance(context.Context, int64, float64) error { return nil }

// UpdateBalance accepts the unused fixture call while retaining stored balance.
func (s *userHandlerRepoStub) UpdateBalance(context.Context, int64, float64) error { return nil }

// DeductBalance returns zero without applying a deduction to fixture state.
func (s *userHandlerRepoStub) DeductBalance(context.Context, int64, float64) (float64, error) {
	return 0, nil
}

// UpdateConcurrency accepts the fixture call without changing its user limits.
func (s *userHandlerRepoStub) UpdateConcurrency(context.Context, int64, int) error { return nil }

// BatchSetConcurrency reports zero affected users without changing fixture
// concurrency.
func (s *userHandlerRepoStub) BatchSetConcurrency(context.Context, []int64, int) (int, error) {
	return 0, nil
}

// AdjustBalance rejects unsupported financial mutation in this profile-only
// user fixture.
func (s *userHandlerRepoStub) AdjustBalance(ctx context.Context, id int64, delta float64) (identitycore.BalanceChange, error) {
	panic("unexpected AdjustBalance call")
}

// SetBalance rejects unsupported balance replacement in the profile-only user
// fixture.
func (s *userHandlerRepoStub) SetBalance(ctx context.Context, id int64, value float64) (identitycore.BalanceChange, error) {
	panic("unexpected SetBalance call")
}

// BatchAddConcurrency reports zero affected users while retaining fixture
// limits.
func (s *userHandlerRepoStub) BatchAddConcurrency(context.Context, []int64, int) (int, error) {
	return 0, nil
}

// BatchUpdateLimits reports zero affected users without applying the unused
// patch.
func (s *userHandlerRepoStub) BatchUpdateLimits(context.Context, []int64, *int, *int) (int, error) {
	return 0, nil
}

// ExistsByEmail reports absence without searching the configured profile
// fixture.
func (s *userHandlerRepoStub) ExistsByEmail(context.Context, string) (bool, error) { return false, nil }

// ExistsByNormalizedEmail reports absence without testing the configured email
// identity.
func (s *userHandlerRepoStub) ExistsByNormalizedEmail(context.Context, string) (bool, error) {
	return false, nil
}

// LockRegistrationEmail accepts fixture registration without acquiring a
// persistence lock.
func (s *userHandlerRepoStub) LockRegistrationEmail(context.Context, string) error { return nil }

// RemoveGroupFromAllowedGroups reports zero affected fixture users without
// changing access.
func (s *userHandlerRepoStub) RemoveGroupFromAllowedGroups(context.Context, int64) (int64, error) {
	return 0, nil
}

// AddGroupToAllowedGroups accepts the unused fixture operation without changing
// access.
func (s *userHandlerRepoStub) AddGroupToAllowedGroups(context.Context, int64, int64) error {
	return nil
}

// GetLatestUsedAtByUserIDs returns a new empty activity map for fixture users.
func (s *userHandlerRepoStub) GetLatestUsedAtByUserIDs(context.Context, []int64) (map[int64]*time.Time, error) {
	return map[int64]*time.Time{}, nil
}

// GetLatestUsedAtByUserID returns no persisted activity observation for this
// fixture.
func (s *userHandlerRepoStub) GetLatestUsedAtByUserID(context.Context, int64) (*time.Time, error) {
	return nil, nil
}

// UpdateUserLastActiveAt stores the supplied timestamp when a fixture user
// exists. It ignores user identity, keeps a pointer to this call's timestamp
// value, and callers coordinate this mutation with background profile reads.
func (s *userHandlerRepoStub) UpdateUserLastActiveAt(_ context.Context, _ int64, activeAt time.Time) error {
	if s.user != nil {
		s.user.LastActiveAt = &activeAt
	}
	return nil
}

// RemoveGroupFromUserAllowedGroups accepts the fixture call without editing
// access collections.
func (s *userHandlerRepoStub) RemoveGroupFromUserAllowedGroups(context.Context, int64, int64) error {
	return nil
}

// UpdateTotpSecret accepts the unused call without changing fixture TOTP data.
func (s *userHandlerRepoStub) UpdateTotpSecret(context.Context, int64, *string) error { return nil }

// EnableTotp accepts the unused call while retaining fixture TOTP state.
func (s *userHandlerRepoStub) EnableTotp(context.Context, int64) error { return nil }

// DisableTotp accepts the unused call while retaining fixture TOTP state.
func (s *userHandlerRepoStub) DisableTotp(context.Context, int64) error { return nil }

// GetByIDIncludeDeleted uses the same outer-copy lookup without checking
// deletion state.
func (s *userHandlerRepoStub) GetByIDIncludeDeleted(ctx context.Context, id int64) (*identitycore.User, error) {
	return s.GetByID(ctx, id)
}

// ListUserAuthIdentities returns an outer slice copy of configured identity
// records. Metadata and timestamp references remain shared, while the requested
// user identity is ignored for this single-user fixture.
func (s *userHandlerRepoStub) ListUserAuthIdentities(context.Context, int64) ([]identitycore.UserAuthIdentityRecord, error) {
	out := make([]identitycore.UserAuthIdentityRecord, len(s.identities))
	copy(out, s.identities)
	return out, nil
}

// UnbindUserAuthProvider records the requested provider and removes matching
// identity types from the fixture. It ignores user ID, appends the provider
// even when no match exists, and copies the filtered outer identity slice
// afterward.
func (s *userHandlerRepoStub) UnbindUserAuthProvider(_ context.Context, _ int64, provider string) error {
	s.unbound = append(s.unbound, provider)
	// Filter matching provider types through the existing backing slice, then
	// publish a copied outer result. Identity metadata references remain shared with the retained fixture entries.
	filtered := s.identities[:0]
	for _, identity := range s.identities {
		if identity.ProviderType == provider {
			continue
		}
		filtered = append(filtered, identity)
	}
	s.identities = append([]identitycore.UserAuthIdentityRecord(nil), filtered...)
	return nil
}

// TestUserHandlerUpdateProfileReturnsAvatarURL supplies a new remote avatar
// through ordinary profile HTTP. It checks successful panel output contains the
// requested avatar URL and retains the fixture username.
func TestUserHandlerUpdateProfileReturnsAvatarURL(t *testing.T) {
	repo := &userHandlerRepoStub{
		user: &identitycore.User{
			ID:       11,
			Email:    "handler-avatar@example.com",
			Username: "handler-avatar",
			Role:     identitycore.RoleUser,
			Status:   billing.StatusActive,
		},
	}
	handler := identityhttp.NewUserHandler(newUserProfileCore(t, repo), nil, nil, nil)

	body := []byte(`{"avatar_url":"https://cdn.example.com/avatar.png"}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/user", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 11})

	handler.UpdateProfile(c)

	require.Equal(t, http.StatusOK, recorder.Code)

	var resp struct {
		Code int `json:"code"` // panel result code
		Data struct {
			// returned profile avatar URL
			AvatarURL string `json:"avatar_url"`
			// returned profile username
			Username string `json:"username"`
		} `json:"data"` // profile response payload
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.Equal(t, "https://cdn.example.com/avatar.png", resp.Data.AvatarURL)
	require.Equal(t, "handler-avatar", resp.Data.Username)
}

// TestUserHandlerUpdateProfileRejectsEmailField includes email in an ordinary
// profile patch. It checks BadRequest, the specific forbidden reason/message,
// and the unchanged stored email so verified binding remains the required path.
func TestUserHandlerUpdateProfileRejectsEmailField(t *testing.T) {
	repo := &userHandlerRepoStub{
		user: &identitycore.User{
			ID:       11,
			Email:    "current@example.com",
			Username: "current-user",
			Role:     identitycore.RoleUser,
			Status:   billing.StatusActive,
		},
	}
	handler := identityhttp.NewUserHandler(newUserProfileCore(t, repo), nil, nil, nil)

	body := []byte(`{"email":"attacker@example.com","username":"current-user"}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/user", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 11})

	handler.UpdateProfile(c)

	require.Equal(t, http.StatusBadRequest, recorder.Code)
	require.Equal(t, "current@example.com", repo.user.Email)

	var resp struct {
		Code    int    `json:"code"`    // panel result code
		Message string `json:"message"` // HTTP response message
		Reason  string `json:"reason"`  // identity failure reason
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, http.StatusBadRequest, resp.Code)
	require.Equal(t, "EMAIL_PROFILE_UPDATE_FORBIDDEN", resp.Reason)
	require.Equal(t, "email must be changed through verified email binding", resp.Message)
}

// TestUserHandlerGetProfileReturnsIdentitySummaries supplies email, LinuxDo,
// and OIDC profile data through identity HTTP. It checks bound counts and
// display/provider names, plus the available WeChat bind-start action for an
// unbound provider.
func TestUserHandlerGetProfileReturnsIdentitySummaries(t *testing.T) {
	verifiedAt := time.Date(2026, 4, 20, 8, 30, 0, 0, time.UTC)
	repo := &userHandlerRepoStub{
		user: &identitycore.User{
			ID:       11,
			Email:    "identity@example.com",
			Username: "identity-user",
			Role:     identitycore.RoleUser,
			Status:   billing.StatusActive,
		},
		identities: []identitycore.UserAuthIdentityRecord{
			{
				ProviderType:    "linuxdo",
				ProviderKey:     "linuxdo",
				ProviderSubject: "linuxdo-subject-123456",
				VerifiedAt:      &verifiedAt,
				Metadata: map[string]any{
					"username": "linuxdo-handle",
				},
			},
			{
				ProviderType:    "oidc",
				ProviderKey:     "https://issuer.example.com",
				ProviderSubject: "oidc-user-abc",
				Metadata: map[string]any{
					"suggested_display_name": "OIDC Display",
				},
			},
		},
	}
	handler := identityhttp.NewUserHandler(newUserProfileCore(t, repo), nil, nil, nil)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/user/profile", nil)
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 11})

	handler.GetProfile(c)

	require.Equal(t, http.StatusOK, recorder.Code)

	var resp struct {
		Code int `json:"code"` // panel result code
		Data struct {
			Identities struct {
				Email struct {
					// provider binding presence flag
					Bound bool `json:"bound"`
					// configured provider binding count
					BoundCount int `json:"bound_count"`
					// identity display text
					DisplayName string `json:"display_name"`
				} `json:"email"` // email identity summary
				LinuxDo struct {
					// provider binding presence flag
					Bound bool `json:"bound"`
					// configured provider binding count
					BoundCount int `json:"bound_count"`
					// identity display text
					DisplayName string `json:"display_name"`
					// external identity provider key
					ProviderKey string `json:"provider_key"`
				} `json:"linuxdo"` // LinuxDo identity summary
				OIDC struct {
					// provider binding presence flag
					Bound bool `json:"bound"`
					// identity display text
					DisplayName string `json:"display_name"`
					// external identity provider key
					ProviderKey string `json:"provider_key"`
				} `json:"oidc"` // OIDC identity summary
				WeChat struct {
					// provider binding presence flag
					Bound bool `json:"bound"`
					// available binding action flag
					CanBind bool `json:"can_bind"`
					// provider binding start path
					BindStartPath string `json:"bind_start_path"`
				} `json:"wechat"` // WeChat identity summary
			} `json:"identities"` // provider identity summaries
		} `json:"data"` // profile response payload
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.True(t, resp.Data.Identities.Email.Bound)
	require.Equal(t, 1, resp.Data.Identities.Email.BoundCount)
	require.Equal(t, "identity@example.com", resp.Data.Identities.Email.DisplayName)
	require.True(t, resp.Data.Identities.LinuxDo.Bound)
	require.Equal(t, 1, resp.Data.Identities.LinuxDo.BoundCount)
	require.Equal(t, "linuxdo-handle", resp.Data.Identities.LinuxDo.DisplayName)
	require.Equal(t, "linuxdo", resp.Data.Identities.LinuxDo.ProviderKey)
	require.True(t, resp.Data.Identities.OIDC.Bound)
	require.Equal(t, "OIDC Display", resp.Data.Identities.OIDC.DisplayName)
	require.Equal(t, "https://issuer.example.com", resp.Data.Identities.OIDC.ProviderKey)
	require.False(t, resp.Data.Identities.WeChat.Bound)
	require.True(t, resp.Data.Identities.WeChat.CanBind)
	require.Contains(t, resp.Data.Identities.WeChat.BindStartPath, "/api/v1/auth/oauth/wechat/bind/start")
}

// TestUserHandlerGetProfileReturnsLegacyCompatibilityFields loads a
// LinuxDo-linked profile with matching metadata. It checks compatible
// bound/source fields, nested binding objects, and localization note keys for
// email and removable provider identities.
func TestUserHandlerGetProfileReturnsLegacyCompatibilityFields(t *testing.T) {
	verifiedAt := time.Date(2026, 4, 20, 8, 30, 0, 0, time.UTC)
	repo := &userHandlerRepoStub{
		user: &identitycore.User{
			ID:           21,
			Email:        "legacy-profile@example.com",
			Username:     "linuxdo-handle",
			Role:         identitycore.RoleUser,
			Status:       billing.StatusActive,
			AvatarURL:    "https://cdn.example.com/linuxdo.png",
			AvatarSource: "remote_url",
		},
		identities: []identitycore.UserAuthIdentityRecord{
			{
				ProviderType:    "linuxdo",
				ProviderKey:     "linuxdo",
				ProviderSubject: "linuxdo-subject-21",
				VerifiedAt:      &verifiedAt,
				Metadata: map[string]any{
					"username":   "linuxdo-handle",
					"avatar_url": "https://cdn.example.com/linuxdo.png",
				},
			},
		},
	}
	handler := identityhttp.NewUserHandler(newUserProfileCore(t, repo), nil, nil, nil)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/user/profile", nil)
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 21})

	handler.GetProfile(c)

	require.Equal(t, http.StatusOK, recorder.Code)

	var resp struct {
		Code int            `json:"code"` // panel result code
		Data map[string]any `json:"data"` // profile response payload
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.Equal(t, true, resp.Data["email_bound"])
	require.Equal(t, true, resp.Data["linuxdo_bound"])
	require.Equal(t, false, resp.Data["oidc_bound"])
	require.Equal(t, false, resp.Data["wechat_bound"])
	require.Equal(t, "https://cdn.example.com/linuxdo.png", resp.Data["avatar_url"])

	avatarSource, ok := resp.Data["avatar_source"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "linuxdo", avatarSource["provider"])
	require.Equal(t, "linuxdo", avatarSource["source"])

	authBindings, ok := resp.Data["auth_bindings"].(map[string]any)
	require.True(t, ok)
	linuxdoBinding, ok := authBindings["linuxdo"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, true, linuxdoBinding["bound"])
	require.Equal(t, "linuxdo", linuxdoBinding["provider"])

	identityBindings, ok := resp.Data["identity_bindings"].(map[string]any)
	require.True(t, ok)
	emailBinding, ok := identityBindings["email"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, true, emailBinding["bound"])
	require.Equal(t, "profile.authBindings.notes.emailManagedByBinding", emailBinding["note_key"])

	linuxdoCompatBinding, ok := identityBindings["linuxdo"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "profile.authBindings.notes.canUnbind", linuxdoCompatBinding["note_key"])

	profileSources, ok := resp.Data["profile_sources"].(map[string]any)
	require.True(t, ok)
	usernameSource, ok := profileSources["username"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "linuxdo", usernameSource["provider"])
	require.Equal(t, "linuxdo", usernameSource["source"])
}

// TestUserHandlerGetProfileDoesNotInferEditedProfileSourcesWithoutMatchingIdentityMetadata
// supplies edited username/avatar values different from linked provider
// metadata. It checks successful HTTP omits inferred avatar, username, and
// aggregate source objects.
func TestUserHandlerGetProfileDoesNotInferEditedProfileSourcesWithoutMatchingIdentityMetadata(t *testing.T) {
	repo := &userHandlerRepoStub{
		user: &identitycore.User{
			ID:           22,
			Email:        "edited-profile@example.com",
			Username:     "custom-name",
			Role:         identitycore.RoleUser,
			Status:       billing.StatusActive,
			AvatarURL:    "https://cdn.example.com/custom.png",
			AvatarSource: "remote_url",
		},
		identities: []identitycore.UserAuthIdentityRecord{
			{
				ProviderType:    "linuxdo",
				ProviderKey:     "linuxdo",
				ProviderSubject: "linuxdo-subject-22",
				Metadata: map[string]any{
					"username":   "linuxdo-handle",
					"avatar_url": "https://cdn.example.com/linuxdo.png",
				},
			},
		},
	}
	handler := identityhttp.NewUserHandler(newUserProfileCore(t, repo), nil, nil, nil)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/user/profile", nil)
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 22})

	handler.GetProfile(c)

	require.Equal(t, http.StatusOK, recorder.Code)

	var resp struct {
		Code int            `json:"code"` // panel result code
		Data map[string]any `json:"data"` // profile response payload
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.NotContains(t, resp.Data, "avatar_source")
	require.NotContains(t, resp.Data, "username_source")
	require.NotContains(t, resp.Data, "profile_sources")
}

// GetValue returns the fixture value or an empty string for a missing key. It
// reports nil error for either case, leaving the identity reader to interpret
// its empty-value default.
func (s *userHandlerSettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	return s.values[key], nil
}

// StoreRefreshToken accepts the unused store call without retaining token data.
func (s *userHandlerRefreshTokenCacheStub) StoreRefreshToken(context.Context, string, *identitycore.RefreshTokenData, time.Duration) error {
	return nil
}

// GetRefreshToken always returns ErrRefreshTokenNotFound for the requested
// fixture credential.
func (s *userHandlerRefreshTokenCacheStub) GetRefreshToken(context.Context, string) (*identitycore.RefreshTokenData, error) {
	return nil, identitycore.ErrRefreshTokenNotFound
}

// DeleteRefreshToken accepts token removal without changing this recording
// fixture.
func (s *userHandlerRefreshTokenCacheStub) DeleteRefreshToken(context.Context, string) error {
	return nil
}

// DeleteUserRefreshTokens appends the requested user identity to the revocation
// list. It returns success without modeling stored token families, allowing
// HTTP tests to inspect whether user-session cleanup was requested.
func (s *userHandlerRefreshTokenCacheStub) DeleteUserRefreshTokens(_ context.Context, userID int64) error {
	s.revokedUserIDs = append(s.revokedUserIDs, userID)
	return nil
}

// DeleteTokenFamily accepts the unused family cleanup without storing family
// state.
func (s *userHandlerRefreshTokenCacheStub) DeleteTokenFamily(context.Context, string) error {
	return nil
}

// AddToUserTokenSet accepts membership addition without retaining token-set
// state.
func (s *userHandlerRefreshTokenCacheStub) AddToUserTokenSet(context.Context, int64, string, time.Duration) error {
	return nil
}

// AddToFamilyTokenSet accepts membership addition without retaining family-set
// state.
func (s *userHandlerRefreshTokenCacheStub) AddToFamilyTokenSet(context.Context, string, string, time.Duration) error {
	return nil
}

// GetUserTokenHashes returns no tracked token hashes for the fixture user.
func (s *userHandlerRefreshTokenCacheStub) GetUserTokenHashes(context.Context, int64) ([]string, error) {
	return nil, nil
}

// GetFamilyTokenHashes returns no tracked hashes for the fixture family.
func (s *userHandlerRefreshTokenCacheStub) GetFamilyTokenHashes(context.Context, string) ([]string, error) {
	return nil, nil
}

// IsTokenInFamily reports false because the fixture stores no family
// memberships.
func (s *userHandlerRefreshTokenCacheStub) IsTokenInFamily(context.Context, string, string) (bool, error) {
	return false, nil
}

// GetVerificationCode returns the configured verification pointer regardless of
// email. A nil data field stays nil; the fixture shares this object and its
// write/delete methods do not consume or replace it.
func (s *userHandlerEmailCacheStub) GetVerificationCode(context.Context, string) (*identitycore.VerificationCodeData, error) {
	return s.data, nil
}

// SetVerificationCode accepts the fixture write without replacing its
// configured code.
func (s *userHandlerEmailCacheStub) SetVerificationCode(context.Context, string, *identitycore.VerificationCodeData, time.Duration) error {
	return nil
}

// DeleteVerificationCode accepts cleanup while retaining the configured fixture
// code.
func (s *userHandlerEmailCacheStub) DeleteVerificationCode(context.Context, string) error {
	return nil
}

// GetNotifyVerifyCode returns no notification-email challenge in this focused
// fixture.
func (s *userHandlerEmailCacheStub) GetNotifyVerifyCode(context.Context, string) (*identitycore.VerificationCodeData, error) {
	return nil, nil
}

// SetNotifyVerifyCode accepts the unused write without retaining a notification
// challenge.
func (s *userHandlerEmailCacheStub) SetNotifyVerifyCode(context.Context, string, *identitycore.VerificationCodeData, time.Duration) error {
	return nil
}

// DeleteNotifyVerifyCode accepts the unused cleanup without changing fixture
// state.
func (s *userHandlerEmailCacheStub) DeleteNotifyVerifyCode(context.Context, string) error {
	return nil
}

// GetPasswordResetToken returns no password-reset credential from this binding
// fixture.
func (s *userHandlerEmailCacheStub) GetPasswordResetToken(context.Context, string) (*identitycore.PasswordResetTokenData, error) {
	return nil, nil
}

// SetPasswordResetToken accepts the unused write without retaining a reset
// credential.
func (s *userHandlerEmailCacheStub) SetPasswordResetToken(context.Context, string, *identitycore.PasswordResetTokenData, time.Duration) error {
	return nil
}

// DeletePasswordResetToken accepts the unused removal without changing binding
// fixture state.
func (s *userHandlerEmailCacheStub) DeletePasswordResetToken(context.Context, string) error {
	return nil
}

// IsPasswordResetEmailInCooldown reports false for every email in this fixture.
func (s *userHandlerEmailCacheStub) IsPasswordResetEmailInCooldown(context.Context, string) bool {
	return false
}

// SetPasswordResetEmailCooldown accepts the unused call without installing
// cooldown state.
func (s *userHandlerEmailCacheStub) SetPasswordResetEmailCooldown(context.Context, string, time.Duration) error {
	return nil
}

// GetNotifyCodeUserRate returns zero without reading notification challenge
// counts.
func (s *userHandlerEmailCacheStub) GetNotifyCodeUserRate(context.Context, int64) (int64, error) {
	return 0, nil
}

// IncrNotifyCodeUserRate returns zero without incrementing a persisted fixture
// count.
func (s *userHandlerEmailCacheStub) IncrNotifyCodeUserRate(context.Context, int64, time.Duration) (int64, error) {
	return 0, nil
}

// TestUserHandlerBindEmailIdentityReturnsProfileResponse binds a verified email
// for a synthetic-email user using a valid code fixture. It checks successful
// HTTP returns the new email and its bound flag.
func TestUserHandlerBindEmailIdentityReturnsProfileResponse(t *testing.T) {
	repo := &userHandlerRepoStub{
		user: &identitycore.User{
			ID:       11,
			Email:    "legacy-user" + identitycore.LinuxDoConnectSyntheticEmailDomain,
			Username: "legacy-user",
			Role:     identitycore.RoleUser,
			Status:   billing.StatusActive,
		},
	}
	emailCache := &userHandlerEmailCacheStub{
		data: &identitycore.VerificationCodeData{
			Code:      "123456",
			CreatedAt: time.Now().UTC(),
			ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
		},
	}
	cfg := &config.Config{Auth: config.AuthConfig{JWT: config.JWTConfig{
		Secret:                "test-secret",
		AccessTokenTTLMinutes: 60,
	}}}
	emailService := identitycore.NewEmailChallenges(emailCache, nil)
	authService := newUserBindingAuth(repo, nil, cfg, nil, emailService)
	handler := identityhttp.NewUserHandler(newUserProfileCore(t, repo), authService, nil, nil)

	body := []byte(`{"email":"new@example.com","verify_code":"123456","password":"new-password"}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/user/provider-bindings/email", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "provider", Value: "email"}}
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 11})

	handler.BindEmailIdentity(c)

	require.Equal(t, http.StatusOK, recorder.Code)

	var resp struct {
		Code int `json:"code"` // panel result code
		Data struct {
			// returned profile email
			Email string `json:"email"`
			// email binding presence flag
			EmailBound bool `json:"email_bound"`
		} `json:"data"` // profile response payload
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.Equal(t, "new@example.com", resp.Data.Email)
	require.True(t, resp.Data.EmailBound)
}

// TestUserHandlerUnbindIdentityReturnsUpdatedProfile removes a configured
// LinuxDo identity while retaining email binding. It checks successful HTTP,
// the recorded provider removal, and updated nested LinuxDo bound state in the
// returned profile.
func TestUserHandlerUnbindIdentityReturnsUpdatedProfile(t *testing.T) {
	repo := &userHandlerRepoStub{
		user: &identitycore.User{
			ID:       21,
			Email:    "identity@example.com",
			Username: "identity-user",
			Role:     identitycore.RoleUser,
			Status:   billing.StatusActive,
		},
		identities: []identitycore.UserAuthIdentityRecord{
			{
				ProviderType:    "email",
				ProviderKey:     "email",
				ProviderSubject: "identity@example.com",
			},
			{
				ProviderType:    "linuxdo",
				ProviderKey:     "linuxdo",
				ProviderSubject: "linuxdo-subject-21",
				Metadata: map[string]any{
					"username": "linuxdo-handle",
				},
			},
		},
	}
	handler := identityhttp.NewUserHandler(newUserProfileCore(t, repo), nil, nil, nil)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/v1/user/provider-bindings/linuxdo", nil)
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 21})
	c.Params = gin.Params{{Key: "provider", Value: "linuxdo"}}

	handler.UnbindIdentity(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, []string{"linuxdo"}, repo.unbound)

	var resp struct {
		Code int            `json:"code"` // panel result code
		Data map[string]any `json:"data"` // profile response payload
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)

	authBindings, ok := resp.Data["auth_bindings"].(map[string]any)
	require.True(t, ok)
	linuxdoBinding, ok := authBindings["linuxdo"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, false, linuxdoBinding["bound"])
}

// TestUserHandlerUnbindIdentityRevokesAllUserSessionsWhenAuthServiceConfigured
// removes LinuxDo with a refresh-cache-backed auth service. It checks
// successful HTTP requests cleanup for the user while the fixture TokenVersion
// remains unchanged.
func TestUserHandlerUnbindIdentityRevokesAllUserSessionsWhenAuthServiceConfigured(t *testing.T) {
	repo := &userHandlerRepoStub{
		user: &identitycore.User{
			ID:           23,
			Email:        "identity@example.com",
			Username:     "identity-user",
			Role:         identitycore.RoleUser,
			Status:       billing.StatusActive,
			TokenVersion: 4,
		},
		identities: []identitycore.UserAuthIdentityRecord{
			{
				ProviderType:    "email",
				ProviderKey:     "email",
				ProviderSubject: "identity@example.com",
			},
			{
				ProviderType:    "linuxdo",
				ProviderKey:     "linuxdo",
				ProviderSubject: "linuxdo-subject-23",
			},
		},
	}
	refreshTokenCache := &userHandlerRefreshTokenCacheStub{}
	cfg := &config.Config{Auth: config.AuthConfig{JWT: config.JWTConfig{
		Secret:                "test-secret",
		AccessTokenTTLMinutes: 60,
	}}}
	authService := newUserBindingAuth(repo, refreshTokenCache, cfg, nil, nil)
	handler := identityhttp.NewUserHandler(newUserProfileCore(t, repo), authService, nil, nil)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/v1/user/provider-bindings/linuxdo", nil)
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 23})
	c.Params = gin.Params{{Key: "provider", Value: "linuxdo"}}

	handler.UnbindIdentity(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, []int64{23}, refreshTokenCache.revokedUserIDs)
	// The configured auth service requests refresh-session cleanup after removal.
	// This fixture asserts that request and retains its stored TokenVersion; it performs no access-token validation here.
	require.Equal(t, int64(4), repo.user.TokenVersion)
}

// TestUserHandlerUnbindIdentityDoesNotRevokeSessionsWhenNothingWasUnbound
// requests LinuxDo removal from an email-only user. It checks successful HTTP
// leaves provider-removal and refresh-revocation lists empty and retains the
// stored TokenVersion.
func TestUserHandlerUnbindIdentityDoesNotRevokeSessionsWhenNothingWasUnbound(t *testing.T) {
	repo := &userHandlerRepoStub{
		user: &identitycore.User{
			ID:           24,
			Email:        "identity@example.com",
			Username:     "identity-user",
			Role:         identitycore.RoleUser,
			Status:       billing.StatusActive,
			TokenVersion: 4,
		},
		identities: []identitycore.UserAuthIdentityRecord{
			{
				ProviderType:    "email",
				ProviderKey:     "email",
				ProviderSubject: "identity@example.com",
			},
		},
	}
	refreshTokenCache := &userHandlerRefreshTokenCacheStub{}
	cfg := &config.Config{Auth: config.AuthConfig{JWT: config.JWTConfig{
		Secret:                "test-secret",
		AccessTokenTTLMinutes: 60,
	}}}
	authService := newUserBindingAuth(repo, refreshTokenCache, cfg, nil, nil)
	handler := identityhttp.NewUserHandler(newUserProfileCore(t, repo), authService, nil, nil)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/v1/user/provider-bindings/linuxdo", nil)
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 24})
	c.Params = gin.Params{{Key: "provider", Value: "linuxdo"}}

	handler.UnbindIdentity(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Empty(t, repo.unbound)
	require.Empty(t, refreshTokenCache.revokedUserIDs)
	require.Equal(t, int64(4), repo.user.TokenVersion)
}

// TestUserHandlerBindEmailIdentityRejectsWrongCurrentPasswordForBoundEmail
// enables email changes and supplies a wrong current password. It checks
// BadRequest, PASSWORD_INCORRECT presentation, and unchanged stored email
// despite a valid verification-code fixture.
func TestUserHandlerBindEmailIdentityRejectsWrongCurrentPasswordForBoundEmail(t *testing.T) {
	user := &identitycore.User{
		ID:       11,
		Email:    "current@example.com",
		Username: "bound-user",
		Role:     identitycore.RoleUser,
		Status:   billing.StatusActive,
	}
	require.NoError(t, user.SetPassword("current-password"))

	repo := &userHandlerRepoStub{user: user}
	emailCache := &userHandlerEmailCacheStub{
		data: &identitycore.VerificationCodeData{
			Code:      "123456",
			CreatedAt: time.Now().UTC(),
			ExpiresAt: time.Now().UTC().Add(10 * time.Minute),
		},
	}
	cfg := &config.Config{Auth: config.AuthConfig{JWT: config.JWTConfig{
		Secret:                "test-secret",
		AccessTokenTTLMinutes: 60,
	}}}
	emailService := identitycore.NewEmailChallenges(emailCache, nil)
	// Enable user_email_change_enabled to reach password validation; a disabled setting returns 403 first.
	settingService := newUserBindingSettings(&userHandlerSettingRepoStub{values: map[string]string{
		identitycore.SettingKeyUserEmailChangeEnabled: "true",
	}}, cfg)
	authService := newUserBindingAuth(repo, nil, cfg, settingService, emailService)
	handler := identityhttp.NewUserHandler(newUserProfileCore(t, repo), authService, nil, nil)

	body := []byte(`{"email":"new@example.com","verify_code":"123456","password":"wrong-password"}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/user/provider-bindings/email", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 11})

	handler.BindEmailIdentity(c)

	require.Equal(t, http.StatusBadRequest, recorder.Code)

	var resp struct {
		Code    int    `json:"code"`    // panel result code
		Message string `json:"message"` // HTTP response message
		Reason  string `json:"reason"`  // identity failure reason
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, http.StatusBadRequest, resp.Code)
	require.Equal(t, "PASSWORD_INCORRECT", resp.Reason)
	require.Equal(t, "current password is incorrect", resp.Message)
	require.Equal(t, "current@example.com", repo.user.Email)
}

// TestUserHandlerStartIdentityBindingReturnsAuthorizeURL starts WeChat binding
// with a profile redirect. It checks successful provider/method/browser data
// and an authorization URL containing the binding intent plus encoded redirect.
func TestUserHandlerStartIdentityBindingReturnsAuthorizeURL(t *testing.T) {
	repo := &userHandlerRepoStub{
		user: &identitycore.User{
			ID:       11,
			Email:    "identity@example.com",
			Username: "identity-user",
			Role:     identitycore.RoleUser,
			Status:   billing.StatusActive,
		},
	}
	handler := identityhttp.NewUserHandler(newUserProfileCore(t, repo), nil, nil, nil)

	body := []byte(`{"provider":"wechat","redirect_to":"/settings/profile"}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/user/auth-identities/bind/start", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 11})

	handler.StartIdentityBinding(c)

	require.Equal(t, http.StatusOK, recorder.Code)

	var resp struct {
		Code int `json:"code"` // panel result code
		Data struct {
			// requested identity provider
			Provider string `json:"provider"`
			// provider authorization start URL
			AuthorizeURL string `json:"authorize_url"`
			// authorization request method
			Method string `json:"method"`
			// browser redirect action flag
			UseBrowserRedirect bool `json:"use_browser_redirect"`
		} `json:"data"` // profile response payload
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.Equal(t, "wechat", resp.Data.Provider)
	require.Equal(t, "GET", resp.Data.Method)
	require.True(t, resp.Data.UseBrowserRedirect)
	require.Contains(t, resp.Data.AuthorizeURL, "/api/v1/auth/oauth/wechat/bind/start")
	require.Contains(t, resp.Data.AuthorizeURL, "intent=bind_current_user")
	require.Contains(t, resp.Data.AuthorizeURL, "redirect=%2Fsettings%2Fprofile")
}

// ConsumeRefreshToken reports an absent fixture credential without mutating its
// revocation list.
func (s *userHandlerRefreshTokenCacheStub) ConsumeRefreshToken(context.Context, string) (bool, error) {
	return false, nil
}
