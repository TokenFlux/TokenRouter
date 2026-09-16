// 兼容入口委托 HTTP Adapter；纯 JSON 修复仍由 protocol 唯一实现。
package httputil

import (
	"net/http"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
)

func ReadLenientJSONRequestBodyWithPrealloc(req *http.Request, max int64) ([]byte, error) {
	return gatewayhttp.ReadLenientJSONRequestBodyWithPrealloc(req, max)
}
func NormalizeLenientJSONRequestBody(body []byte, limit int64) ([]byte, error) {
	return gatewayhttp.NormalizeLenientJSONRequestBody(body, limit)
}
func ReadRequestBodyWithPrealloc(req *http.Request) ([]byte, error) {
	return gatewayhttp.ReadRequestBodyWithPrealloc(req)
}
