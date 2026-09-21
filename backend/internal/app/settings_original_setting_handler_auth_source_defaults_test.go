package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/promotion"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/site"

	"github.com/TokenFlux/TokenRouter/internal/identity"

	"github.com/TokenFlux/TokenRouter/internal/ops"

	settingscore "github.com/TokenFlux/TokenRouter/internal/settings"

	"github.com/TokenFlux/TokenRouter/internal/settings/composite"

	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	settingshttp "github.com/TokenFlux/TokenRouter/internal/settings/httpapi"
	settingsdto "github.com/TokenFlux/TokenRouter/internal/settings/httpapi/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type settingHandlerRepoStub struct {
	values      map[string]string
	lastUpdates map[string]string
}

func (s *settingHandlerRepoStub) Get(ctx context.Context, key string) (*settingscore.Setting, error) {
	panic("unexpected Get call")
}

func (s *settingHandlerRepoStub) GetValue(ctx context.Context, key string) (string, error) {
	if s.values != nil {
		if value, ok := s.values[key]; ok {
			return value, nil
		}
	}
	return "", nil
}

func (s *settingHandlerRepoStub) Set(ctx context.Context, key, value string) error {
	panic("unexpected Set call")
}

func (s *settingHandlerRepoStub) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := s.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

func (s *settingHandlerRepoStub) SetMultiple(ctx context.Context, settings map[string]string) error {
	s.lastUpdates = make(map[string]string, len(settings))
	for key, value := range settings {
		s.lastUpdates[key] = value
		if s.values == nil {
			s.values = map[string]string{}
		}
		s.values[key] = value
	}
	return nil
}

func (s *settingHandlerRepoStub) GetAll(ctx context.Context) (map[string]string, error) {
	out := make(map[string]string, len(s.values))
	for key, value := range s.values {
		out[key] = value
	}
	return out, nil
}

func (s *settingHandlerRepoStub) Delete(ctx context.Context, key string) error {
	panic("unexpected Delete call")
}

type failingAuthSourceSettingsRepoStub struct {
	values map[string]string
	err    error
}

func (s *failingAuthSourceSettingsRepoStub) Get(ctx context.Context, key string) (*settingscore.Setting, error) {
	panic("unexpected Get call")
}

func (s *failingAuthSourceSettingsRepoStub) GetValue(ctx context.Context, key string) (string, error) {
	panic("unexpected GetValue call")
}

func (s *failingAuthSourceSettingsRepoStub) Set(ctx context.Context, key, value string) error {
	panic("unexpected Set call")
}

func (s *failingAuthSourceSettingsRepoStub) GetMultiple(ctx context.Context, keys []string) (map[string]string, error) {
	out := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := s.values[key]; ok {
			out[key] = value
		}
	}
	return out, nil
}

func (s *failingAuthSourceSettingsRepoStub) SetMultiple(ctx context.Context, settings map[string]string) error {
	if _, ok := settings[identity.SettingKeyAuthSourceDefaultEmailBalance]; ok {
		return s.err
	}
	for key, value := range settings {
		if s.values == nil {
			s.values = map[string]string{}
		}
		s.values[key] = value
	}
	return nil
}

func (s *failingAuthSourceSettingsRepoStub) GetAll(ctx context.Context) (map[string]string, error) {
	out := make(map[string]string, len(s.values))
	for key, value := range s.values {
		out[key] = value
	}
	return out, nil
}

func (s *failingAuthSourceSettingsRepoStub) Delete(ctx context.Context, key string) error {
	panic("unexpected Delete call")
}

func TestSettingHandler_GetSettings_InjectsAuthSourceDefaults(t *testing.T) {

	repo := &settingHandlerRepoStub{
		values: map[string]string{
			identity.SettingKeyRegistrationEnabled:                 "true",
			promotion.SettingKeyPromoCodeEnabled:                   "true",
			identity.SettingKeyAuthSourceDefaultEmailBalance:       "9.5",
			identity.SettingKeyAuthSourceDefaultEmailConcurrency:   "8",
			identity.SettingKeyAuthSourceDefaultEmailSubscriptions: `[{"plan_id":31}]`,
			identity.SettingKeyForceEmailOnThirdPartySignup:        "true",
		},
	}
	options, _ := newCompositeSettingsHTTPFixture(repo, &config.Config{Default: config.DefaultConfig{UserConcurrency: 5}})
	handler := settingshttp.NewHandler(options)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings", nil)

	handler.GetSettings(c)

	require.Equal(t, http.StatusOK, rec.Code)
	var resp response.Response
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	data, ok := resp.Data.(map[string]any)
	require.True(t, ok)
	require.Equal(t, 9.5, data["auth_source_default_email_balance"])
	require.Equal(t, float64(8), data["auth_source_default_email_concurrency"])
	require.Equal(t, true, data["force_email_on_third_party_signup"])

	subscriptions, ok := data["auth_source_default_email_subscriptions"].([]any)
	require.True(t, ok)
	require.Len(t, subscriptions, 1)
}

func TestSettingHandler_UpdateSettings_PreservesOmittedAuthSourceDefaults(t *testing.T) {

	repo := &settingHandlerRepoStub{
		values: map[string]string{
			identity.SettingKeyRegistrationEnabled:                    "false",
			promotion.SettingKeyPromoCodeEnabled:                      "true",
			identity.SettingKeyAuthSourceDefaultEmailBalance:          "9.5",
			identity.SettingKeyAuthSourceDefaultEmailConcurrency:      "8",
			identity.SettingKeyAuthSourceDefaultEmailSubscriptions:    `[{"plan_id":31}]`,
			identity.SettingKeyAuthSourceDefaultEmailGrantOnSignup:    "true",
			identity.SettingKeyAuthSourceDefaultEmailGrantOnFirstBind: "false",
			identity.SettingKeyForceEmailOnThirdPartySignup:           "true",
		},
	}
	options, _ := newCompositeSettingsHTTPFixture(repo, &config.Config{Default: config.DefaultConfig{UserConcurrency: 5}})
	handler := settingshttp.NewHandler(options)

	body := map[string]any{
		"registration_enabled":              true,
		"promo_code_enabled":                true,
		"auth_source_default_email_balance": 12.75,
	}
	rawBody, err := json.Marshal(body)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", bytes.NewReader(rawBody))
	c.Request.Header.Set("Content-Type", "application/json")

	handler.UpdateSettings(c)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "12.75000000", repo.values[identity.SettingKeyAuthSourceDefaultEmailBalance])
	require.Equal(t, "8", repo.values[identity.SettingKeyAuthSourceDefaultEmailConcurrency])
	require.Equal(t, `[{"plan_id":31}]`, repo.values[identity.SettingKeyAuthSourceDefaultEmailSubscriptions])
	require.Equal(t, "true", repo.values[identity.SettingKeyForceEmailOnThirdPartySignup])

	var resp response.Response
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	data, ok := resp.Data.(map[string]any)
	require.True(t, ok)
	require.Equal(t, 12.75, data["auth_source_default_email_balance"])
	require.Equal(t, float64(8), data["auth_source_default_email_concurrency"])
	require.Equal(t, true, data["force_email_on_third_party_signup"])
}

func TestSettingHandler_UpdateSettings_AcceptsMarkdownCustomMenuURL(t *testing.T) {

	repo := &settingHandlerRepoStub{
		values: map[string]string{
			promotion.SettingKeyPromoCodeEnabled: "true",
		},
	}
	options, _ := newCompositeSettingsHTTPFixture(repo, &config.Config{Default: config.DefaultConfig{UserConcurrency: 5}})
	handler := settingshttp.NewHandler(options)

	body := map[string]any{
		"promo_code_enabled": true,
		"custom_menu_items": []map[string]any{
			{
				"id":         "guide",
				"label":      "Guide",
				"icon_svg":   "",
				"url":        " md:guide ",
				"visibility": "user",
				"sort_order": 0,
			},
		},
	}
	rawBody, err := json.Marshal(body)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", bytes.NewReader(rawBody))
	c.Request.Header.Set("Content-Type", "application/json")

	handler.UpdateSettings(c)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `[{"id":"guide","label":"Guide","icon_svg":"","url":"md:guide","visibility":"user","sort_order":0}]`, repo.values[site.SettingKeyCustomMenuItems])
}

func TestSettingHandler_UpdateSettings_RejectsEmptyMarkdownCustomMenuSlug(t *testing.T) {

	repo := &settingHandlerRepoStub{
		values: map[string]string{
			promotion.SettingKeyPromoCodeEnabled: "true",
		},
	}
	options, _ := newCompositeSettingsHTTPFixture(repo, &config.Config{Default: config.DefaultConfig{UserConcurrency: 5}})
	handler := settingshttp.NewHandler(options)

	body := map[string]any{
		"promo_code_enabled": true,
		"custom_menu_items": []map[string]any{
			{
				"id":         "guide",
				"label":      "Guide",
				"icon_svg":   "",
				"url":        "md:",
				"visibility": "user",
				"sort_order": 0,
			},
		},
	}
	rawBody, err := json.Marshal(body)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", bytes.NewReader(rawBody))
	c.Request.Header.Set("Content-Type", "application/json")

	handler.UpdateSettings(c)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.NotContains(t, repo.values, site.SettingKeyCustomMenuItems)
}

func TestSettingHandler_UpdateSettings_PersistsPaymentVisibleMethodsAndAdvancedScheduler(t *testing.T) {

	repo := &settingHandlerRepoStub{
		values: map[string]string{
			promotion.SettingKeyPromoCodeEnabled: "true",
			ops.SettingKeyOpsAdvancedSettings:    `{"data_retention":{"cleanup_enabled":true,"cleanup_schedule":"0 4 * * *","error_log_retention_days":12,"minute_metrics_retention_days":8,"hourly_metrics_retention_days":30},"aggregation":{"aggregation_enabled":true},"openai_account_quota_auto_pause":{"default_threshold_5h":0.6,"default_threshold_7d":0.7},"ignore_count_tokens_errors":true,"ignore_context_canceled":true,"ignore_no_available_accounts":false,"ignore_invalid_api_key_errors":false,"ignore_insufficient_balance_errors":true,"display_openai_token_stats":false,"display_alert_events":true,"auto_refresh_enabled":false,"auto_refresh_interval_seconds":30}`,
		},
	}
	options, _ := newCompositeSettingsHTTPFixture(repo, &config.Config{Default: config.DefaultConfig{UserConcurrency: 5}})
	paymentConfigService := payment.NewConfigService(nil, repo, nil, nil, payment.ConfigurationRuntime{})
	options.Payment = paymentConfigService
	handler := settingshttp.NewHandler(options)

	body := map[string]any{
		"promo_code_enabled":                               true,
		"payment_visible_method_alipay_source":             "easypay",
		"payment_visible_method_wxpay_source":              "wxpay",
		"payment_visible_method_alipay_enabled":            true,
		"payment_visible_method_wxpay_enabled":             false,
		"advanced_scheduler_subscription_priority_enabled": true,
		"openai_account_quota_auto_pause": map[string]any{
			"default_threshold_5h": 0.95,
			"default_threshold_7d": 0.9,
		},
	}
	rawBody, err := json.Marshal(body)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", bytes.NewReader(rawBody))
	c.Request.Header.Set("Content-Type", "application/json")

	handler.UpdateSettings(c)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, payment.VisibleMethodSourceEasyPayAlipay, repo.values[payment.SettingPaymentVisibleMethodAlipaySource])
	require.Equal(t, payment.VisibleMethodSourceOfficialWechat, repo.values[payment.SettingPaymentVisibleMethodWxpaySource])
	require.Equal(t, "true", repo.values[payment.SettingPaymentVisibleMethodAlipayEnabled])
	require.Equal(t, "false", repo.values[payment.SettingPaymentVisibleMethodWxpayEnabled])
	require.Equal(t, "true", repo.values[scheduler.SettingKeyAdvancedSchedulerSubscriptionPriorityEnabled])
	var advanced ops.OpsAdvancedSettings
	require.NoError(t, json.Unmarshal([]byte(repo.values[ops.SettingKeyOpsAdvancedSettings]), &advanced))
	require.True(t, advanced.DataRetention.CleanupEnabled)
	require.Equal(t, "0 4 * * *", advanced.DataRetention.CleanupSchedule)
	require.NotContains(t, repo.values[ops.SettingKeyOpsAdvancedSettings], `"aggregation"`)
	require.Equal(t, 0.95, advanced.OpenAIAccountQuotaAutoPause.DefaultThreshold5h)
	require.Equal(t, 0.9, advanced.OpenAIAccountQuotaAutoPause.DefaultThreshold7d)

	var resp response.Response
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	data, ok := resp.Data.(map[string]any)
	require.True(t, ok)
	require.Equal(t, payment.VisibleMethodSourceEasyPayAlipay, data["payment_visible_method_alipay_source"])
	require.Equal(t, payment.VisibleMethodSourceOfficialWechat, data["payment_visible_method_wxpay_source"])
	require.Equal(t, true, data["payment_visible_method_alipay_enabled"])
	require.Equal(t, false, data["payment_visible_method_wxpay_enabled"])
	require.Equal(t, true, data["advanced_scheduler_subscription_priority_enabled"])
	quota, ok := data["openai_account_quota_auto_pause"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, 0.95, quota["default_threshold_5h"])
	require.Equal(t, 0.9, quota["default_threshold_7d"])
}

func TestSettingHandler_UpdateSettings_PreservesLegacyBlankPaymentVisibleMethodSource(t *testing.T) {

	repo := &settingHandlerRepoStub{
		values: map[string]string{
			promotion.SettingKeyPromoCodeEnabled:             "true",
			payment.SettingPaymentVisibleMethodAlipayEnabled: "true",
			payment.SettingPaymentVisibleMethodAlipaySource:  "",
			payment.SettingPaymentVisibleMethodWxpayEnabled:  "false",
			payment.SettingPaymentVisibleMethodWxpaySource:   "",
		},
	}
	options, _ := newCompositeSettingsHTTPFixture(repo, &config.Config{Default: config.DefaultConfig{UserConcurrency: 5}})
	handler := settingshttp.NewHandler(options)

	body := map[string]any{
		"promo_code_enabled": false,
	}
	rawBody, err := json.Marshal(body)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", bytes.NewReader(rawBody))
	c.Request.Header.Set("Content-Type", "application/json")

	handler.UpdateSettings(c)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "", repo.values[payment.SettingPaymentVisibleMethodAlipaySource])
	require.Equal(t, "true", repo.values[payment.SettingPaymentVisibleMethodAlipayEnabled])
}

func TestSettingHandler_UpdateSettings_PersistsExplicitFalseOIDCCompatibilityFlags(t *testing.T) {

	repo := &settingHandlerRepoStub{
		values: map[string]string{
			promotion.SettingKeyPromoCodeEnabled:              "true",
			identity.SettingKeyOIDCConnectEnabled:             "true",
			identity.SettingKeyOIDCConnectProviderName:        "OIDC",
			identity.SettingKeyOIDCConnectClientID:            "oidc-client",
			identity.SettingKeyOIDCConnectClientSecret:        "oidc-secret",
			identity.SettingKeyOIDCConnectIssuerURL:           "https://issuer.example.com",
			identity.SettingKeyOIDCConnectAuthorizeURL:        "https://issuer.example.com/auth",
			identity.SettingKeyOIDCConnectTokenURL:            "https://issuer.example.com/token",
			identity.SettingKeyOIDCConnectUserInfoURL:         "https://issuer.example.com/userinfo",
			identity.SettingKeyOIDCConnectJWKSURL:             "https://issuer.example.com/jwks",
			identity.SettingKeyOIDCConnectScopes:              "openid email profile",
			identity.SettingKeyOIDCConnectRedirectURL:         "https://example.com/api/v1/auth/oauth/oidc/callback",
			identity.SettingKeyOIDCConnectFrontendRedirectURL: "/auth/oidc/callback",
			identity.SettingKeyOIDCConnectTokenAuthMethod:     "client_secret_post",
			identity.SettingKeyOIDCConnectUsePKCE:             "true",
			identity.SettingKeyOIDCConnectValidateIDToken:     "true",
			identity.SettingKeyOIDCConnectAllowedSigningAlgs:  "RS256",
			identity.SettingKeyOIDCConnectClockSkewSeconds:    "120",
		},
	}
	options, _ := newCompositeSettingsHTTPFixture(repo, &config.Config{Default: config.DefaultConfig{UserConcurrency: 5}})
	handler := settingshttp.NewHandler(options)

	body := map[string]any{
		"promo_code_enabled":                true,
		"oidc_connect_enabled":              true,
		"oidc_connect_use_pkce":             false,
		"oidc_connect_validate_id_token":    false,
		"oidc_connect_allowed_signing_algs": "",
	}
	rawBody, err := json.Marshal(body)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", bytes.NewReader(rawBody))
	c.Request.Header.Set("Content-Type", "application/json")

	handler.UpdateSettings(c)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "false", repo.values[identity.SettingKeyOIDCConnectUsePKCE])
	require.Equal(t, "false", repo.values[identity.SettingKeyOIDCConnectValidateIDToken])

	var resp response.Response
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	data, ok := resp.Data.(map[string]any)
	require.True(t, ok)
	require.Equal(t, false, data["oidc_connect_use_pkce"])
	require.Equal(t, false, data["oidc_connect_validate_id_token"])
}

func TestSettingHandler_UpdateSettings_DoesNotSolidifyImplicitOIDCSecurityDefaultsOnLegacyUpgrade(t *testing.T) {

	repo := &settingHandlerRepoStub{
		values: map[string]string{
			promotion.SettingKeyPromoCodeEnabled:               "true",
			identity.SettingKeyOIDCConnectEnabled:              "true",
			identity.SettingKeyOIDCConnectProviderName:         "OIDC",
			identity.SettingKeyOIDCConnectClientID:             "oidc-client",
			identity.SettingKeyOIDCConnectClientSecret:         "oidc-secret",
			identity.SettingKeyOIDCConnectIssuerURL:            "https://issuer.example.com",
			identity.SettingKeyOIDCConnectAuthorizeURL:         "https://issuer.example.com/auth",
			identity.SettingKeyOIDCConnectTokenURL:             "https://issuer.example.com/token",
			identity.SettingKeyOIDCConnectUserInfoURL:          "https://issuer.example.com/userinfo",
			identity.SettingKeyOIDCConnectJWKSURL:              "https://issuer.example.com/jwks",
			identity.SettingKeyOIDCConnectScopes:               "openid email profile",
			identity.SettingKeyOIDCConnectRedirectURL:          "https://example.com/api/v1/auth/oauth/oidc/callback",
			identity.SettingKeyOIDCConnectFrontendRedirectURL:  "/auth/oidc/callback",
			identity.SettingKeyOIDCConnectTokenAuthMethod:      "client_secret_post",
			identity.SettingKeyOIDCConnectAllowedSigningAlgs:   "RS256",
			identity.SettingKeyOIDCConnectClockSkewSeconds:     "120",
			identity.SettingKeyOIDCConnectRequireEmailVerified: "false",
			identity.SettingKeyOIDCConnectUserInfoEmailPath:    "",
			identity.SettingKeyOIDCConnectUserInfoIDPath:       "",
			identity.SettingKeyOIDCConnectUserInfoUsernamePath: "",
		},
	}
	options, _ := newCompositeSettingsHTTPFixture(repo, &config.Config{
		Default: config.DefaultConfig{UserConcurrency: 5},
		OIDC: config.OIDCConnectConfig{
			Enabled:             true,
			ProviderName:        "OIDC",
			ClientID:            "oidc-client",
			ClientSecret:        "oidc-secret",
			IssuerURL:           "https://issuer.example.com",
			AuthorizeURL:        "https://issuer.example.com/auth",
			TokenURL:            "https://issuer.example.com/token",
			UserInfoURL:         "https://issuer.example.com/userinfo",
			JWKSURL:             "https://issuer.example.com/jwks",
			Scopes:              "openid email profile",
			RedirectURL:         "https://example.com/api/v1/auth/oauth/oidc/callback",
			FrontendRedirectURL: "/auth/oidc/callback",
			TokenAuthMethod:     "client_secret_post",
			UsePKCE:             true,
			ValidateIDToken:     true,
			AllowedSigningAlgs:  "RS256",
			ClockSkewSeconds:    120,
		},
	})
	handler := settingshttp.NewHandler(options)

	body := map[string]any{
		"promo_code_enabled":   true,
		"oidc_connect_enabled": true,
	}
	rawBody, err := json.Marshal(body)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", bytes.NewReader(rawBody))
	c.Request.Header.Set("Content-Type", "application/json")

	handler.UpdateSettings(c)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, "false", repo.values[identity.SettingKeyOIDCConnectUsePKCE])
	require.Equal(t, "false", repo.values[identity.SettingKeyOIDCConnectValidateIDToken])
}

func TestSettingHandler_UpdateSettings_RejectsInvalidPaymentVisibleMethodSource(t *testing.T) {

	repo := &settingHandlerRepoStub{
		values: map[string]string{
			promotion.SettingKeyPromoCodeEnabled: "true",
		},
	}
	options, _ := newCompositeSettingsHTTPFixture(repo, &config.Config{Default: config.DefaultConfig{UserConcurrency: 5}})
	handler := settingshttp.NewHandler(options)

	body := map[string]any{
		"promo_code_enabled":                   true,
		"payment_visible_method_alipay_source": "bogus",
	}
	rawBody, err := json.Marshal(body)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", bytes.NewReader(rawBody))
	c.Request.Header.Set("Content-Type", "application/json")

	handler.UpdateSettings(c)

	require.Equal(t, http.StatusBadRequest, rec.Code)
	require.NotContains(t, repo.values, payment.SettingPaymentVisibleMethodAlipaySource)
}

func TestSettingHandler_UpdateSettings_DoesNotPersistPartialSystemSettingsWhenAuthSourceDefaultsFail(t *testing.T) {

	repo := &failingAuthSourceSettingsRepoStub{
		values: map[string]string{
			identity.SettingKeyRegistrationEnabled:                 "false",
			promotion.SettingKeyPromoCodeEnabled:                   "true",
			identity.SettingKeyAuthSourceDefaultEmailBalance:       "9.5",
			identity.SettingKeyAuthSourceDefaultEmailConcurrency:   "8",
			identity.SettingKeyAuthSourceDefaultEmailSubscriptions: `[{"plan_id":31}]`,
		},
		err: errors.New("write auth source defaults failed"),
	}
	options, _ := newCompositeSettingsHTTPFixture(repo, &config.Config{Default: config.DefaultConfig{UserConcurrency: 5}})
	handler := settingshttp.NewHandler(options)

	body := map[string]any{
		"registration_enabled":              true,
		"promo_code_enabled":                true,
		"auth_source_default_email_balance": 12.75,
	}
	rawBody, err := json.Marshal(body)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", bytes.NewReader(rawBody))
	c.Request.Header.Set("Content-Type", "application/json")

	handler.UpdateSettings(c)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	require.Equal(t, "false", repo.values[identity.SettingKeyRegistrationEnabled])
	require.Equal(t, "9.5", repo.values[identity.SettingKeyAuthSourceDefaultEmailBalance])
}

func TestDiffSettings_IncludesAuthSourceDefaultsAndForceEmail(t *testing.T) {
	changed := settingshttp.DiffSettings(
		&composite.Snapshot{},
		&composite.Snapshot{},
		&identity.AuthSourceDefaultSettings{
			Email: identity.ProviderDefaultGrantSettings{
				Balance:          0,
				Concurrency:      5,
				Subscriptions:    nil,
				GrantOnSignup:    true,
				GrantOnFirstBind: false,
			},
			ForceEmailOnThirdPartySignup: false,
		},
		&identity.AuthSourceDefaultSettings{
			Email: identity.ProviderDefaultGrantSettings{
				Balance:          12.5,
				Concurrency:      7,
				Subscriptions:    []identity.DefaultSubscriptionSetting{{PlanID: 21}},
				GrantOnSignup:    false,
				GrantOnFirstBind: true,
			},
			ForceEmailOnThirdPartySignup: true,
		},
		settingsdto.UpdateSettingsRequest{},
	)

	require.Contains(t, changed, "auth_source_default_email_balance")
	require.Contains(t, changed, "auth_source_default_email_concurrency")
	require.Contains(t, changed, "auth_source_default_email_subscriptions")
	require.Contains(t, changed, "auth_source_default_email_grant_on_signup")
	require.Contains(t, changed, "auth_source_default_email_grant_on_first_bind")
	require.Contains(t, changed, "force_email_on_third_party_signup")
}
