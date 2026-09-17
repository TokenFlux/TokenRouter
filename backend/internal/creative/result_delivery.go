package creative

import (
	"context"
	"errors"
	"time"
)

const OutputDeliveryPending = "DELIVERY_PENDING"
const OutputDeliveryLost = "DELIVERY_LOST"

// ProviderOutput 是本次调用已确认的图片，字节只在当前 worker 与临时存储之间流转。
type ProviderOutput struct {
	Index        int
	Success      bool
	Bytes        []byte
	Mime         string
	ErrorCode    string
	ErrorMessage string
}

// ProviderOutcomeStore 把成功事实、输出元数据和恢复动作放在同一存储事务中。
type ProviderOutcomeStore interface {
	RecordProviderOutcome(context.Context, string, int64, []CreativeRunOutput, time.Time) error
	CompleteProviderOutcome(context.Context, string, float64, bool, time.Time) error
}

// ResultDelivery 只负责成功事实与结果交付，不调用供应商，也不决定资金金额。
type ResultDelivery struct {
	Now      func() time.Time
	Repo     CreativeRunRepository
	Outcomes ProviderOutcomeStore
	Store    CreativeTransientStore
	TTL      time.Duration
}

// Record 先确认成功事实，再有界保存输出；暂存失败不会丢弃已发生的服务事实。
func (d ResultDelivery) Record(ctx context.Context, id string, accountID int64, outputs []ProviderOutput) error {
	if d.Outcomes == nil {
		return errors.New("creative provider outcome store is not configured")
	}
	now := d.now()
	expires := now.Add(d.TTL)
	metadata := make([]CreativeRunOutput, 0, len(outputs))
	for _, output := range outputs {
		status := CreativeRunOutputStatusFailed
		code, message := output.ErrorCode, output.ErrorMessage
		if output.Success {
			if len(output.Bytes) == 0 {
				return ErrCreativeTransientFailed.WithCause(errors.New("creative output is empty"))
			}
			status = CreativeRunOutputStatusSucceeded
			code = OutputDeliveryPending
			message = ""
		}
		mime := output.Mime
		size := int64(len(output.Bytes))
		metadata = append(metadata, CreativeRunOutput{RunID: id, OutputIndex: output.Index, Status: status, MimeType: &mime, ByteSize: &size, TransientExpiresAt: &expires, ErrorCode: &code, ErrorMessage: &message})
	}
	if err := d.Outcomes.RecordProviderOutcome(ctx, id, accountID, metadata, now); err != nil {
		return err
	}
	saveCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for _, output := range outputs {
		if !output.Success {
			continue
		}
		var saved bool
		for attempt := 0; attempt < 3; attempt++ {
			if saveCtx.Err() != nil {
				break
			}
			if d.Store != nil && d.Store.SaveOutput(saveCtx, id, output.Index, output.Bytes, d.TTL) == nil {
				saved = true
				break
			}
			if attempt < 2 {
				timer := time.NewTimer(time.Second)
				select {
				case <-saveCtx.Done():
					timer.Stop()
				case <-timer.C:
				}
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		code := ""
		if !saved {
			code = OutputDeliveryLost
		}
		if err := d.Repo.UpdateCreativeRunOutput(ctx, id, output.Index, CreativeRunOutputStatusSucceeded, output.Mime, int64(len(output.Bytes)), &expires, code, ""); err != nil {
			return err
		}
	}
	return nil
}

// Lost 查询真实可交付性；Redis 故障返回错误，明确缺失才按结果丢失处理。
func (d ResultDelivery) Lost(ctx context.Context, id string, outputs []*CreativeRunOutput) (bool, error) {
	lost := false
	for _, output := range outputs {
		if output == nil || output.Status != CreativeRunOutputStatusSucceeded {
			continue
		}
		if output.ErrorCode != nil && *output.ErrorCode == OutputDeliveryLost {
			lost = true
			continue
		}
		if d.Store == nil {
			return false, ErrCreativeTransientUnavailable
		}
		_, err := d.Store.LoadOutput(ctx, id, output.OutputIndex)
		if errors.Is(err, ErrCreativeTransientNotFound) {
			lost = true
			continue
		}
		if err != nil {
			return false, err
		}
	}
	return lost, nil
}

// now 保持各原取时点，构造时可注入同一时钟来源。
func (s ResultDelivery) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
