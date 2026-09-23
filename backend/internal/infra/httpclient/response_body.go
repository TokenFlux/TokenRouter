package httpclient

import (
	"errors"
	"fmt"
	"io"
)

// DefaultResponseReadMaxBytes 保留上游非流响应的原默认读取上限。
const DefaultResponseReadMaxBytes int64 = 128 * 1024 * 1024

var ErrResponseBodyTooLarge = errors.New("upstream response body too large")

// ReadResponseBodyLimited 保留多读一个字节判定超限、nil 与底层读取错误语义；关闭仍由调用方负责。
func ReadResponseBodyLimited(reader io.Reader, maxBytes int64) ([]byte, error) {
	if reader == nil {
		return nil, errors.New("response body is nil")
	}
	if maxBytes <= 0 {
		maxBytes = DefaultResponseReadMaxBytes
	}
	body, err := io.ReadAll(io.LimitReader(reader, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, fmt.Errorf("%w: limit=%d", ErrResponseBodyTooLarge, maxBytes)
	}
	return body, nil
}
