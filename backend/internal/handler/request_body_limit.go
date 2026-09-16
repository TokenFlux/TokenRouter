package handler

import (
	"errors"
	"net/http"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	"github.com/TokenFlux/TokenRouter/internal/config"
)

func extractMaxBytesError(err error) (*http.MaxBytesError, bool) {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		return maxErr, true
	}
	return nil, false
}

func buildBodyTooLargeMessage(limit int64) string { return gatewayhttp.BodyTooLargeMessage(limit) }

// readLenientJSONRequestBodyWithPrealloc 按网关请求体上限读取并规范化 JSON。
func readLenientJSONRequestBodyWithPrealloc(req *http.Request, cfg *config.Config) ([]byte, error) {
	return gatewayhttp.ReadLenientJSONRequestBodyWithPrealloc(req, gatewayMaxBodySize(cfg))
}

// gatewayMaxBodySize 读取网关配置的请求体上限，空配置由 httputil 使用保守默认值。
func gatewayMaxBodySize(cfg *config.Config) int64 {
	if cfg == nil {
		return 0
	}
	return cfg.Gateway.MaxBodySize
}
