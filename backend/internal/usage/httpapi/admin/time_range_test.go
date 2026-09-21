package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestParseTimeRange(t *testing.T) {

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, "/?start_date=2024-01-01&end_date=2024-01-02&timezone=UTC", nil)
	c.Request = req

	start, end := parseTimeRange(c, timezone.NewCalendar(time.Local))
	require.Equal(t, time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), start)
	require.Equal(t, time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC), end)

	req = httptest.NewRequest(http.MethodGet, "/?start_date=bad&timezone=UTC", nil)
	c.Request = req
	start, end = parseTimeRange(c, timezone.NewCalendar(time.Local))
	require.False(t, start.IsZero())
	require.False(t, end.IsZero())
}

// 管理查询采用注入的服务端时区，并保留用户覆盖与日历日结束边界。
func TestParseTimeRangeInjectedCalendar(t *testing.T) {
	newYork, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	calendar := timezone.NewCalendar(newYork)
	for _, tc := range []struct {
		name     string
		query    string
		location *time.Location
		duration time.Duration
	}{
		{name: "server_default", location: newYork, duration: 23 * time.Hour},
		{name: "invalid_user_fallback", query: "&timezone=invalid-zone", location: newYork, duration: 23 * time.Hour},
		{name: "user_override", query: "&timezone=Asia%2FShanghai", location: shanghai, duration: 24 * time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, "/?start_date=2026-03-08&end_date=2026-03-08"+tc.query, nil)
			start, end := parseTimeRange(c, calendar)
			require.Equal(t, time.Date(2026, 3, 8, 0, 0, 0, 0, tc.location), start)
			require.Equal(t, time.Date(2026, 3, 9, 0, 0, 0, 0, tc.location), end)
			require.Equal(t, tc.duration, end.Sub(start))
		})
	}
}
