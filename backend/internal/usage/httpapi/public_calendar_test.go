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

// 不同处理器独立持有时区，公开查询仍按日历日扩展结束日期。
func TestPublicUsageDateRangeInjectedCalendar(t *testing.T) {
	newYork, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	for _, loc := range []*time.Location{newYork, time.UTC} {
		t.Run(loc.String(), func(t *testing.T) {
			h := NewPublicUsageHandler(nil, nil, nil, nil, PublicUsageContext{}, timezone.NewCalendar(loc))
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, "/?start_date=2026-11-01&end_date=2026-11-01&timezone=invalid-zone", nil)
			start, end := h.parseUsageDateRange(c)
			require.Equal(t, time.Date(2026, 11, 1, 0, 0, 0, 0, loc), start)
			require.Equal(t, time.Date(2026, 11, 2, 0, 0, 0, 0, loc), end)
			if loc == newYork {
				require.Equal(t, 25*time.Hour, end.Sub(start))
			}
		})
	}
}

// 带明确偏移的时间不受服务端日界影响，也不额外增加一天。
func TestPublicUsageDateRangeExplicitOffset(t *testing.T) {
	h := NewPublicUsageHandler(nil, nil, nil, nil, PublicUsageContext{}, timezone.NewCalendar(time.UTC))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/?start_date=2026-03-08T01:00:00-05:00&end_date=2026-03-08T03:00:00-04:00", nil)
	start, end := h.parseUsageDateRange(c)
	require.Equal(t, time.Date(2026, 3, 8, 6, 0, 0, 0, time.UTC), start.UTC())
	require.Equal(t, time.Date(2026, 3, 8, 7, 0, 0, 0, time.UTC), end.UTC())
}
