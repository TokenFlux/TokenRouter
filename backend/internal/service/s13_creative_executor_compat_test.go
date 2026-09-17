//go:build unit

// 原私有入口仅为既有测试保留，生产消费者已经迁入所属模块。
package service

import (
	creativeprovider "github.com/TokenFlux/TokenRouter/internal/creative/provider"

	"github.com/TokenFlux/TokenRouter/internal/creative"
)

const (
	creativeMaxOutputBytes = 32 << 20
)

func creativePlatformImageModel(platform, model string) bool {
	return creative.CreativePlatformImageModel(platform, model)
}
func normalizeCreativeOutputs(outputs []CreativeOutput) ([]CreativeOutput, error) {
	return creative.NormalizeCreativeOutputs(outputs)
}
func creativeOpenAIImageSize(imageSize, aspectRatio string) string {
	return creativeprovider.CreativeOpenAIImageSize(imageSize, aspectRatio)
}
