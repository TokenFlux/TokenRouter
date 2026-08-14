package handler

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BrandonVee/TokenRouter/internal/config"
	"github.com/BrandonVee/TokenRouter/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestRequestBodyLimitTooLarge(t *testing.T) {
	gin.SetMode(gin.TestMode)

	limit := int64(16)
	router := gin.New()
	router.Use(middleware.RequestBodyLimit(limit))
	router.POST("/test", func(c *gin.Context) {
		_, err := io.ReadAll(c.Request.Body)
		if err != nil {
			if maxErr, ok := extractMaxBytesError(err); ok {
				c.JSON(http.StatusRequestEntityTooLarge, gin.H{
					"error": buildBodyTooLargeMessage(maxErr.Limit),
				})
				return
			}
			c.JSON(http.StatusBadRequest, gin.H{
				"error": "read_failed",
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	payload := bytes.Repeat([]byte("a"), int(limit+1))
	req := httptest.NewRequest(http.MethodPost, "/test", bytes.NewReader(payload))
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code)
	require.Contains(t, recorder.Body.String(), buildBodyTooLargeMessage(limit))
}

func TestReadLenientJSONRequestBodyWithPreallocUsesGatewayLimit(t *testing.T) {
	body := []byte("{\"input\":\"\x00\x00\"}")
	limit := int64(len(body) + 5)
	cfg := &config.Config{}
	cfg.Gateway.MaxBodySize = limit
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))

	_, err := readLenientJSONRequestBodyWithPrealloc(req, cfg)
	var maxErr *http.MaxBytesError
	require.True(t, errors.As(err, &maxErr))
	// 归一化膨胀后必须继续使用网关配置的上限。
	require.Equal(t, limit, maxErr.Limit)
}
