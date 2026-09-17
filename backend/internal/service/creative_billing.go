// 旧资金入口仅转换命令，任务规则与 billing 资金实现均只有一份。
package service

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/creative"
)

func CreativeHoldRequestID(runID string) string    { return creative.CreativeHoldRequestID(runID) }
func CreativeCaptureRequestID(runID string) string { return creative.CreativeCaptureRequestID(runID) }
func CreativeReleaseRequestID(runID string) string { return creative.CreativeReleaseRequestID(runID) }
func CreativeSettlementRequestID(runID string) string {
	return creative.CreativeSettlementRequestID(runID)
}

type creativeLegacyFunds struct{ store UsageBillingRepository }

func (p creativeLegacyFunds) Reserve(ctx context.Context, cmd *billing.TaskFundsCommand) (*billing.TaskFundsResult, error) {
	if p.store == nil {
		return nil, ErrCreativeBillingHoldFailed
	}
	old := &BatchImageBalanceHoldCommand{CreativeEntity: true}
	old.UpdateBillingCommand(cmd)
	result, err := p.store.ReserveBatchImageBalance(ctx, old)
	if err == nil {
		*cmd = *old.BillingCommand()
	}
	return result, err
}
func (p creativeLegacyFunds) Capture(ctx context.Context, cmd *billing.TaskFundsCommand) (*billing.TaskFundsResult, error) {
	if p.store == nil {
		return nil, ErrCreativeBillingHoldFailed
	}
	old := &BatchImageBalanceHoldCommand{CreativeEntity: true}
	old.UpdateBillingCommand(cmd)
	result, err := p.store.CaptureBatchImageBalance(ctx, old)
	if err == nil {
		*cmd = *old.BillingCommand()
	}
	return result, err
}
func (p creativeLegacyFunds) Release(ctx context.Context, cmd *billing.TaskFundsCommand) (*billing.TaskFundsResult, error) {
	if p.store == nil {
		return nil, ErrCreativeBillingHoldFailed
	}
	old := &BatchImageBalanceHoldCommand{CreativeEntity: true}
	old.UpdateBillingCommand(cmd)
	result, err := p.store.ReleaseBatchImageBalance(ctx, old)
	if err == nil {
		*cmd = *old.BillingCommand()
	}
	return result, err
}
