package httpapi

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestParseOpsViewParam(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/?view=excluded", nil)
	require.Equal(t, opsListViewExcluded, parseOpsViewParam(c))

	c2, _ := gin.CreateTestContext(w)
	c2.Request = httptest.NewRequest(http.MethodGet, "/?view=all", nil)
	require.Equal(t, opsListViewAll, parseOpsViewParam(c2))

	c3, _ := gin.CreateTestContext(w)
	c3.Request = httptest.NewRequest(http.MethodGet, "/?view=unknown", nil)
	require.Equal(t, opsListViewErrors, parseOpsViewParam(c3))

	require.Equal(t, "", parseOpsViewParam(nil))
}
func TestParseOpsDuration(t *testing.T) {
	dur, ok := parseOpsDuration("1h")
	require.True(t, ok)
	require.Equal(t, time.Hour, dur)

	_, ok = parseOpsDuration("invalid")
	require.False(t, ok)
}
func TestParseOpsTokenStatsDuration(t *testing.T) {
	tests := []struct {
		input string
		want  time.Duration
		ok    bool
	}{
		{input: "30m", want: 30 * time.Minute, ok: true},
		{input: "1h", want: time.Hour, ok: true},
		{input: "1d", want: 24 * time.Hour, ok: true},
		{input: "15d", want: 15 * 24 * time.Hour, ok: true},
		{input: "30d", want: 30 * 24 * time.Hour, ok: true},
		{input: "7d", want: 0, ok: false},
	}

	for _, tt := range tests {
		got, ok := parseOpsTokenStatsDuration(tt.input)
		require.Equal(t, tt.ok, ok, "input=%s", tt.input)
		require.Equal(t, tt.want, got, "input=%s", tt.input)
	}
}
func TestParseOpsTokenStatsFilter_Defaults(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	before := time.Now().UTC()
	filter, err := parseOpsTokenStatsFilter(c)
	after := time.Now().UTC()

	require.NoError(t, err)
	require.NotNil(t, filter)
	require.Equal(t, "30d", filter.TimeRange)
	require.Equal(t, 1, filter.Page)
	require.Equal(t, 20, filter.PageSize)
	require.Equal(t, 0, filter.TopN)
	require.Nil(t, filter.GroupID)
	require.Equal(t, "", filter.Platform)
	require.True(t, filter.StartTime.Before(filter.EndTime))
	require.WithinDuration(t, before.Add(-30*24*time.Hour), filter.StartTime, 2*time.Second)
	require.WithinDuration(t, after, filter.EndTime, 2*time.Second)
}
func TestParseOpsTokenStatsFilter_WithTopN(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(
		http.MethodGet,
		"/?time_range=1h&platform=anthropic&group_id=12&top_n=50",
		nil,
	)

	filter, err := parseOpsTokenStatsFilter(c)
	require.NoError(t, err)
	require.Equal(t, "1h", filter.TimeRange)
	require.Equal(t, "anthropic", filter.Platform)
	require.NotNil(t, filter.GroupID)
	require.Equal(t, int64(12), *filter.GroupID)
	require.Equal(t, 50, filter.TopN)
	require.Equal(t, 0, filter.Page)
	require.Equal(t, 0, filter.PageSize)
}
func TestParseOpsTokenStatsFilter_InvalidParams(t *testing.T) {
	tests := []string{
		"/?time_range=7d",
		"/?group_id=0",
		"/?group_id=abc",
		"/?top_n=0",
		"/?top_n=101",
		"/?top_n=10&page=1",
		"/?top_n=10&page_size=20",
		"/?page=0",
		"/?page_size=0",
		"/?page_size=101",
	}

	gin.SetMode(gin.TestMode)
	for _, rawURL := range tests {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, rawURL, nil)

		_, err := parseOpsTokenStatsFilter(c)
		require.Error(t, err, "url=%s", rawURL)
	}
}
func TestParseOpsTimeRange(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	now := time.Now().UTC()
	startStr := now.Add(-time.Hour).Format(time.RFC3339)
	endStr := now.Format(time.RFC3339)
	c.Request = httptest.NewRequest(http.MethodGet, "/?start_time="+startStr+"&end_time="+endStr, nil)
	start, end, err := parseOpsTimeRange(c, "1h")
	require.NoError(t, err)
	require.True(t, start.Before(end))

	c2, _ := gin.CreateTestContext(w)
	c2.Request = httptest.NewRequest(http.MethodGet, "/?start_time=bad", nil)
	_, _, err = parseOpsTimeRange(c2, "1h")
	require.Error(t, err)
}
func TestParseOpsLatencyBucketBoundaries(t *testing.T) {
	defaults, err := parseOpsLatencyBucketBoundaries("")
	require.NoError(t, err)
	require.Equal(t, []int64{100, 200, 500, 1000, 2000}, defaults)

	custom, err := parseOpsLatencyBucketBoundaries("1000, 2000,5000,10000,20000")
	require.NoError(t, err)
	require.Equal(t, []int64{1000, 2000, 5000, 10000, 20000}, custom)

	invalid := []string{
		"100,200",
		"0,200,500,1000,2000",
		"100,200,200,1000,2000",
		"100,200,500,1000,86400001",
		"100,abc,500,1000,2000",
	}
	for _, raw := range invalid {
		_, err := parseOpsLatencyBucketBoundaries(raw)
		require.Error(t, err, "raw=%s", raw)
	}
}
func TestParseOpsRealtimeWindow(t *testing.T) {
	dur, label, ok := parseOpsRealtimeWindow("5m")
	require.True(t, ok)
	require.Equal(t, 5*time.Minute, dur)
	require.Equal(t, "5min", label)

	_, _, ok = parseOpsRealtimeWindow("invalid")
	require.False(t, ok)
}
func TestOpsWSHelpers(t *testing.T) {
	prefixes, invalid := parseTrustedProxyList("10.0.0.0/8,invalid")
	require.Len(t, prefixes, 1)
	require.Len(t, invalid, 1)

	host := hostWithoutPort("example.com:443")
	require.Equal(t, "example.com", host)

	addr := netip.MustParseAddr("10.0.0.1")
	require.True(t, isAddrInTrustedProxies(addr, prefixes))
	require.False(t, isAddrInTrustedProxies(netip.MustParseAddr("192.168.0.1"), prefixes))
}
