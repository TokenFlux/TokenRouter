//go:build unit

package httpapi

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	service "github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestParseCreativeCreateRunMultipartStopsAtTotalLimit 验证解析阶段不会继续累积超出总量的文件。
func TestParseCreativeCreateRunMultipartStopsAtTotalLimit(t *testing.T) {

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	require.NoError(t, writer.WriteField("group_id", "12"))
	part, err := writer.CreateFormFile("source_images[]", "source.png")
	require.NoError(t, err)
	_, err = part.Write([]byte("123456"))
	require.NoError(t, err)
	part, err = writer.CreateFormFile("source_images[]", "source-2.png")
	require.NoError(t, err)
	_, err = part.Write([]byte("7890"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	req := httptest.NewRequest(http.MethodPost, "/creative/runs", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = req

	parsed, err := parseCreativeCreateRunMultipart(ctx, 32, 8)
	require.Nil(t, parsed)
	require.ErrorIs(t, err, service.ErrCreativeInputTooLarge)
}
