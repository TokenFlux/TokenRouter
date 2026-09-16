package httpapi

import (
	"errors"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// shutdownBody 确认停止入口不会读取报文或调用尚未进入的业务依赖。
type shutdownBody struct{ t *testing.T }

func (b shutdownBody) Read([]byte) (int, error) {
	b.t.Fatal("stopped request read body")
	return 0, io.EOF
}
func (shutdownBody) Close() error { return nil }
func TestRequestLifetimeRejectsBeforeBodyAndDependencies(t *testing.T) {
	h := NewCountTokensHandler(1024, 1, nil, nil)
	h.BindRequestActivity(func() (func(), error) { return nil, errors.New("stopped") })
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/v1/messages/count_tokens", nil)
	c.Request.Body = shutdownBody{t}
	h.CountTokens(c)
	require.Equal(t, 503, rec.Code)
	require.JSONEq(t, `{"type":"error","error":{"type":"api_error","message":"Service is shutting down"}}`, rec.Body.String())
}
func TestRequestLifetimeUsesSharedOwnerWithoutNewState(t *testing.T) {
	active := 0
	scope := requestLifetime{}
	scope.BindRequestActivity(func() (func(), error) { active++; return func() { active-- }, nil })
	done, accepted := scope.beginRequest(nil, "openai")
	require.True(t, accepted)
	require.Equal(t, 1, active)
	done()
	require.Zero(t, active)
}
