//go:build unit

// 仅为原私有测试保留转接，普通运行构建不持有重复实现。
package service

import provider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

func canonicalizeGrokMediaImageURLFields(body []byte, fields ...string) ([]byte, error) {
	return provider.GrokMediaCodec().CanonicalizeGrokMediaImageURLFields(body, fields...)
}
