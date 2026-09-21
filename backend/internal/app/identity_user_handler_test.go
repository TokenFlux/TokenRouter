//go:build unit

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"

	identitycore "github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"

	"github.com/TokenFlux/TokenRouter/internal/settings"

	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	authctx "github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type userHandlerRepoStub struct {
	user       *identitycore.User
	identities []identitycore.UserAuthIdentityRecord
	unbound    []string
}

func (s *userHandlerRepoStub) Create(context.Context, *identitycore.User) error { return nil }
func (s *userHandlerRepoStub) CreateWithNormalizedEmailGuard(ctx context.Context, user *identitycore.User, _ string) error {
	return s.Create(ctx, user)
}
func (s *userHandlerRepoStub) GetByID(context.Context, int64) (*identitycore.User, error) {
	cloned := *s.user
	return &cloned, nil
}
func (s *userHandlerRepoStub) GetByEmail(context.Context, string) (*identitycore.User, error) {
	cloned := *s.user
	return &cloned, nil
}
func (s *userHandlerRepoStub) GetFirstAdmin(context.Context) (*identitycore.User, error) {
	cloned := *s.user
	return &cloned, nil
}
func (s *userHandlerRepoStub) Update(_ context.Context, user *identitycore.User, _ identitycore.UserUpdateFields) error {
	cloned := *user
	s.user = &cloned
	return nil
}
func (s *userHandlerRepoStub) UpdateWithNormalizedEmailGuard(ctx context.Context, user *identitycore.User, _ string, fields identitycore.UserUpdateFields) error {
	return s.Update(ctx, user, fields)
}
func (s *userHandlerRepoStub) Delete(context.Context, int64) error { return nil }
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
func (s *userHandlerRepoStub) DeleteUserAvatar(context.Context, int64) error {
	s.user.AvatarURL = ""
	s.user.AvatarSource = ""
	s.user.AvatarMIME = ""
	s.user.AvatarByteSize = 0
	s.user.AvatarSHA256 = ""
	return nil
}
func (s *userHandlerRepoStub) List(context.Context, pagination.PaginationParams) ([]identitycore.User, *pagination.PaginationResult, error) {
	return nil, nil, nil
}
func (s *userHandlerRepoStub) ListWithFilters(context.Context, pagination.PaginationParams, identitycore.UserListFilters) ([]identitycore.User, *pagination.PaginationResult, error) {
	return nil, nil, nil
}
func (s *userHandlerRepoStub) AddBalance(context.Context, int64, float64) error    { return nil }
func (s *userHandlerRepoStub) UpdateBalance(context.Context, int64, float64) error { return nil }
func (s *userHandlerRepoStub) DeductBalance(context.Context, int64, float64) (float64, error) {
	return 0, nil
}
func (s *userHandlerRepoStub) UpdateConcurrency(context.Context, int64, int) error { return nil }
func (s *userHandlerRepoStub) BatchSetConcurrency(context.Context, []int64, int) (int, error) {
	return 0, nil
}

func (s *userHandlerRepoStub) AdjustBalance(ctx context.Context, id int64, delta float64) (identitycore.BalanceChange, error) {
	panic("unexpected AdjustBalance call")
}

func (s *userHandlerRepoStub) SetBalance(ctx context.Context, id int64, value float64) (identitycore.BalanceChange, error) {
	panic("unexpected SetBalance call")
}
func (s *userHandlerRepoStub) BatchAddConcurrency(context.Context, []int64, int) (int, error) {
	return 0, nil
}
func (s *userHandlerRepoStub) BatchUpdateLimits(context.Context, []int64, *int, *int) (int, error) {
	return 0, nil
}
func (s *userHandlerRepoStub) ExistsByEmail(context.Context, string) (bool, error) { return false, nil }
func (s *userHandlerRepoStub) ExistsByNormalizedEmail(context.Context, string) (bool, error) {
	return false, nil
}
func (s *userHandlerRepoStub) LockRegistrationEmail(context.Context, string) error { return nil }
func (s *userHandlerRepoStub) RemoveGroupFromAllowedGroups(context.Context, int64) (int64, error) {
	return 0, nil
}
func (s *userHandlerRepoStub) AddGroupToAllowedGroups(context.Context, int64, int64) error {
	return nil
}
func (s *userHandlerRepoStub) GetLatestUsedAtByUserIDs(context.Context, []int64) (map[int64]*time.Time, error) {
	return map[int64]*time.Time{}, nil
}
func (s *userHandlerRepoStub) GetLatestUsedAtByUserID(context.Context, int64) (*time.Time, error) {
	return nil, nil
}
func (s *userHandlerRepoStub) UpdateUserLastActiveAt(_ context.Context, _ int64, activeAt time.Time) error {
	if s.user != nil {
		s.user.LastActiveAt = &activeAt
	}
	return nil
}
func (s *userHandlerRepoStub) RemoveGroupFromUserAllowedGroups(context.Context, int64, int64) error {
	return nil
}
func (s *userHandlerRepoStub) UpdateTotpSecret(context.Context, int64, *string) error { return nil }
func (s *userHandlerRepoStub) EnableTotp(context.Context, int64) error                { return nil }
func (s *userHandlerRepoStub) DisableTotp(context.Context, int64) error               { return nil }
func (s *userHandlerRepoStub) GetByIDIncludeDeleted(ctx context.Context, id int64) (*identitycore.User, error) {
	return s.GetByID(ctx, id)
}
func (s *userHandlerRepoStub) ListUserAuthIdentities(context.Context, int64) ([]identitycore.UserAuthIdentityRecord, error) {
	out := make([]identitycore.UserAuthIdentityRecord, len(s.identities))
	copy(out, s.identities)
	return out, nil
}
func (s *userHandlerRepoStub) UnbindUserAuthProvider(_ context.Context, _ int64, provider string) error {
	s.unbound = append(s.unbound, provider)
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
		Code int `json:"code"`
		Data struct {
			AvatarURL string `json:"avatar_url"`
			Username  string `json:"username"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.Equal(t, "https://cdn.example.com/avatar.png", resp.Data.AvatarURL)
	require.Equal(t, "handler-avatar", resp.Data.Username)
}

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
		Code    int    `json:"code"`
		Message string `json:"message"`
		Reason  string `json:"reason"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, http.StatusBadRequest, resp.Code)
	require.Equal(t, "EMAIL_PROFILE_UPDATE_FORBIDDEN", resp.Reason)
	require.Equal(t, "email must be changed through verified email binding", resp.Message)
}

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
		Code int `json:"code"`
		Data struct {
			Identities struct {
				Email struct {
					Bound       bool   `json:"bound"`
					BoundCount  int    `json:"bound_count"`
					DisplayName string `json:"display_name"`
				} `json:"email"`
				LinuxDo struct {
					Bound       bool   `json:"bound"`
					BoundCount  int    `json:"bound_count"`
					DisplayName string `json:"display_name"`
					ProviderKey string `json:"provider_key"`
				} `json:"linuxdo"`
				OIDC struct {
					Bound       bool   `json:"bound"`
					DisplayName string `json:"display_name"`
					ProviderKey string `json:"provider_key"`
				} `json:"oidc"`
				WeChat struct {
					Bound         bool   `json:"bound"`
					CanBind       bool   `json:"can_bind"`
					BindStartPath string `json:"bind_start_path"`
				} `json:"wechat"`
			} `json:"identities"`
		} `json:"data"`
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
		Code int            `json:"code"`
		Data map[string]any `json:"data"`
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
		Code int            `json:"code"`
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.NotContains(t, resp.Data, "avatar_source")
	require.NotContains(t, resp.Data, "username_source")
	require.NotContains(t, resp.Data, "profile_sources")
}

type userHandlerEmailCacheStub struct {
	data *identitycore.VerificationCodeData
}

// userHandlerSettingRepoStub 只实现 GetValue，用于在测试中开启邮箱换绑设置。
type userHandlerSettingRepoStub struct {
	settings.Repository
	values map[string]string
}

func (s *userHandlerSettingRepoStub) GetValue(_ context.Context, key string) (string, error) {
	return s.values[key], nil
}

type userHandlerRefreshTokenCacheStub struct {
	revokedUserIDs []int64
}

func (s *userHandlerRefreshTokenCacheStub) StoreRefreshToken(context.Context, string, *identitycore.RefreshTokenData, time.Duration) error {
	return nil
}

func (s *userHandlerRefreshTokenCacheStub) GetRefreshToken(context.Context, string) (*identitycore.RefreshTokenData, error) {
	return nil, identitycore.ErrRefreshTokenNotFound
}

func (s *userHandlerRefreshTokenCacheStub) DeleteRefreshToken(context.Context, string) error {
	return nil
}

func (s *userHandlerRefreshTokenCacheStub) DeleteUserRefreshTokens(_ context.Context, userID int64) error {
	s.revokedUserIDs = append(s.revokedUserIDs, userID)
	return nil
}

func (s *userHandlerRefreshTokenCacheStub) DeleteTokenFamily(context.Context, string) error {
	return nil
}

func (s *userHandlerRefreshTokenCacheStub) AddToUserTokenSet(context.Context, int64, string, time.Duration) error {
	return nil
}

func (s *userHandlerRefreshTokenCacheStub) AddToFamilyTokenSet(context.Context, string, string, time.Duration) error {
	return nil
}

func (s *userHandlerRefreshTokenCacheStub) GetUserTokenHashes(context.Context, int64) ([]string, error) {
	return nil, nil
}

func (s *userHandlerRefreshTokenCacheStub) GetFamilyTokenHashes(context.Context, string) ([]string, error) {
	return nil, nil
}

func (s *userHandlerRefreshTokenCacheStub) IsTokenInFamily(context.Context, string, string) (bool, error) {
	return false, nil
}

func (s *userHandlerEmailCacheStub) GetVerificationCode(context.Context, string) (*identitycore.VerificationCodeData, error) {
	return s.data, nil
}

func (s *userHandlerEmailCacheStub) SetVerificationCode(context.Context, string, *identitycore.VerificationCodeData, time.Duration) error {
	return nil
}

func (s *userHandlerEmailCacheStub) DeleteVerificationCode(context.Context, string) error {
	return nil
}

func (s *userHandlerEmailCacheStub) GetNotifyVerifyCode(context.Context, string) (*identitycore.VerificationCodeData, error) {
	return nil, nil
}

func (s *userHandlerEmailCacheStub) SetNotifyVerifyCode(context.Context, string, *identitycore.VerificationCodeData, time.Duration) error {
	return nil
}

func (s *userHandlerEmailCacheStub) DeleteNotifyVerifyCode(context.Context, string) error {
	return nil
}

func (s *userHandlerEmailCacheStub) GetPasswordResetToken(context.Context, string) (*identitycore.PasswordResetTokenData, error) {
	return nil, nil
}

func (s *userHandlerEmailCacheStub) SetPasswordResetToken(context.Context, string, *identitycore.PasswordResetTokenData, time.Duration) error {
	return nil
}

func (s *userHandlerEmailCacheStub) DeletePasswordResetToken(context.Context, string) error {
	return nil
}

func (s *userHandlerEmailCacheStub) IsPasswordResetEmailInCooldown(context.Context, string) bool {
	return false
}

func (s *userHandlerEmailCacheStub) SetPasswordResetEmailCooldown(context.Context, string, time.Duration) error {
	return nil
}

func (s *userHandlerEmailCacheStub) GetNotifyCodeUserRate(context.Context, int64) (int64, error) {
	return 0, nil
}

func (s *userHandlerEmailCacheStub) IncrNotifyCodeUserRate(context.Context, int64, time.Duration) (int64, error) {
	return 0, nil
}

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
	cfg := &config.Config{
		JWT: config.JWTConfig{
			Secret:     "test-secret",
			ExpireHour: 1,
		},
	}
	emailService := identitycore.NewEmailChallenges(emailCache, nil)
	authService := newUserBindingAuth(repo, nil, cfg, nil, emailService)
	handler := identityhttp.NewUserHandler(newUserProfileCore(t, repo), authService, nil, nil)

	body := []byte(`{"email":"new@example.com","verify_code":"123456","password":"new-password"}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/user/account-bindings/email", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "provider", Value: "email"}}
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 11})

	handler.BindEmailIdentity(c)

	require.Equal(t, http.StatusOK, recorder.Code)

	var resp struct {
		Code int `json:"code"`
		Data struct {
			Email      string `json:"email"`
			EmailBound bool   `json:"email_bound"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.Equal(t, "new@example.com", resp.Data.Email)
	require.True(t, resp.Data.EmailBound)
}

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
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/v1/user/account-bindings/linuxdo", nil)
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 21})
	c.Params = gin.Params{{Key: "provider", Value: "linuxdo"}}

	handler.UnbindIdentity(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, []string{"linuxdo"}, repo.unbound)

	var resp struct {
		Code int            `json:"code"`
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)

	authBindings, ok := resp.Data["auth_bindings"].(map[string]any)
	require.True(t, ok)
	linuxdoBinding, ok := authBindings["linuxdo"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, false, linuxdoBinding["bound"])
}

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
	cfg := &config.Config{
		JWT: config.JWTConfig{
			Secret:     "test-secret",
			ExpireHour: 1,
		},
	}
	authService := newUserBindingAuth(repo, refreshTokenCache, cfg, nil, nil)
	handler := identityhttp.NewUserHandler(newUserProfileCore(t, repo), authService, nil, nil)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/v1/user/account-bindings/linuxdo", nil)
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 23})
	c.Params = gin.Params{{Key: "provider", Value: "linuxdo"}}

	handler.UnbindIdentity(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Equal(t, []int64{23}, refreshTokenCache.revokedUserIDs)
	// 撤销依赖的是 refresh session 清理，而不是 token_version：users 表没有这一列
	// （见 resolvedTokenVersion，实际值由 email+password_hash 指纹推导），
	// 所以此前"自增 TokenVersion 再整行写回"不持久化任何东西，
	// 却会用旧快照覆盖并发写入的列。这里断言用户行未被改写。
	require.Equal(t, int64(4), repo.user.TokenVersion)
}

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
	cfg := &config.Config{
		JWT: config.JWTConfig{
			Secret:     "test-secret",
			ExpireHour: 1,
		},
	}
	authService := newUserBindingAuth(repo, refreshTokenCache, cfg, nil, nil)
	handler := identityhttp.NewUserHandler(newUserProfileCore(t, repo), authService, nil, nil)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodDelete, "/api/v1/user/account-bindings/linuxdo", nil)
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 24})
	c.Params = gin.Params{{Key: "provider", Value: "linuxdo"}}

	handler.UnbindIdentity(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Empty(t, repo.unbound)
	require.Empty(t, refreshTokenCache.revokedUserIDs)
	require.Equal(t, int64(4), repo.user.TokenVersion)
}

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
	cfg := &config.Config{
		JWT: config.JWTConfig{
			Secret:     "test-secret",
			ExpireHour: 1,
		},
	}
	emailService := identitycore.NewEmailChallenges(emailCache, nil)
	// 换绑主邮箱需要显式开启 user_email_change_enabled，否则会在校验密码前被 403 拦截。
	settingService := newUserBindingSettings(&userHandlerSettingRepoStub{values: map[string]string{
		identitycore.SettingKeyUserEmailChangeEnabled: "true",
	}}, cfg)
	authService := newUserBindingAuth(repo, nil, cfg, settingService, emailService)
	handler := identityhttp.NewUserHandler(newUserProfileCore(t, repo), authService, nil, nil)

	body := []byte(`{"email":"new@example.com","verify_code":"123456","password":"wrong-password"}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/user/account-bindings/email", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 11})

	handler.BindEmailIdentity(c)

	require.Equal(t, http.StatusBadRequest, recorder.Code)

	var resp struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Reason  string `json:"reason"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, http.StatusBadRequest, resp.Code)
	require.Equal(t, "PASSWORD_INCORRECT", resp.Reason)
	require.Equal(t, "current password is incorrect", resp.Message)
	require.Equal(t, "current@example.com", repo.user.Email)
}

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
		Code int `json:"code"`
		Data struct {
			Provider           string `json:"provider"`
			AuthorizeURL       string `json:"authorize_url"`
			Method             string `json:"method"`
			UseBrowserRedirect bool   `json:"use_browser_redirect"`
		} `json:"data"`
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

// ConsumeRefreshToken 与此桩始终未找到凭据的读取行为一致。
func (s *userHandlerRefreshTokenCacheStub) ConsumeRefreshToken(context.Context, string) (bool, error) {
	return false, nil
}
