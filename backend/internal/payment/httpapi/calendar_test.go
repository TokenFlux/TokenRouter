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

// 支付查询继续要求完整有效的范围，同时使用显式服务端时区处理日期输入。
func TestPaymentDashboardRangeInjectedCalendar(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	calendar := timezone.NewCalendar(loc)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/?start_date=2026-03-08&end_date=2026-03-08", nil)
	start, end, ok := parsePaymentDashboardRange(c, calendar)
	require.True(t, ok)
	require.Equal(t, time.Date(2026, 3, 8, 0, 0, 0, 0, loc), start)
	require.Equal(t, time.Date(2026, 3, 9, 0, 0, 0, 0, loc), end)
	require.Equal(t, 23*time.Hour, end.Sub(start))

	for _, query := range []string{
		"start_date=2026-03-08",
		"start_date=bad&end_date=2026-03-08",
		"start_date=2026-03-09&end_date=2026-03-08",
	} {
		t.Run(query, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/?"+query, nil)
			_, _, ok := parsePaymentDashboardRange(c, calendar)
			require.False(t, ok)
			require.Equal(t, http.StatusBadRequest, recorder.Code)
		})
	}
}
