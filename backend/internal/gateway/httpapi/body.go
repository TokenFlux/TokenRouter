package httpapi

import (
	"errors"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/server/httpx"
)

// ReadLenientJSONRequestBodyWithPrealloc 读取请求体，并在严格 JSON
// 校验前转义字符串中的原始控制字节。
func ReadLenientJSONRequestBodyWithPrealloc(req *http.Request, maxNormalizedBytes int64) ([]byte, error) {
	body, err := ReadRequestBodyWithPrealloc(req)
	if err != nil {
		return nil, err
	}
	return NormalizeLenientJSONRequestBody(body, maxNormalizedBytes)
}

// NormalizeLenientJSONRequestBody 保留既有 HTTP 错误类型及其 Limit 字段。
func NormalizeLenientJSONRequestBody(body []byte, limit int64) ([]byte, error) {
	result, err := openai.NormalizeLenientJSONRequestBody(body, limit)
	var exceeded *openai.BodyLimitError
	if errors.As(err, &exceeded) {
		return nil, &http.MaxBytesError{Limit: exceeded.Limit}
	}
	return result, err
}

// ReadRequestBodyWithPrealloc 读取及解压仍由 S01 的 HTTP 实现拥有。
func ReadRequestBodyWithPrealloc(req *http.Request) ([]byte, error) {
	return httpx.ReadRequestBodyWithPrealloc(req)
}
