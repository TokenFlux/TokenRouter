package httpapi

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/server/middleware"
)

// TestLogRequestBodyReadFailureClassifiesWithoutPayload injects a decoding
// error containing a private payload marker. It requires normalized metadata
// and confirms that the captured diagnostic excludes the marker.
func TestLogRequestBodyReadFailureClassifiesWithoutPayload(t *testing.T) {
	log, logs := newObservedLogger(t)
	req, err := http.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader("secret-payload-marker"))
	require.NoError(t, err)
	req.Header.Set("Content-Encoding", "gzip")
	req.ContentLength = 21

	LogRequestBodyReadFailure(log, req, errors.New(`decode Content-Encoding "gzip": unexpected EOF secret-payload-marker`))

	entries := logs.All()
	require.Len(t, entries, 1)
	require.Equal(t, "read request body failed", entries[0].Message)
	fields := entries[0].ContextMap()
	require.Equal(t, "decode_content_encoding", fields["error_kind"])
	require.Equal(t, "gzip", fields["content_encoding"])
	require.EqualValues(t, 21, fields["content_length"])
	require.NotContains(t, entries[0].Message+fmt.Sprint(fields), "secret-payload-marker")
}

// TestRequestBodyReadErrorKind supplies unsupported encoding, truncated input
// and a maximum-size error. It protects their diagnostic labels and the other
// category for arbitrary encoding text.
func TestRequestBodyReadErrorKind(t *testing.T) {
	require.Equal(t, "unsupported_content_encoding", requestBodyReadErrorKind(errors.New(`decode Content-Encoding "br": unsupported Content-Encoding`)))
	require.Equal(t, "truncated_body", requestBodyReadErrorKind(io.ErrUnexpectedEOF))
	require.Equal(t, "max_bytes", requestBodyReadErrorKind(&http.MaxBytesError{Limit: 10}))
	require.Equal(t, "other", requestContentEncodingCategory("private-payload-marker"))
}

// TestRequestBodyLimitTooLarge sends one byte beyond a middleware-enforced
// request limit. Reading must return HTTP 413 with the shared size message
// instead of admitting the oversized payload.
func TestRequestBodyLimitTooLarge(t *testing.T) {
	// A tiny limit exercises the exact first-byte overflow boundary.
	limit := int64(16)
	router := gin.New()
	router.Use(middleware.RequestBodyLimit(limit))
	router.POST("/test", func(c *gin.Context) {
		_, err := io.ReadAll(c.Request.Body)
		if err != nil {
			if maxErr, ok := errors.AsType[*http.MaxBytesError](err); ok {
				c.JSON(http.StatusRequestEntityTooLarge, gin.H{
					"error": BodyTooLargeMessage(maxErr.Limit),
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
	require.Contains(t, recorder.Body.String(), BodyTooLargeMessage(limit))
}
