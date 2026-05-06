package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/pkg/usagestats"
	middleware2 "github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type userUsageRepoCapture struct {
	service.UsageLogRepository
	listParams  pagination.PaginationParams
	listFilters usagestats.UsageLogFilters
}

func (s *userUsageRepoCapture) ListWithFilters(ctx context.Context, params pagination.PaginationParams, filters usagestats.UsageLogFilters) ([]service.UsageLog, *pagination.PaginationResult, error) {
	s.listParams = params
	s.listFilters = filters
	return []service.UsageLog{}, &pagination.PaginationResult{
		Total:    0,
		Page:     params.Page,
		PageSize: params.PageSize,
		Pages:    0,
	}, nil
}

func newUserUsageRequestTypeTestRouter(repo *userUsageRepoCapture) *gin.Engine {
	gin.SetMode(gin.TestMode)
	usageSvc := service.NewUsageService(repo)
	handler := NewUsageHandler(usageSvc, nil, nil)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(middleware2.ContextKeyUser), middleware2.AuthSubject{UserID: 42})
		c.Next()
	})
	router.GET("/usage", handler.List)
	return router
}

func TestUserUsageListRequestTypePriority(t *testing.T) {
	repo := &userUsageRepoCapture{}
	router := newUserUsageRequestTypeTestRouter(repo)

	req := httptest.NewRequest(http.MethodGet, "/usage?request_type=ws_v2&stream=bad", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, int64(42), repo.listFilters.UserID)
	require.NotNil(t, repo.listFilters.RequestType)
	require.Equal(t, int16(service.RequestTypeWSV2), *repo.listFilters.RequestType)
	require.Nil(t, repo.listFilters.Stream)
}

func TestUserUsageListInvalidRequestType(t *testing.T) {
	repo := &userUsageRepoCapture{}
	router := newUserUsageRequestTypeTestRouter(repo)

	req := httptest.NewRequest(http.MethodGet, "/usage?request_type=invalid", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUserUsageListInvalidStream(t *testing.T) {
	repo := &userUsageRepoCapture{}
	router := newUserUsageRequestTypeTestRouter(repo)

	req := httptest.NewRequest(http.MethodGet, "/usage?stream=invalid", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestParseUsageRankingTimeRangeDefaultsToToday(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/usage/ranking?timezone=Asia/Shanghai", nil)
	now := time.Date(2026, 5, 6, 15, 30, 0, 0, time.FixedZone("CST", 8*3600))

	start, end, err := parseUsageRankingTimeRange(c, now, "Asia/Shanghai")

	require.NoError(t, err)
	require.Equal(t, "2026-05-06 00:00:00 +0800 CST", start.String())
	require.Equal(t, "2026-05-07 00:00:00 +0800 CST", end.String())
}

func TestParseUsageRankingTimeRangeDateOnlyEndIsInclusive(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/usage/ranking?start_date=2026-05-01&end_date=2026-05-03&timezone=Asia/Shanghai", nil)
	now := time.Date(2026, 5, 6, 15, 30, 0, 0, time.UTC)

	start, end, err := parseUsageRankingTimeRange(c, now, "Asia/Shanghai")

	require.NoError(t, err)
	require.Equal(t, "2026-05-01 00:00:00 +0800 CST", start.String())
	require.Equal(t, "2026-05-04 00:00:00 +0800 CST", end.String())
	require.Equal(t, "2026-05-03", usageRankingDisplayEndDate(end))
}

func TestParseUsageRankingTimeRangeRejectsInvalidRange(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/usage/ranking?start_date=2026-05-03&end_date=2026-05-01&timezone=Asia/Shanghai", nil)
	now := time.Date(2026, 5, 6, 15, 30, 0, 0, time.UTC)

	_, _, err := parseUsageRankingTimeRange(c, now, "Asia/Shanghai")

	require.ErrorContains(t, err, "end_date must be later than start_date")
}
