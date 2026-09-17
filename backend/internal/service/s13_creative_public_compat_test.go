//go:build unit

// 原私有入口仅为既有测试保留，生产消费者已经迁入所属模块。
package service

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/creative"

	_ "image/jpeg"

	_ "image/png"
)

func (s *CreativePublicService) creativePrice(ctx context.Context, group *Group, model, imageSize string) float64 {
	return s.nativePublic().CreativePrice(ctx, creativeGroupProjection(group), model, imageSize)
}

type creativeModelCapabilities = creative.CreativeModelCapabilities

func creativeCapabilitiesForModel(platform, model string) creativeModelCapabilities {
	return creative.CreativeCapabilitiesForModel(platform, model)
}
func creativeFilterImageSizesForModel(platform, model string, sizes []string) []string {
	return creative.CreativeFilterImageSizesForModel(platform, model, sizes)
}
func creativeGeminiModelsForAccount(account *Account) []string {
	return creative.CreativeGeminiModelsForAccount(creativeCatalogAccount(account))
}
func isCreativeGeminiImageModel(model string) bool { return creative.IsCreativeGeminiImageModel(model) }
func creativeExpandAccountModels(account *Account, candidates []string, matches func(string) bool) []string {
	return creative.CreativeExpandAccountModels(creativeCatalogAccount(account), candidates, matches)
}
func defaultCreativeGrokModelCandidates() []string {
	return creative.DefaultCreativeGrokModelCandidates()
}

type validatedCreativeParams = creative.ValidatedCreativeParams

func (s *CreativePublicService) validateCreateParams(ctx context.Context, userID int64, params *CreateCreativeRunParamsPublic) (*validatedCreativeParams, error) {
	return s.nativePublic().ValidateCreateParams(ctx, userID, params)
}

type creativeFingerprintPayload = creative.CreativeFingerprintPayload

func buildCreativeRequestFingerprint(payload creativeFingerprintPayload) string {
	return creative.BuildCreativeRequestFingerprint(payload)
}
func sha256Hex(data []byte) string { return creative.Sha256Hex(data) }

func creativeOperationsForPlatform(platform string) []string {
	return creative.CreativeOperationsForPlatform(platform)
}
