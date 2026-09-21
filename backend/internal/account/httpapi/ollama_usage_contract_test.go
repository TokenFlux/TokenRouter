package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type ollamaCloudUsageHandlerTestRepo struct {
	account           *accountcore.Record
	accounts          []*accountcore.Record
	groupResolveCalls int
}

func (r *ollamaCloudUsageHandlerTestRepo) GetByID(_ context.Context, id int64) (*accountcore.Record, error) {
	if r.account != nil && r.account.ID == id {
		return r.account, nil
	}
	for _, account := range r.accounts {
		if account.ID == id {
			return account, nil
		}
	}
	return nil, accountcore.ErrAccountNotFound
}

func (r *ollamaCloudUsageHandlerTestRepo) ListOllamaCloudUsageGroupAccounts(_ context.Context, _ []*accountcore.Record) ([]accountcore.Record, error) {
	r.groupResolveCalls++
	result := make([]accountcore.Record, 0, len(r.accounts)+1)
	if r.account != nil {
		result = append(result, *r.account)
	}
	for _, account := range r.accounts {
		result = append(result, *account)
	}
	return result, nil
}

func (r *ollamaCloudUsageHandlerTestRepo) SaveOllamaCloudUsageSession(context.Context, *accountcore.Record, string, bool) error {
	return nil
}
func (r *ollamaCloudUsageHandlerTestRepo) DeleteOllamaCloudUsageSession(context.Context, *accountcore.Record) error {
	return nil
}
func (r *ollamaCloudUsageHandlerTestRepo) SetOllamaCloudUsageAutoRefresh(context.Context, *accountcore.Record, bool) error {
	return nil
}
func (r *ollamaCloudUsageHandlerTestRepo) UpdateOllamaCloudUsageSnapshot(context.Context, *accountcore.Record, *accountcore.OllamaCloudUsageSnapshot) error {
	return nil
}
func (r *ollamaCloudUsageHandlerTestRepo) DisableOllamaCloudUsageAutoRefresh(context.Context, *accountcore.Record) error {
	return nil
}
func (r *ollamaCloudUsageHandlerTestRepo) ListDueOllamaCloudUsageAccounts(context.Context, time.Time, time.Duration, time.Duration, int) ([]accountcore.Record, error) {
	return nil, nil
}

func newOllamaCloudUsageHandlerTestService(t *testing.T) *accountcore.OllamaCloudUsageService {
	t.Helper()
	svc := accountcore.NewOllamaCloudUsageService(nil, nil, nil, accountcore.OllamaUsageOptions{})
	t.Cleanup(svc.Stop)
	return svc
}

func newOllamaCloudUsageHandlerContext(method, target, body, id string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(method, target, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = request
	if id != "" {
		ctx.Params = gin.Params{{Key: "id", Value: id}}
	}
	return ctx, recorder
}

func TestOllamaCloudUsageHandlersValidateRequestsAndDependencies(t *testing.T) {
	svc := newOllamaCloudUsageHandlerTestService(t)

	t.Run("invalid account id", func(t *testing.T) {
		ctx, recorder := newOllamaCloudUsageHandlerContext(http.MethodGet, "/admin/accounts/not-an-id/ollama-cloud-usage", "", "not-an-id")
		NewOllamaUsageHandler(svc).GetOllamaCloudUsage(ctx)
		require.Equal(t, http.StatusBadRequest, recorder.Code)
	})

	t.Run("empty session", func(t *testing.T) {
		ctx, recorder := newOllamaCloudUsageHandlerContext(http.MethodPut, "/admin/accounts/7/ollama-cloud-usage/session", `{"session":""}`, "7")
		NewOllamaUsageHandler(svc).SaveOllamaCloudUsageSession(ctx)
		require.Equal(t, http.StatusBadRequest, recorder.Code)
	})

	t.Run("missing enabled", func(t *testing.T) {
		ctx, recorder := newOllamaCloudUsageHandlerContext(http.MethodPut, "/admin/accounts/7/ollama-cloud-usage/auto-refresh", `{}`, "7")
		NewOllamaUsageHandler(svc).SetOllamaCloudUsageAutoRefresh(ctx)
		require.Equal(t, http.StatusBadRequest, recorder.Code)
	})

	t.Run("service unavailable", func(t *testing.T) {
		ctx, recorder := newOllamaCloudUsageHandlerContext(http.MethodGet, "/admin/accounts/7/ollama-cloud-usage", "", "7")
		NewOllamaUsageHandler(nil).GetOllamaCloudUsage(ctx)
		require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
		require.Contains(t, recorder.Body.String(), "OLLAMA_CLOUD_USAGE_UNAVAILABLE")
	})
}

func TestOllamaCloudUsageEncryptionKeyStateConsistentAcrossAccountResponses(t *testing.T) {

	for _, configured := range []bool{false, true} {
		t.Run("configured="+strconv.FormatBool(configured), func(t *testing.T) {
			account := &accountcore.Record{
				ID:          7,
				Name:        "ollama",
				Platform:    capability.PlatformOpenAI,
				Type:        capability.AccountTypeAPIKey,
				Credentials: map[string]any{"base_url": "https://ollama.com", "api_key": "test-key"},
				Extra:       map[string]any{},
				Status:      accountcore.StatusActive,
			}
			adminService := &ollamaManagementFixture{}
			adminService.accounts = []accountcore.Record{*account}
			usageService := accountcore.NewOllamaCloudUsageService(
				&ollamaCloudUsageHandlerTestRepo{account: account}, nil, nil, accountcore.OllamaUsageOptions{EncryptionKeyConfigured: configured},
			)
			t.Cleanup(usageService.Stop)

			handler := newOllamaManagementHandler(adminService, usageService)
			router := gin.New()
			router.GET("/accounts", handler.List)
			router.GET("/accounts/:id", handler.GetByID)
			router.GET("/accounts/:id/ollama-cloud-usage", NewOllamaUsageHandler(usageService).GetOllamaCloudUsage)

			listRecorder := httptest.NewRecorder()
			router.ServeHTTP(listRecorder, httptest.NewRequest(http.MethodGet, "/accounts?page=1&page_size=20", nil))
			require.Equal(t, http.StatusOK, listRecorder.Code)
			var listPayload struct {
				Data struct {
					Items []struct {
						OllamaCloudUsage *accountcore.OllamaCloudUsageState `json:"ollama_cloud_usage"`
					} `json:"items"`
				} `json:"data"`
			}
			require.NoError(t, json.Unmarshal(listRecorder.Body.Bytes(), &listPayload))
			require.Len(t, listPayload.Data.Items, 1)
			require.NotNil(t, listPayload.Data.Items[0].OllamaCloudUsage)

			detailRecorder := httptest.NewRecorder()
			router.ServeHTTP(detailRecorder, httptest.NewRequest(http.MethodGet, "/accounts/7", nil))
			require.Equal(t, http.StatusOK, detailRecorder.Code)
			var detailPayload struct {
				Data struct {
					OllamaCloudUsage *accountcore.OllamaCloudUsageState `json:"ollama_cloud_usage"`
				} `json:"data"`
			}
			require.NoError(t, json.Unmarshal(detailRecorder.Body.Bytes(), &detailPayload))
			require.NotNil(t, detailPayload.Data.OllamaCloudUsage)

			stateRecorder := httptest.NewRecorder()
			router.ServeHTTP(stateRecorder, httptest.NewRequest(http.MethodGet, "/accounts/7/ollama-cloud-usage", nil))
			require.Equal(t, http.StatusOK, stateRecorder.Code)
			var statePayload struct {
				Data accountcore.OllamaCloudUsageState `json:"data"`
			}
			require.NoError(t, json.Unmarshal(stateRecorder.Body.Bytes(), &statePayload))

			listConfigured := listPayload.Data.Items[0].OllamaCloudUsage.EncryptionKeyConfigured
			detailConfigured := detailPayload.Data.OllamaCloudUsage.EncryptionKeyConfigured
			require.Equal(t, configured, listConfigured)
			require.Equal(t, statePayload.Data.EncryptionKeyConfigured, listConfigured)
			require.Equal(t, statePayload.Data.EncryptionKeyConfigured, detailConfigured)
		})
	}
}

func TestOllamaCloudUsageSharedStateMatchesListDetailAndSpecialEndpointWithoutListNPlusOne(t *testing.T) {
	now := time.Now().UTC()
	source := &accountcore.Record{
		ID: 7, Name: "source", Platform: capability.PlatformOpenAI, Type: capability.AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "https://ollama.com", "api_key": "shared-secret-key"},
		Extra: map[string]any{
			accountcore.OllamaCloudUsageSessionExtraKey:     "ciphertext-secret",
			accountcore.OllamaCloudUsageAutoRefreshExtraKey: true,
			accountcore.OllamaCloudUsageSnapshotExtraKey: &accountcore.OllamaCloudUsageSnapshot{
				Status: accountcore.OllamaCloudUsageStatusOK, Data: &accountcore.OllamaCloudUsageData{Plan: "pro"},
				LastAttemptAt: now, NextRefreshAt: now.Add(time.Hour),
			},
		},
		Status: accountcore.StatusActive,
	}
	sibling := &accountcore.Record{
		ID: 8, Name: "sibling", Platform: capability.PlatformAnthropic, Type: capability.AccountTypeAPIKey,
		Credentials: map[string]any{"base_url": "HTTPS://WWW.OLLAMA.COM:443/v1", "api_key": "shared-secret-key"},
		Extra:       map[string]any{}, Status: accountcore.StatusActive,
	}
	repo := &ollamaCloudUsageHandlerTestRepo{accounts: []*accountcore.Record{source, sibling}}
	adminService := &ollamaManagementFixture{}
	adminService.accounts = []accountcore.Record{*source, *sibling}
	usageService := accountcore.NewOllamaCloudUsageService(repo, nil, nil, accountcore.OllamaUsageOptions{EncryptionKeyConfigured: true})
	t.Cleanup(usageService.Stop)
	handler := newOllamaManagementHandler(adminService, usageService)
	router := gin.New()
	router.GET("/accounts", handler.List)
	router.GET("/accounts/:id", handler.GetByID)
	router.GET("/accounts/:id/ollama-cloud-usage", NewOllamaUsageHandler(usageService).GetOllamaCloudUsage)

	listRecorder := httptest.NewRecorder()
	router.ServeHTTP(listRecorder, httptest.NewRequest(http.MethodGet, "/accounts?page=1&page_size=20", nil))
	require.Equal(t, http.StatusOK, listRecorder.Code)
	require.Equal(t, 1, repo.groupResolveCalls, "the full list page must use one group-resolution batch")
	var listPayload struct {
		Data struct {
			Items []struct {
				ID               int64                              `json:"id"`
				OllamaCloudUsage *accountcore.OllamaCloudUsageState `json:"ollama_cloud_usage"`
			} `json:"items"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(listRecorder.Body.Bytes(), &listPayload))
	require.Len(t, listPayload.Data.Items, 2)
	for _, item := range listPayload.Data.Items {
		require.True(t, item.OllamaCloudUsage.Configured)
		require.Equal(t, "pro", item.OllamaCloudUsage.Snapshot.Data.Plan)
	}

	detailRecorder := httptest.NewRecorder()
	router.ServeHTTP(detailRecorder, httptest.NewRequest(http.MethodGet, "/accounts/8", nil))
	require.Equal(t, http.StatusOK, detailRecorder.Code)
	var detailPayload struct {
		Data struct {
			OllamaCloudUsage *accountcore.OllamaCloudUsageState `json:"ollama_cloud_usage"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(detailRecorder.Body.Bytes(), &detailPayload))

	stateRecorder := httptest.NewRecorder()
	router.ServeHTTP(stateRecorder, httptest.NewRequest(http.MethodGet, "/accounts/8/ollama-cloud-usage", nil))
	require.Equal(t, http.StatusOK, stateRecorder.Code)
	var statePayload struct {
		Data accountcore.OllamaCloudUsageState `json:"data"`
	}
	require.NoError(t, json.Unmarshal(stateRecorder.Body.Bytes(), &statePayload))
	require.Equal(t, statePayload.Data.Configured, detailPayload.Data.OllamaCloudUsage.Configured)
	require.Equal(t, statePayload.Data.Snapshot, detailPayload.Data.OllamaCloudUsage.Snapshot)
	for _, body := range []string{listRecorder.Body.String(), detailRecorder.Body.String(), stateRecorder.Body.String()} {
		require.NotContains(t, body, "shared-secret-key")
		require.NotContains(t, body, "ciphertext-secret")
	}
}

func TestGetOllamaCloudUsageSettingsHandlerSuccess(t *testing.T) {
	ctx, recorder := newOllamaCloudUsageHandlerContext(http.MethodGet, "/admin/accounts/ollama-cloud-usage/settings", "", "")
	handler := NewOllamaUsageHandler(newOllamaCloudUsageHandlerTestService(t))

	handler.GetOllamaCloudUsageSettings(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"enabled":false`)
	require.Contains(t, recorder.Body.String(), `"interval_minutes":60`)
	require.Contains(t, recorder.Body.String(), `"debounce_minutes":1`)
}
