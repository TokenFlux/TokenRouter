//go:build unit

// 原私有入口仅为既有测试保留，生产消费者已经迁入所属模块。
package service

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/batchimage"
)

func buildBatchImageHoldCommand(job *BatchImageJob, requestID string, actualAmount float64, payloadHash string) (*BatchImageBalanceHoldCommand, error) {
	cmd, err := batchimage.BuildHoldCommand(job, requestID, actualAmount, payloadHash)
	if err != nil {
		return nil, err
	}
	old := &BatchImageBalanceHoldCommand{}
	old.UpdateBillingCommand(cmd)
	return old, nil
}
func reserveBatchImageBalanceHold(ctx context.Context, repo UsageBillingRepository, job *BatchImageJob, groupID *int64, payloadHash string) error {
	return batchFundingProjection(repo).Reserve(ctx, job, groupID, payloadHash)
}
func captureBatchImageBalanceHold(ctx context.Context, repo UsageBillingRepository, job *BatchImageJob, actualAmount float64, payloadHash string) (*BatchImageBalanceHoldResult, error) {
	return batchFundingProjection(repo).Capture(ctx, job, actualAmount, payloadHash)
}
func releaseBatchImageBalanceHold(ctx context.Context, repo UsageBillingRepository, job *BatchImageJob, payloadHash string) error {
	return batchFundingProjection(repo).Release(ctx, job, payloadHash)
}
