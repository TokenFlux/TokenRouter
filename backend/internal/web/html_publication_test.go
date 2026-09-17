//go:build embed

package web

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type s15BlockingPublic struct {
	entered, release chan struct{}
	calls            atomic.Int64
}

func (p *s15BlockingPublic) GetPublicSettingsForInjection(context.Context) (any, error) {
	if p.calls.Add(1) == 1 {
		close(p.entered)
		<-p.release
		return map[string]any{"site_name": "OLD-S15"}, nil
	}
	return map[string]any{"site_name": "NEW-S15"}, nil
}

// 旧回源跨过失效点后不能成为后续请求使用的缓存。
func TestFrontendServerLateHTMLPublication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	p := &s15BlockingPublic{entered: make(chan struct{}), release: make(chan struct{})}
	s, err := NewFrontendServer(p)
	require.NoError(t, err)
	call := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/", nil)
		s.serveIndexHTML(c)
		return w
	}
	done := make(chan struct{})
	go func() { call(); close(done) }()
	<-p.entered
	s.InvalidateCache()
	close(p.release)
	<-done
	w := call()
	require.True(t, strings.Contains(w.Body.String(), "NEW-S15"), "旧回源覆盖失效，后续请求未取得新配置")
}
