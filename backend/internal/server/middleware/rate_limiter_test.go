package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	ippkg "github.com/TokenFlux/TokenRouter/internal/server/clientip"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// fakeFixedWindow 在 HTTP 契约处提供可控结果，不依赖 Redis 或可变全局钩子。
type fakeFixedWindow struct {
	counts  map[string]int64
	failure error
}

func (f *fakeFixedWindow) Allow(_ context.Context, key string, limit int, window time.Duration) (bool, int64, time.Duration, error) {
	if f.failure != nil {
		return false, 0, 0, f.failure
	}
	f.counts[key]++
	count := f.counts[key]
	if count > int64(limit) {
		return false, count, window, nil
	}
	return true, count, 0, nil
}

// TestRateLimiterFailureModes 保留默认故障放行及认证入口显式故障关闭。
func TestRateLimiterFailureModes(t *testing.T) {
	for _, mode := range []RateLimitFailureMode{RateLimitFailOpen, RateLimitFailClose} {
		limiter := NewRateLimiter(&fakeFixedWindow{failure: errors.New("redis unavailable")})
		router := gin.New()
		router.Use(limiter.LimitWithOptions("failure", 1, time.Second, RateLimitOptions{FailureMode: mode}))
		router.GET("/", func(c *gin.Context) {
			c.Status(http.StatusOK)
		})
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
		if mode == RateLimitFailClose {
			require.Equal(t, http.StatusTooManyRequests, w.Code)
		} else {
			require.Equal(t, http.StatusOK, w.Code)
		}
	}
}
func TestRateLimiterDifferentIPsIndependent(t *testing.T) {

	callCounts := make(map[string]int64)
	limiter := NewRateLimiter(&fakeFixedWindow{counts: callCounts})

	router := gin.New()
	router.Use(limiter.Limit("api", 1, time.Second))
	router.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	// 第一个 IP 的请求应通过
	req1 := httptest.NewRequest(http.MethodGet, "/test", nil)
	req1.RemoteAddr = "10.0.0.1:1234"
	rec1 := httptest.NewRecorder()
	router.ServeHTTP(rec1, req1)
	require.Equal(t, http.StatusOK, rec1.Code, "第一个 IP 的第一次请求应通过")

	// 第二个 IP 的请求应独立通过（不受第一个 IP 的计数影响）
	req2 := httptest.NewRequest(http.MethodGet, "/test", nil)
	req2.RemoteAddr = "10.0.0.2:5678"
	rec2 := httptest.NewRecorder()
	router.ServeHTTP(rec2, req2)
	require.Equal(t, http.StatusOK, rec2.Code, "第二个 IP 的第一次请求应独立通过")

	// 第一个 IP 的第二次请求应被限流
	req3 := httptest.NewRequest(http.MethodGet, "/test", nil)
	req3.RemoteAddr = "10.0.0.1:1234"
	rec3 := httptest.NewRecorder()
	router.ServeHTTP(rec3, req3)
	require.Equal(t, http.StatusTooManyRequests, rec3.Code, "第一个 IP 的第二次请求应被限流")
}

func TestRateLimiterHonorsForwardedIPSnapshot(t *testing.T) {

	callCounts := make(map[string]int64)
	limiter := NewRateLimiter(&fakeFixedWindow{counts: callCounts})

	router := gin.New()
	// 模拟 SessionBindingContext：开启转发 IP 兼容模式快照
	router.Use(func(c *gin.Context) {
		ippkg.SetForwardedIPSettings(c, true, nil)
		c.Next()
	})
	router.Use(limiter.Limit("fwd", 1, time.Second))
	router.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	send := func(xff string) int {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		// 所有请求都来自同一个反代地址
		req.RemoteAddr = "127.0.0.1:5678"
		req.Header.Set("X-Forwarded-For", xff)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Code
	}

	// 反代后两个不同的真实客户端应各自独立计数，不因共享代理地址被合并限流
	require.Equal(t, http.StatusOK, send("198.51.100.1"))
	require.Equal(t, http.StatusOK, send("198.51.100.2"))
	// 同一真实客户端第二次请求应被限流
	require.Equal(t, http.StatusTooManyRequests, send("198.51.100.1"))

	require.Contains(t, callCounts, "fwd:198.51.100.1")
	require.Contains(t, callCounts, "fwd:198.51.100.2")
}

// TestRateLimiterSuccessAndLimit 验证计数超限后的响应与 Retry-After。
func TestRateLimiterSuccessAndLimit(t *testing.T) {
	limiter := NewRateLimiter(&fakeFixedWindow{counts: map[string]int64{}})
	router := gin.New()
	router.Use(limiter.Limit("test", 1, time.Second))
	router.GET("/test", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	request := func() *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/test", nil)
		r.RemoteAddr = "127.0.0.1:1234"
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	require.Equal(t, http.StatusOK, request().Code)
	limited := request()
	require.Equal(t, http.StatusTooManyRequests, limited.Code)
	require.Equal(t, "1", limited.Header().Get("Retry-After"))
	require.JSONEq(t, `{"error":"rate limit exceeded","message":"Too many requests, please try again later"}`, limited.Body.String())
}
