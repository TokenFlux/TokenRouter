package httpapi

import (
	"bytes"
	"io"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/gateway/compact"
)

// CompactFallbackErrorResponse 将原生压缩恢复信号投影回原 HTTP 错误链。
func CompactFallbackErrorResponse(resp *http.Response, signal *compact.Failure) (*http.Response, []byte) {
	headers := make(http.Header)
	if resp != nil {
		headers = resp.Header.Clone()
	}
	if headers.Get("Content-Type") == "" {
		headers.Set("Content-Type", "application/json")
	}
	payload := compact.NormalizeHTTPErrorPayload(signal)
	return &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     headers,
		Body:       io.NopCloser(bytes.NewReader(payload)),
	}, payload
}
