package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 返利查询保持包含结束日最后一纳秒的旧契约，不统一为其他查询的排他边界。
func TestAffiliateRecordFilterInjectedCalendar(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/?start_at=2026-11-01&end_at=2026-11-01&timezone=invalid-zone", nil)
	filter := parseAffiliateRecordFilter(c, 2, 200, timezone.NewCalendar(loc))
	require.Equal(t, 2, filter.Page)
	require.Equal(t, 100, filter.PageSize)
	require.NotNil(t, filter.StartAt)
	require.NotNil(t, filter.EndAt)
	require.Equal(t, time.Date(2026, 11, 1, 0, 0, 0, 0, loc), *filter.StartAt)
	require.Equal(t, time.Date(2026, 11, 2, 0, 0, 0, 0, loc).Add(-time.Nanosecond), *filter.EndAt)
	require.Equal(t, 25*time.Hour-time.Nanosecond, filter.EndAt.Sub(*filter.StartAt))
}
