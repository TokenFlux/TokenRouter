// 旧任务资金签名仅转换值，算法由 batchimage 与 billing 唯一实现。
package service

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/batchimage"
	"github.com/TokenFlux/TokenRouter/internal/billing"
)

func BatchImageHoldRequestID(batchID string) string {
	return batchimage.BatchImageHoldRequestID(batchID)
}
func BatchImageCaptureRequestID(batchID string) string {
	return batchimage.BatchImageCaptureRequestID(batchID)
}
func BatchImageReleaseRequestID(batchID string) string {
	return batchimage.BatchImageReleaseRequestID(batchID)
}

type batchLegacyFunds struct{ store UsageBillingRepository }

func batchFundingProjection(repo UsageBillingRepository) batchimage.Funding {
	out := batchimage.Funding{Observe: creativeLegacyObserve}
	if source, ok := repo.(interface{ BillingFunds() *billing.Funds }); ok {
		out.Store = source.BillingFunds()
		return out
	}
	if repo != nil {
		out.Store = batchLegacyFunds{repo}
	}
	return out
}
func (p batchLegacyFunds) Reserve(ctx context.Context, cmd *billing.TaskFundsCommand) (*billing.TaskFundsResult, error) {
	old := &BatchImageBalanceHoldCommand{}
	old.UpdateBillingCommand(cmd)
	result, err := p.store.ReserveBatchImageBalance(ctx, old)
	if err == nil {
		*cmd = *old.BillingCommand()
	}
	return result, err
}
func (p batchLegacyFunds) Capture(ctx context.Context, cmd *billing.TaskFundsCommand) (*billing.TaskFundsResult, error) {
	old := &BatchImageBalanceHoldCommand{}
	old.UpdateBillingCommand(cmd)
	result, err := p.store.CaptureBatchImageBalance(ctx, old)
	if err == nil {
		*cmd = *old.BillingCommand()
	}
	return result, err
}
func (p batchLegacyFunds) Release(ctx context.Context, cmd *billing.TaskFundsCommand) (*billing.TaskFundsResult, error) {
	old := &BatchImageBalanceHoldCommand{}
	old.UpdateBillingCommand(cmd)
	result, err := p.store.ReleaseBatchImageBalance(ctx, old)
	if err == nil {
		*cmd = *old.BillingCommand()
	}
	return result, err
}
