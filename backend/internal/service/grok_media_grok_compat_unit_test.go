//go:build unit

// 迁移后的私有测试转接不进入生产构建。
package service

import provider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

func grokMediaSignedVideoContentURL(body []byte, requestID string) (string, error) {
	return provider.GrokMediaCodec().GrokMediaSignedVideoContentURL(body, requestID)
}
