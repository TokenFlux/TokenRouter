package httpapi

import (
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// 已取得槽位不再抢占或绑定粘性，且重复清理只释放一次。
func TestSelectedAccountSlotOwnsOnlyAcquiredResource(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	var released atomic.Int32
	acquired := 0
	hooks := AccountSlotHooks{Acquired: func(*gin.Context) { acquired++ }, CapacityLimited: func(*gin.Context) { t.Fatal("unexpected capacity failure") }}
	started := false
	release, ok := AcquireSelectedAccountSlot(c, nil, "session", &SelectedAccountSlot{AccountID: 12, Acquired: true, ReleaseFunc: func() { released.Add(1) }}, false, &started, zap.NewNop(), func(int, string, string, string) { t.Fatal("unexpected HTTP error") }, nil, nil, hooks)
	require.True(t, ok)
	require.Equal(t, 1, acquired)
	require.Zero(t, released.Load())
	release()
	release()
	require.EqualValues(t, 1, released.Load())
}
func TestSelectedAccountSlotMissingWaitPlanKeepsErrorShape(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	limited := false
	status := 0
	kind, message := "", ""
	started := false
	release, ok := AcquireSelectedAccountSlot(c, nil, "", &SelectedAccountSlot{AccountID: 12}, false, &started, zap.NewNop(), func(s int, k, code, m string) { status = s; kind = k; message = m; require.Empty(t, code) }, nil, nil, AccountSlotHooks{Acquired: func(*gin.Context) { t.Fatal("unexpected acquired") }, CapacityLimited: func(*gin.Context) { limited = true }})
	require.False(t, ok)
	require.Nil(t, release)
	require.True(t, limited)
	require.Equal(t, 503, status)
	require.Equal(t, "api_error", kind)
	require.Equal(t, "No available accounts", message)
}
