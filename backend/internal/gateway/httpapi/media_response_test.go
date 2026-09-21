package httpapi

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// HTTP 投影只转发白名单下载头；提交标记必须先于第一块响应体。
func TestMediaContentResponsePreservesRangeAndCommit(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	committed := false
	response := &http.Response{StatusCode: 206, Header: http.Header{"Content-Range": []string{"bytes 0-3/20"}, "X-Secret": []string{"hidden"}}, ContentLength: 4, Body: io.NopCloser(strings.NewReader("data"))}
	require.NoError(t, WriteGrokMediaContentResponse(c, response, func() { committed = true }))
	require.True(t, committed)
	require.Equal(t, 206, recorder.Code)
	require.Equal(t, "data", recorder.Body.String())
	require.Equal(t, "bytes 0-3/20", recorder.Header().Get("Content-Range"))
	require.Equal(t, "4", recorder.Header().Get("Content-Length"))
	require.Equal(t, "application/octet-stream", recorder.Header().Get("Content-Type"))
	require.Empty(t, recorder.Header().Get("X-Secret"))
}

type mediaReadFailure struct{}

func (mediaReadFailure) Read([]byte) (int, error) { return 0, errors.New("download failed") }
func (mediaReadFailure) Close() error             { return nil }
func TestMediaContentReadFailureRemainsVisible(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	err := WriteGrokMediaContentResponse(c, &http.Response{StatusCode: 200, ContentLength: -1, Header: make(http.Header), Body: mediaReadFailure{}}, nil)
	require.ErrorContains(t, err, "download failed")
}
func TestVoiceMissingBodyPreservesMethodDifference(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = &http.Request{Method: http.MethodGet}
	body, err := ReadGrokVoiceBody(c)
	require.NoError(t, err)
	require.Nil(t, body)
	c.Request.Method = http.MethodPost
	_, err = ReadGrokVoiceBody(c)
	require.ErrorContains(t, err, "request body is required")
}
