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

// 团队查询沿用服务端时区，不因携带用户 timezone 参数改变范围。
func TestTeamUsageQueryInjectedCalendar(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/?from=2026-03-08&to=2026-03-08&timezone=UTC&member_id=12&api_key_id=34", nil)
	query, err := parseTeamUsageQuery(c, timezone.NewCalendar(loc))
	require.NoError(t, err)
	require.Equal(t, time.Date(2026, 3, 8, 0, 0, 0, 0, loc), query.From)
	require.Equal(t, time.Date(2026, 3, 9, 0, 0, 0, 0, loc), query.To)
	require.Equal(t, 23*time.Hour, query.To.Sub(query.From))
	require.EqualValues(t, 12, *query.ActorUserID)
	require.EqualValues(t, 34, *query.APIKeyID)
}
