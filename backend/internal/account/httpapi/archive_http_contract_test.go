package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	idempotencytest "github.com/TokenFlux/TokenRouter/internal/idempotency/testkit"

	"github.com/TokenFlux/TokenRouter/internal/idempotency"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"time"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/account/transfer"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type dataResponse struct {
	Code int         `json:"code"`
	Data dataPayload `json:"data"`
}

type dataPayload struct {
	Type           string        `json:"type"`
	Version        int           `json:"version"`
	Proxies        []dataProxy   `json:"proxies"`
	Accounts       []dataAccount `json:"accounts"`
	SkippedShadows int           `json:"skipped_shadows"`
}

type dataProxy struct {
	ProxyKey string `json:"proxy_key"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	Status   string `json:"status"`
}

type dataAccount struct {
	Name        string         `json:"name"`
	Platform    string         `json:"platform"`
	Type        string         `json:"type"`
	Credentials map[string]any `json:"credentials"`
	Extra       map[string]any `json:"extra"`
	ProxyKey    *string        `json:"proxy_key"`
	Concurrency int            `json:"concurrency"`
	Priority    int            `json:"priority"`
}

func setupAccountDataRouter() (*gin.Engine, *archiveHTTPFixture) {
	return setupAccountDataRouterWithSettings(nil)
}

func setupAccountDataRouterWithSettings(settingService *accountcore.RuntimeSettings) (*gin.Engine, *archiveHTTPFixture) {

	router := gin.New()
	adminSvc := newArchiveHTTPFixture()

	options := accountcore.ArchiveOptions{Now: time.Now, DecodeIDToken: accountprovider.DecodeArchiveIDToken}
	if settingService != nil {
		options.Defaults = settingService.GetOpenAIOAuthImportDefaults
	}
	core := accountcore.NewArchive(adminSvc, egress.NewProxyTransfer(adminSvc, nil, time.Now), options)
	h := NewArchiveHandler(core)

	router.GET("/api/v1/admin/accounts/data", h.ExportData)
	router.POST("/api/v1/admin/accounts/data", h.ImportData)
	return router, adminSvc
}

func TestExportDataIncludesSecrets(t *testing.T) {
	router, adminSvc := setupAccountDataRouter()

	proxyID := int64(11)
	adminSvc.proxies = []egress.Proxy{
		{
			ID:       proxyID,
			Name:     "proxy",
			Protocol: "http",
			Host:     "127.0.0.1",
			Port:     8080,
			Username: "user",
			Password: "pass",
			Status:   billing.StatusActive,
		},
		{
			ID:       12,
			Name:     "orphan",
			Protocol: "https",
			Host:     "10.0.0.1",
			Port:     443,
			Username: "o",
			Password: "p",
			Status:   billing.StatusActive,
		},
	}
	adminSvc.accounts = []accountcore.Record{
		{
			ID:          21,
			Name:        "account",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.AccountTypeOAuth,
			Credentials: map[string]any{"token": "secret"},
			Extra:       map[string]any{"note": "x"},
			ProxyID:     &proxyID,
			Concurrency: 3,
			Priority:    50,
			Status:      billing.StatusDisabled,
		},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/data", nil)
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp dataResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.Empty(t, resp.Data.Type)
	require.Equal(t, 0, resp.Data.Version)
	require.Len(t, resp.Data.Proxies, 1)
	require.Equal(t, "pass", resp.Data.Proxies[0].Password)
	require.Len(t, resp.Data.Accounts, 1)
	require.Equal(t, "secret", resp.Data.Accounts[0].Credentials["token"])
}

func TestExportDataWithoutProxies(t *testing.T) {
	router, adminSvc := setupAccountDataRouter()

	proxyID := int64(11)
	adminSvc.proxies = []egress.Proxy{
		{
			ID:       proxyID,
			Name:     "proxy",
			Protocol: "http",
			Host:     "127.0.0.1",
			Port:     8080,
			Username: "user",
			Password: "pass",
			Status:   billing.StatusActive,
		},
	}
	adminSvc.accounts = []accountcore.Record{
		{
			ID:          21,
			Name:        "account",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.AccountTypeOAuth,
			Credentials: map[string]any{"token": "secret"},
			ProxyID:     &proxyID,
			Concurrency: 3,
			Priority:    50,
			Status:      billing.StatusDisabled,
		},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/data?include_proxies=false", nil)
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp dataResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.Len(t, resp.Data.Proxies, 0)
	require.Len(t, resp.Data.Accounts, 1)
	require.Nil(t, resp.Data.Accounts[0].ProxyKey)
}

// TestExportDataExcludesSparkShadow 验证外审第5轮 P1/P2:导出时排除 spark 影子账号
// (影子无凭据、导入侧强制 credentials 非空,混入会产出无法还原的坏备份),并透出跳过计数。
func TestExportDataExcludesSparkShadow(t *testing.T) {
	router, adminSvc := setupAccountDataRouter()

	parentID := int64(21)
	adminSvc.accounts = []accountcore.Record{
		{
			ID:          parentID,
			Name:        "mother",
			Platform:    capability.PlatformOpenAI,
			Type:        capability.AccountTypeOAuth,
			Credentials: map[string]any{"token": "secret"},
			Status:      billing.StatusActive,
		},
		{
			ID:              22,
			Name:            "mother (Spark)",
			Platform:        capability.PlatformOpenAI,
			Type:            capability.AccountTypeOAuth,
			Credentials:     map[string]any{}, // 影子恒空凭据
			ParentAccountID: &parentID,        // 影子标记
			QuotaDimension:  accountcore.QuotaDimensionSpark,
			Status:          billing.StatusActive,
		},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/accounts/data?include_proxies=false", nil)
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp dataResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.Len(t, resp.Data.Accounts, 1, "影子应被排除,仅导出母账号")
	require.Equal(t, "mother", resp.Data.Accounts[0].Name)
	require.Equal(t, 1, resp.Data.SkippedShadows, "跳过的影子数量应透出")
}

func TestExportDataPassesAccountFiltersAndSort(t *testing.T) {
	router, adminSvc := setupAccountDataRouter()
	adminSvc.accounts = []accountcore.Record{
		{ID: 1, Name: "acc-1", Status: billing.StatusActive},
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/admin/accounts/data?platform=openai&type=oauth&status=active&group=12&privacy_mode=blocked&search=keyword&sort_by=priority&sort_order=desc",
		nil,
	)
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	require.Equal(t, 1, adminSvc.list.lastListAccounts.calls)
	require.Equal(t, "openai", adminSvc.list.lastListAccounts.platform)
	require.Equal(t, "oauth", adminSvc.list.lastListAccounts.accountType)
	require.Equal(t, "active", adminSvc.list.lastListAccounts.status)
	require.Equal(t, int64(12), adminSvc.list.lastListAccounts.groupID)
	require.Equal(t, "blocked", adminSvc.list.lastListAccounts.privacyMode)
	require.Equal(t, "keyword", adminSvc.list.lastListAccounts.search)
	require.Equal(t, "priority", adminSvc.list.lastListAccounts.sortBy)
	require.Equal(t, "desc", adminSvc.list.lastListAccounts.sortOrder)
}

func TestExportDataSelectedIDsOverrideFilters(t *testing.T) {
	router, adminSvc := setupAccountDataRouter()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/admin/accounts/data?ids=1,2&platform=openai&search=keyword&sort_by=priority&sort_order=desc",
		nil,
	)
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	var resp dataResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.Len(t, resp.Data.Accounts, 2)
	require.Equal(t, 0, adminSvc.list.lastListAccounts.calls)
}

func TestImportDataReusesProxyAndSkipsDefaultGroup(t *testing.T) {
	router, adminSvc := setupAccountDataRouter()

	adminSvc.proxies = []egress.Proxy{
		{
			ID:       1,
			Name:     "proxy",
			Protocol: "socks5",
			Host:     "1.2.3.4",
			Port:     1080,
			Username: "u",
			Password: "p",
			Status:   billing.StatusActive,
		},
	}

	dataPayload := map[string]any{
		"data": map[string]any{
			"type":    transfer.DataType,
			"version": transfer.DataVersion,
			"proxies": []map[string]any{
				{
					"proxy_key": "socks5|1.2.3.4|1080|u|p",
					"name":      "proxy",
					"protocol":  "socks5",
					"host":      "1.2.3.4",
					"port":      1080,
					"username":  "u",
					"password":  "p",
					"status":    "active",
				},
			},
			"accounts": []map[string]any{
				{
					"name":        "acc",
					"platform":    capability.PlatformOpenAI,
					"type":        capability.AccountTypeOAuth,
					"credentials": map[string]any{"token": "x"},
					"proxy_key":   "socks5|1.2.3.4|1080|u|p",
					"concurrency": 3,
					"priority":    50,
				},
			},
		},
		"skip_default_group_bind": true,
	}

	body, _ := json.Marshal(dataPayload)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/data", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	require.Len(t, adminSvc.createdProxies, 0)
	require.Len(t, adminSvc.createdAccounts, 1)
	require.True(t, adminSvc.createdAccounts[0].SkipDefaultGroupBind)
}

func TestImportDataIdempotencyIgnoresDeprecatedLongContextBillingExtra(t *testing.T) {
	const deprecatedKey = "openai_long_context_billing_enabled"
	previousCoordinator := idempotency.DefaultIdempotencyCoordinator()
	idempotency.SetDefaultIdempotencyCoordinator(idempotency.NewIdempotencyCoordinator(
		idempotencytest.NewMemoryStore(),
		idempotency.DefaultIdempotencyConfig(),
	))
	t.Cleanup(func() {
		idempotency.SetDefaultIdempotencyCoordinator(previousCoordinator)
	})

	router, adminSvc := setupAccountDataRouter()
	call := func(extra map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		account := map[string]any{
			"name":        "openai-oauth",
			"platform":    capability.PlatformOpenAI,
			"type":        capability.AccountTypeOAuth,
			"credentials": map[string]any{"access_token": "token"},
			"extra":       extra,
		}
		payload := map[string]any{
			"data": map[string]any{
				"type":     transfer.DataType,
				"version":  transfer.DataVersion,
				"proxies":  []map[string]any{},
				"accounts": []map[string]any{account},
			},
			"skip_default_group_bind": true,
		}
		body, err := json.Marshal(payload)
		require.NoError(t, err)
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/data", bytes.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", "import-deprecated-long-context")
		router.ServeHTTP(recorder, request)
		return recorder
	}

	first := call(map[string]any{
		deprecatedKey: false,
		"preserved":   "value",
	})
	second := call(map[string]any{"preserved": "value"})
	third := call(map[string]any{
		deprecatedKey: map[string]any{"malformed": true},
		"preserved":   "value",
	})

	require.Equal(t, http.StatusOK, first.Code, first.Body.String())
	require.Equal(t, http.StatusOK, second.Code, second.Body.String())
	require.Equal(t, http.StatusOK, third.Code, third.Body.String())
	require.Empty(t, first.Header().Get("X-Idempotency-Replayed"))
	require.Equal(t, "true", second.Header().Get("X-Idempotency-Replayed"))
	require.Equal(t, "true", third.Header().Get("X-Idempotency-Replayed"))
	require.Len(t, adminSvc.createdAccounts, 1)
	require.NotContains(t, adminSvc.createdAccounts[0].Extra, deprecatedKey)
	require.Equal(t, "value", adminSvc.createdAccounts[0].Extra["preserved"])
}

func TestImportDataAppliesOpenAIOAuthDefaultModelWhitelistWhenMissing(t *testing.T) {
	settingSvc := accountcore.NewRuntimeSettings(&archiveSettingFixture{}, nil)
	router, adminSvc := setupAccountDataRouterWithSettings(settingSvc)

	postImportAccount(t, router, map[string]any{
		"name":        "openai-oauth",
		"platform":    capability.PlatformOpenAI,
		"type":        capability.AccountTypeOAuth,
		"credentials": map[string]any{"access_token": "token"},
	})

	require.Len(t, adminSvc.createdAccounts, 1)
	require.Equal(t, []string{
		"gpt-5.2",
		"gpt-5.3",
		"gpt-5.3-spark",
		"gpt-5.4",
		"gpt-5.4-mini",
		"gpt-5.5",
	}, adminSvc.createdAccounts[0].Credentials["model_whitelist"])
}

func TestImportDataKeepsExistingOpenAIOAuthModelWhitelist(t *testing.T) {
	settingSvc := accountcore.NewRuntimeSettings(&archiveSettingFixture{}, nil)
	router, adminSvc := setupAccountDataRouterWithSettings(settingSvc)

	postImportAccount(t, router, map[string]any{
		"name":     "openai-oauth",
		"platform": capability.PlatformOpenAI,
		"type":     capability.AccountTypeOAuth,
		"credentials": map[string]any{
			"access_token":    "token",
			"model_whitelist": []any{},
		},
	})

	require.Len(t, adminSvc.createdAccounts, 1)
	require.Equal(t, []any{}, adminSvc.createdAccounts[0].Credentials["model_whitelist"])
}

func TestImportDataTreatsOpenAIOAuthNullAccountFieldAsPresent(t *testing.T) {
	repo := &archiveSettingFixture{values: map[string]string{
		accountcore.SettingKeyOpenAIOAuthImportDefaults: `{"account":{"concurrency":7}}`,
	}}
	settingSvc := accountcore.NewRuntimeSettings(repo, nil)
	router, adminSvc := setupAccountDataRouterWithSettings(settingSvc)

	postImportAccount(t, router, map[string]any{
		"name":        "openai-oauth",
		"platform":    capability.PlatformOpenAI,
		"type":        capability.AccountTypeOAuth,
		"credentials": map[string]any{"access_token": "token"},
		"concurrency": nil,
	})

	require.Len(t, adminSvc.createdAccounts, 1)
	require.Equal(t, 0, adminSvc.createdAccounts[0].Concurrency)
}

func TestImportDataDoesNotApplyOpenAIOAuthDefaultsToOtherPlatforms(t *testing.T) {
	settingSvc := accountcore.NewRuntimeSettings(&archiveSettingFixture{}, nil)
	router, adminSvc := setupAccountDataRouterWithSettings(settingSvc)

	postImportAccount(t, router, map[string]any{
		"name":        "anthropic-oauth",
		"platform":    capability.PlatformAnthropic,
		"type":        capability.AccountTypeOAuth,
		"credentials": map[string]any{"access_token": "token"},
	})

	require.Len(t, adminSvc.createdAccounts, 1)
	_, exists := adminSvc.createdAccounts[0].Credentials["model_whitelist"]
	require.False(t, exists)
}

func TestImportDataAcceptsQoderCosyAccount(t *testing.T) {
	router, adminSvc := setupAccountDataRouter()

	postImportAccount(t, router, map[string]any{
		"name":     "qoder-cosy",
		"platform": capability.PlatformQoder,
		"type":     capability.AccountTypeCosy,
		"credentials": map[string]any{
			"pat": "pat-123",
		},
	})

	require.Len(t, adminSvc.createdAccounts, 1)
	require.Equal(t, capability.PlatformQoder, adminSvc.createdAccounts[0].Platform)
	require.Equal(t, capability.AccountTypeCosy, adminSvc.createdAccounts[0].Type)
	require.Equal(t, "pat-123", adminSvc.createdAccounts[0].Credentials["pat"])
}

func TestImportDataRejectsQoderNonCosyAccount(t *testing.T) {
	router, adminSvc := setupAccountDataRouter()

	rec := postImportAccountRaw(t, router, map[string]any{
		"name":        "qoder-apikey",
		"platform":    capability.PlatformQoder,
		"type":        capability.AccountTypeAPIKey,
		"credentials": map[string]any{"api_key": "key"},
	})

	require.Equal(t, http.StatusOK, rec.Code)
	require.Empty(t, adminSvc.createdAccounts)
	result := decodeImportResult(t, rec)
	require.Equal(t, 1, result.Data.AccountFailed)
	require.Contains(t, rec.Body.String(), "qoder accounts require cosy")
}

func TestImportDataRejectsCosyNonQoderAccount(t *testing.T) {
	router, adminSvc := setupAccountDataRouter()

	rec := postImportAccountRaw(t, router, map[string]any{
		"name":        "anthropic-cosy",
		"platform":    capability.PlatformAnthropic,
		"type":        capability.AccountTypeCosy,
		"credentials": map[string]any{"pat": "pat-123"},
	})

	require.Equal(t, http.StatusOK, rec.Code)
	require.Empty(t, adminSvc.createdAccounts)
	result := decodeImportResult(t, rec)
	require.Equal(t, 1, result.Data.AccountFailed)
	require.Contains(t, rec.Body.String(), "cosy account type requires qoder platform")
}

func decodeImportResult(t *testing.T, rec *httptest.ResponseRecorder) struct {
	Code int                       `json:"code"`
	Data transfer.DataImportResult `json:"data"`
} {
	t.Helper()
	var result struct {
		Code int                       `json:"code"`
		Data transfer.DataImportResult `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
	return result
}

func postImportAccount(t *testing.T, router *gin.Engine, account map[string]any) {
	t.Helper()

	rec := postImportAccountRaw(t, router, account)
	require.Equal(t, http.StatusOK, rec.Code)
}

func postImportAccountRaw(t *testing.T, router *gin.Engine, account map[string]any) *httptest.ResponseRecorder {
	t.Helper()

	dataPayload := map[string]any{
		"data": map[string]any{
			"type":     transfer.DataType,
			"version":  transfer.DataVersion,
			"proxies":  []map[string]any{},
			"accounts": []map[string]any{account},
		},
		"skip_default_group_bind": true,
	}

	body, _ := json.Marshal(dataPayload)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/accounts/data", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	return rec
}
