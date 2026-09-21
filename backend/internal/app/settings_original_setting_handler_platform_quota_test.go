//go:build unit

package app

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/promotion"

	"github.com/TokenFlux/TokenRouter/internal/identity"

	"github.com/TokenFlux/TokenRouter/internal/ops"

	"github.com/TokenFlux/TokenRouter/internal/settings/composite"

	response "github.com/TokenFlux/TokenRouter/internal/server/httpx"
	settingshttp "github.com/TokenFlux/TokenRouter/internal/settings/httpapi"
	settingsdto "github.com/TokenFlux/TokenRouter/internal/settings/httpapi/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDiffSettings_DetectsGlobalPlatformQuotaChange(t *testing.T) {
	five := 5.0
	ten := 10.0
	before := &composite.Snapshot{
		DefaultPlatformQuotas: map[string]*identity.DefaultPlatformQuotaSetting{
			"anthropic": {DailyLimitUSD: &five},
		},
	}
	after := &composite.Snapshot{
		DefaultPlatformQuotas: map[string]*identity.DefaultPlatformQuotaSetting{
			"anthropic": {DailyLimitUSD: &ten},
		},
	}

	changed := settingshttp.DiffSettings(before, after, nil, nil, settingsdto.UpdateSettingsRequest{})
	found := false
	for _, key := range changed {
		if key == billing.SettingKeyDefaultPlatformQuotas {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected change detection for default platform quotas, got %v", changed)
	}
}

func TestDiffSettings_DetectsOpenAIQuotaAutoPauseChange(t *testing.T) {
	before := &composite.Snapshot{
		OpenAIQuotaAutoPauseSettings: ops.OpsOpenAIAccountQuotaAutoPauseSettings{
			DefaultThreshold5h: 0.6,
			DefaultThreshold7d: 0.7,
		},
	}
	after := &composite.Snapshot{
		OpenAIQuotaAutoPauseSettings: ops.OpsOpenAIAccountQuotaAutoPauseSettings{
			DefaultThreshold5h: 0.95,
			DefaultThreshold7d: 0.7,
		},
	}

	changed := settingshttp.DiffSettings(before, after, nil, nil, settingsdto.UpdateSettingsRequest{})
	require.Contains(t, changed, "openai_account_quota_auto_pause")
}

func TestDiffSettings_NoChangeWhenEqual(t *testing.T) {
	five := 5.0
	before := &composite.Snapshot{
		DefaultPlatformQuotas: map[string]*identity.DefaultPlatformQuotaSetting{
			"anthropic": {DailyLimitUSD: &five},
		},
	}
	after := &composite.Snapshot{
		DefaultPlatformQuotas: map[string]*identity.DefaultPlatformQuotaSetting{
			"anthropic": {DailyLimitUSD: &five},
		},
	}

	changed := settingshttp.DiffSettings(before, after, nil, nil, settingsdto.UpdateSettingsRequest{})
	for _, key := range changed {
		if key == billing.SettingKeyDefaultPlatformQuotas {
			t.Error("equal values should not be detected as changed")
		}
	}
}

func TestDiffSettings_DetectsDefaultUserAPIKeyLimitChange(t *testing.T) {
	before := &composite.Snapshot{DefaultUserAPIKeyLimit: 100}
	after := &composite.Snapshot{DefaultUserAPIKeyLimit: 0}

	changed := settingshttp.DiffSettings(before, after, nil, nil, settingsdto.UpdateSettingsRequest{})

	require.Contains(t, changed, identity.SettingKeyDefaultUserAPIKeyLimit)
}

func TestSettingsAuditRequestDoesNotInheritStoredTencentSecrets(t *testing.T) {
	req := settingsdto.UpdateSettingsRequest{
		TencentCaptchaAppSecretKey:   "  ",
		TencentCaptchaCloudSecretID:  "\t",
		TencentCaptchaCloudSecretKey: "\n",
	}

	auditReq := settingshttp.SettingsAuditRequest(req)
	req.TencentCaptchaAppSecretKey = "stored-app-secret"
	req.TencentCaptchaCloudSecretID = "stored-secret-id"
	req.TencentCaptchaCloudSecretKey = "stored-secret-key"

	require.Empty(t, auditReq.TencentCaptchaAppSecretKey)
	require.Empty(t, auditReq.TencentCaptchaCloudSecretID)
	require.Empty(t, auditReq.TencentCaptchaCloudSecretKey)
}

func TestEqualNullableFloat(t *testing.T) {
	five := 5.0
	five2 := 5.0
	ten := 10.0
	cases := []struct {
		a, b *float64
		want bool
	}{
		{nil, nil, true},
		{&five, nil, false},
		{nil, &five, false},
		{&five, &five2, true},
		{&five, &ten, false},
	}
	for _, c := range cases {
		if got := settingshttp.EqualNullableFloat(c.a, c.b); got != c.want {
			t.Errorf("settingshttp.EqualNullableFloat(%v, %v) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestEqualPlatformQuotaSettings_DetectsPerWindowChange(t *testing.T) {
	five := 5.0
	ten := 10.0
	before := map[string]*identity.DefaultPlatformQuotaSetting{
		"anthropic": {DailyLimitUSD: &five},
	}
	after := map[string]*identity.DefaultPlatformQuotaSetting{
		"anthropic": {DailyLimitUSD: &ten},
	}
	if settingshttp.EqualPlatformQuotaSettings(before, after) {
		t.Error("expected unequal")
	}
}

func TestAppendAuthSourceDefaultChanges_DetectsPerWindow(t *testing.T) {
	five := 5.0
	ten := 10.0
	before := &identity.AuthSourceDefaultSettings{
		LinuxDo: identity.ProviderDefaultGrantSettings{
			PlatformQuotas: map[string]*identity.DefaultPlatformQuotaSetting{
				"anthropic": {DailyLimitUSD: &five},
			},
		},
	}
	after := &identity.AuthSourceDefaultSettings{
		LinuxDo: identity.ProviderDefaultGrantSettings{
			PlatformQuotas: map[string]*identity.DefaultPlatformQuotaSetting{
				"anthropic": {DailyLimitUSD: &ten},
			},
		},
	}

	changed := settingshttp.AppendAuthSourceDefaultChanges([]string{}, before, after)
	// 改动 B5：整体替换语义，审计 log 发单个 JSON key，而非展开 84 个扁平 key。
	key := identity.SettingKeyAuthSourcePlatformQuotas("linuxdo")
	found := false
	for _, k := range changed {
		if k == key {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected %q in changed, got %v", key, changed)
	}
}

// TestSettingHandler_AuthSourcePlatformQuotas_PutGetRoundTrip 验证 Bug A 修复：
// PUT 发 auth_source_default_email_platform_quotas，GET 能读回相同值（端到端往返）。
func TestSettingHandler_AuthSourcePlatformQuotas_PutGetRoundTrip(t *testing.T) {

	repo := &settingHandlerRepoStub{
		values: map[string]string{
			promotion.SettingKeyPromoCodeEnabled: "true",
		},
	}
	options, _ := newCompositeSettingsHTTPFixture(repo, &config.Config{Default: config.DefaultConfig{UserConcurrency: 5}})
	handler := settingshttp.NewHandler(options)

	// PUT：发 email platform quota（openai monthly=20）
	putBody := map[string]any{
		"auth_source_default_email_platform_quotas": map[string]any{
			"openai": map[string]any{
				"monthly": 20,
			},
		},
	}
	rawBody, err := json.Marshal(putBody)
	require.NoError(t, err)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/settings", bytes.NewReader(rawBody))
	c.Request.Header.Set("Content-Type", "application/json")
	handler.UpdateSettings(c)
	require.Equal(t, http.StatusOK, rec.Code)

	// 验证 DB 中写入了 JSON key
	jsonKey := identity.SettingKeyAuthSourcePlatformQuotas("email")
	require.NotEmpty(t, repo.values[jsonKey], "expected JSON key to be written to DB")

	// GET：验证响应中 auth_source_default_email_platform_quotas.openai.monthly = 20
	rec2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(rec2)
	c2.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/settings", nil)
	handler.GetSettings(c2)
	require.Equal(t, http.StatusOK, rec2.Code)

	var resp response.Response
	require.NoError(t, json.Unmarshal(rec2.Body.Bytes(), &resp))
	data, ok := resp.Data.(map[string]any)
	require.True(t, ok)

	emailPQ, ok := data["auth_source_default_email_platform_quotas"].(map[string]any)
	require.True(t, ok, "expected auth_source_default_email_platform_quotas to be a map")
	openaiPQ, ok := emailPQ["openai"].(map[string]any)
	require.True(t, ok, "expected openai entry in email platform quotas")
	monthly, ok := openaiPQ["monthly"].(float64)
	require.True(t, ok, "expected monthly to be float64")
	require.Equal(t, float64(20), monthly, "expected openai monthly=20")
}
