package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/usagestats"
	middleware2 "github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type usageRankingSettingRepoStub struct {
	service.SettingRepository
	values map[string]string
}

func (s *usageRankingSettingRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := s.values[key]; ok {
			result[key] = value
		}
	}
	return result, nil
}

type usageRankingRepoCapture struct {
	service.UsageLogRepository
	called bool
	sortBy service.UsageRankingSortBy
}

func (r *usageRankingRepoCapture) GetUsageRanking(_ context.Context, _, _ time.Time, _ int, sortBy service.UsageRankingSortBy) (*usagestats.UsageRankingResponse, error) {
	r.called = true
	r.sortBy = sortBy
	return &usagestats.UsageRankingResponse{
		Ranking: []usagestats.UsageRankingItem{{
			Rank:                1,
			UserID:              7,
			DisplayName:         "ranked-user",
			Requests:            8,
			InputTokens:         100,
			OutputTokens:        200,
			CacheCreationTokens: 30,
			CacheReadTokens:     40,
			TotalTokens:         370,
			ActualCost:          12.5,
		}},
		TotalRequests:   8,
		TotalTokens:     370,
		TotalActualCost: 12.5,
	}, nil
}

func newUsageRankingSettingsRouter(repo *usageRankingRepoCapture, values map[string]string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	settingSvc := service.NewSettingService(&usageRankingSettingRepoStub{values: values}, nil)
	h := newLegacyUsageHandlerFixture(service.NewUsageService(repo), nil, nil, settingSvc)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 42})
		c.Next()
	})
	router.GET("/usage/ranking", h.Ranking)
	return router
}

func TestUsageRankingDisabledRejectsBeforeQuery(t *testing.T) {
	repo := &usageRankingRepoCapture{}
	router := newUsageRankingSettingsRouter(repo, map[string]string{
		service.SettingKeyUsageRankingEnabled: "false",
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/usage/ranking", nil))

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.False(t, repo.called)
}

func TestUsageRankingProjectsHiddenFieldsAndUsesConfiguredSort(t *testing.T) {
	repo := &usageRankingRepoCapture{}
	router := newUsageRankingSettingsRouter(repo, map[string]string{
		service.SettingKeyUsageRankingEnabled:         "true",
		service.SettingKeyUsageRankingSortBy:          string(service.UsageRankingSortByRequests),
		service.SettingKeyUsageRankingShowTotalTokens: "false",
		service.SettingKeyUsageRankingShowRequests:    "false",
		service.SettingKeyUsageRankingShowActualCost:  "false",
		service.SettingKeyUsageRankingLimit:           "12",
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/usage/ranking", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.True(t, repo.called)
	require.Equal(t, service.UsageRankingSortByRequests, repo.sortBy)

	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &envelope))
	var payload map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(envelope.Data, &payload))
	require.Contains(t, payload, "total_requests")
	require.NotContains(t, payload, "total_tokens")
	require.NotContains(t, payload, "total_actual_cost")
	require.Equal(t, json.RawMessage(`true`), payload["show_requests"])
	require.Equal(t, json.RawMessage(`false`), payload["show_total_tokens"])
	require.Equal(t, json.RawMessage(`false`), payload["show_actual_cost"])

	var rows []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(payload["ranking"], &rows))
	require.Len(t, rows, 1)
	require.Contains(t, rows[0], "requests")
	require.NotContains(t, rows[0], "total_tokens")
	require.NotContains(t, rows[0], "input_tokens")
	require.NotContains(t, rows[0], "output_tokens")
	require.NotContains(t, rows[0], "cache_creation_tokens")
	require.NotContains(t, rows[0], "cache_read_tokens")
	require.NotContains(t, rows[0], "actual_cost")
}
