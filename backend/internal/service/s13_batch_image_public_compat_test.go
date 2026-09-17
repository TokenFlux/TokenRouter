//go:build unit

// 原私有入口仅为既有测试保留，生产消费者已经迁入所属模块。
package service

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/batchimage"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

func (s *BatchImagePublicService) resolvePricingSnapshot(ctx context.Context, owner BatchImageOwner, req BatchImageSubmitRequest, provider string, account *Account) (*BatchImagePricingSnapshot, error) {
	return s.nativePublic().ResolvePricingSnapshot(ctx, owner, req, provider, s.batchCandidate(account))
}

func batchImagePricingModel(mapping ChannelMappingResult, requestedModel, channelMappedModel, upstreamModel string) string {
	return batchimage.BatchImagePricingModel(routing.ChannelMappingResult(mapping), requestedModel, channelMappedModel, upstreamModel)
}
