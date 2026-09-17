package postgres

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/ent/creativerun"
	"github.com/TokenFlux/TokenRouter/internal/creative"
)

// RecordProviderOutcome 原子记录成功事实与 settle 恢复动作，不持久化图片字节。
func (r *creativeRunRepository) RecordProviderOutcome(ctx context.Context, id string, accountID int64, outputs []creative.CreativeRunOutput, now time.Time) error {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	current, err := tx.CreativeRun.Query().Where(creativerun.RunIDEQ(id)).Only(ctx)
	if err != nil {
		return translatePersistenceError(err, creative.ErrCreativeRunNotFound, nil)
	}
	if current.ProviderResultRecordedAt != nil {
		return tx.Commit()
	}
	if current.Status != creative.CreativeRunStatusRunning && current.Status != creative.CreativeRunStatusCancelled {
		return creative.ErrCreativeInvalidTransition
	}
	change := tx.CreativeRun.Update().Where(creativerun.IDEQ(current.ID), creativerun.VersionEQ(current.Version)).SetProviderResultRecordedAt(now).SetUpdatedAt(now).AddVersion(1)
	if accountID > 0 {
		change.SetAccountID(accountID)
	}
	if current.Status != creative.CreativeRunStatusCancelled {
		change.SetStatus(creative.CreativeRunStatusProviderSucceeded)
	}
	n, err := change.Save(ctx)
	if err != nil {
		return err
	}
	if n != 1 {
		return creative.ErrCreativeInvalidTransition
	}
	participant := &creativeRunRepository{client: tx.Client()}
	for _, output := range outputs {
		if err := participant.UpdateCreativeRunOutput(ctx, id, output.OutputIndex, output.Status, creative.CreativeDerefString(output.MimeType), creative.CreativeDerefInt64(output.ByteSize), output.TransientExpiresAt, creative.CreativeDerefString(output.ErrorCode), creative.CreativeDerefString(output.ErrorMessage)); err != nil {
			return err
		}
	}
	if err := tx.CreativeRunOutbox.Create().SetRunID(id).SetOperation(string(creative.CreativeRunOutboxSettle)).SetStatus("pending").SetAvailableAt(now).OnConflictColumns("run_id", "operation").DoNothing().Exec(ctx); err != nil {
		return err
	}
	return tx.Commit()
}

// CompleteProviderOutcome 交付丢失与成功均持久化实际费用，取消终态不能被改回成功。
func (r *creativeRunRepository) CompleteProviderOutcome(ctx context.Context, id string, cost float64, lost bool, now time.Time) error {
	current, err := r.client.CreativeRun.Query().Where(creativerun.RunIDEQ(id)).Only(ctx)
	if err != nil {
		return translatePersistenceError(err, creative.ErrCreativeRunNotFound, nil)
	}
	if current.Status == creative.CreativeRunStatusSucceeded || current.Status == creative.CreativeRunStatusResultLost {
		return nil
	}
	if current.ProviderResultRecordedAt == nil {
		return creative.ErrCreativeInvalidTransition
	}
	target := creative.CreativeRunStatusSucceeded
	if lost {
		target = creative.CreativeRunStatusResultLost
	}
	if current.Status == creative.CreativeRunStatusCancelled {
		target = current.Status
	}
	n, err := r.client.CreativeRun.Update().Where(creativerun.IDEQ(current.ID), creativerun.VersionEQ(current.Version)).SetStatus(target).SetActualCost(cost).SetCompletedAt(now).SetUpdatedAt(now).AddVersion(1).Save(ctx)
	if err != nil {
		return err
	}
	if n != 1 {
		return creative.ErrCreativeInvalidTransition
	}
	return nil
}
