// PipelineProcessor 在查询/索引和结算之间推进，不包含第二套供应商提交重试。
package batchimage

import (
	"context"
	"errors"
	"time"
)

type PipelineProcessor struct {
	ProviderProcessor *ProviderProcessor
	SettlementService *Settlement
	RetryDelay        time.Duration
}

func (p *PipelineProcessor) Process(ctx context.Context, batchID string) (BatchImageProcessResult, error) {
	if p == nil || p.ProviderProcessor == nil {
		return BatchImageProcessResult{}, errors.New("batch image pipeline processor is not configured")
	}
	job, err := p.ProviderProcessor.Repo.GetBatchImageJobByBatchID(ctx, batchID)
	if err != nil {
		return BatchImageProcessResult{}, err
	}
	if job.Status == BatchImageJobStatusSettling {
		if p.SettlementService == nil {
			return BatchImageProcessResult{Terminal: true}, nil
		}
		_, err := p.SettlementService.Settle(ctx, batchID)
		if err != nil {
			if errors.Is(err, ErrBatchImageSettlementBillingFailed) {
				updated, getErr := p.ProviderProcessor.Repo.GetBatchImageJobByBatchID(ctx, batchID)
				if getErr == nil && IsTerminalBatchImageJobStatus(updated.Status) {
					return BatchImageProcessResult{Terminal: true}, nil
				}
				delay := p.RetryDelay
				if delay <= 0 {
					delay = BatchImageSettlementRetryDelay
				}
				return BatchImageProcessResult{RequeueAfter: delay}, nil
			}
			return BatchImageProcessResult{}, err
		}
		return BatchImageProcessResult{Terminal: true}, nil
	}
	return p.ProviderProcessor.Process(ctx, batchID)
}
