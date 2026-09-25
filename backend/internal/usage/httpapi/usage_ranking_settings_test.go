package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"

	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/settings"

	"github.com/TokenFlux/TokenRouter/internal/usage"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type usageRankingSettingRepoStub struct {
	settings.Repository
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
	usage.UsageLogRepository
	called bool
	sortBy usage.UsageRankingSortBy
}

func (r *usageRankingRepoCapture) GetUsageRanking(_ context.Context, _, _ time.Time, _ int, sortBy usage.UsageRankingSortBy) (*usage.UsageRankingResponse, error) {
	r.called = true
	r.sortBy = sortBy
	return &usage.UsageRankingResponse{
		Ranking: []usage.UsageRankingItem{{
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

	settingSvc := usage.NewRuntimeSettings(&usageRankingSettingRepoStub{values: values})
	h := NewUsageHandler(usage.NewUsageService(repo), nil, nil, settingSvc, timezone.NewCalendar(time.Local))
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 42})
		c.Next()
	})
	router.GET("/usage/ranking", h.Ranking)
	return router
}

func TestUsageRankingDisabledRejectsBeforeQuery(t *testing.T) {
	repo := &usageRankingRepoCapture{}
	router := newUsageRankingSettingsRouter(repo, map[string]string{
		usage.SettingKeyUsageRankingEnabled: "false",
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/usage/ranking", nil))

	require.Equal(t, http.StatusForbidden, recorder.Code)
	require.False(t, repo.called)
}

func TestUsageRankingProjectsHiddenFieldsAndUsesConfiguredSort(t *testing.T) {
	repo := &usageRankingRepoCapture{}
	router := newUsageRankingSettingsRouter(repo, map[string]string{
		usage.SettingKeyUsageRankingEnabled:         "true",
		usage.SettingKeyUsageRankingSortBy:          string(usage.UsageRankingSortByRequests),
		usage.SettingKeyUsageRankingShowTotalTokens: "false",
		usage.SettingKeyUsageRankingShowRequests:    "false",
		usage.SettingKeyUsageRankingShowActualCost:  "false",
		usage.SettingKeyUsageRankingLimit:           "12",
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/usage/ranking", nil))

	require.Equal(t, http.StatusOK, recorder.Code)
	require.True(t, repo.called)
	require.Equal(t, usage.UsageRankingSortByRequests, repo.sortBy)

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
