//go:build unit

// 白盒测试保留必要旧名称，生产入口已使用唯一所属实现。
package service

import (
	"github.com/TokenFlux/TokenRouter/internal/creative"

	gemininative "github.com/TokenFlux/TokenRouter/internal/upstream/gemini"
)

func parseCreativeGeminiImageOutputs(body []byte) ([]creative.CreativeOutput, error) {
	return gemininative.ParseImageOutputs(body, func(status int, message string) error { return creative.CreativeHTTPStatusError(status, message) })
}
