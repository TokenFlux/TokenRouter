// 任务查询仅消费工作区与用户投影，返回独立的公开值。
package creative

import (
	"context"
	"errors"
	"strings"
	"time"
)

type Queries struct {
	Now            func() time.Time
	Repo           CreativeRunRepository
	TransientStore CreativeTransientStore
	Enabled        func(context.Context) bool
	Observe        func(string, ...any)
}

func (s *Queries) warn(event string, values ...any) {
	if s.Observe != nil {
		s.Observe(event, values...)
	}
}

// GetRun 返回单个任务（含输出元数据），校验所有权。
func (s *Queries) GetRun(ctx context.Context, scope CreativeRunScope, runID string) (*CreativeRunPublic, error) {
	normalizedScope, err := NormalizeCreativeRunScope(scope)
	if err != nil {
		return nil, err
	}
	scope = normalizedScope
	if s.Enabled == nil || !s.Enabled(ctx) {
		return nil, ErrCreativeDisabled
	}
	run, err := s.Repo.GetCreativeRunByRunIDForOwner(ctx, scope, runID)
	if err != nil {
		return nil, err
	}
	outputs, err := s.Repo.ListCreativeRunOutputs(ctx, run.RunID)
	if err != nil {
		return nil, err
	}
	return CreativeRunToPublic(run, outputs), nil
}

// ListRuns 返回当前用户的任务列表（created_at desc 分页）。
func (s *Queries) ListRuns(ctx context.Context, scope CreativeRunScope, filter CreativeRunFilter) (*CreativeListRunsResponse, error) {
	normalizedScope, err := NormalizeCreativeRunScope(scope)
	if err != nil {
		return nil, err
	}
	scope = normalizedScope
	if s.Enabled == nil || !s.Enabled(ctx) {
		return nil, ErrCreativeDisabled
	}
	if filter.Limit <= 0 || filter.Limit > 100 {
		filter.Limit = 20
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	runs, err := s.Repo.ListCreativeRunsForOwner(ctx, scope, filter)
	if err != nil {
		return nil, err
	}
	data := make([]*CreativeRunPublic, 0, len(runs))
	outputByRun := make(map[string][]*CreativeRunOutput, len(runs))
	batchOutputs := false
	if batchReader, ok := s.Repo.(CreativeRunOutputBatchReader); ok {
		batchOutputs = true
		runIDs := make([]string, 0, len(runs))
		for _, run := range runs {
			if run != nil {
				runIDs = append(runIDs, run.RunID)
			}
		}
		outputByRun, err = batchReader.ListCreativeRunOutputsForRuns(ctx, runIDs)
		if err != nil {
			return nil, err
		}
	}
	for _, run := range runs {
		// 历史列表同样需要输出元数据，否则前端无法关联本地素材与缺失占位。
		outputs := outputByRun[run.RunID]
		if !batchOutputs {
			outputs, err = s.Repo.ListCreativeRunOutputs(ctx, run.RunID)
			if err != nil {
				return nil, err
			}
		}
		data = append(data, CreativeRunToPublic(run, outputs))
	}
	return &CreativeListRunsResponse{Data: data, HasMore: len(data) == filter.Limit}, nil
}

// CreativeOutputContent 是输出内容的返回结构。
type CreativeOutputContent struct {
	Content     []byte
	ContentType string
}

// GetOutputContent 校验所有权与输出状态后从临时存储读取图片字节。
// 过期或缺失时：任务为 succeeded 则转 result_lost，并返回明确错误，绝不明示成功。
func (s *Queries) GetOutputContent(ctx context.Context, scope CreativeRunScope, runID string, outputIndex int) (*CreativeOutputContent, error) {
	normalizedScope, err := NormalizeCreativeRunScope(scope)
	if err != nil {
		return nil, err
	}
	scope = normalizedScope
	if s.Enabled == nil || !s.Enabled(ctx) {
		return nil, ErrCreativeDisabled
	}
	run, err := s.Repo.GetCreativeRunByRunIDForOwner(ctx, scope, runID)
	if err != nil {
		return nil, err
	}
	if !IsTerminalCreativeRunStatus(run.Status) {
		return nil, ErrCreativeOutputNotReady
	}
	output, err := s.Repo.GetCreativeRunOutput(ctx, runID, outputIndex)
	if err != nil {
		return nil, err
	}
	switch output.Status {
	case CreativeRunOutputStatusSucceeded:
	case CreativeRunOutputStatusAcked:
		return nil, ErrCreativeOutputExpired
	case CreativeRunOutputStatusLost:
		return nil, ErrCreativeResultLost
	case CreativeRunOutputStatusFailed:
		return nil, ErrCreativeOutputNotReady
	default:
		return nil, ErrCreativeOutputNotReady
	}
	now := s.now()
	if output.TransientExpiresAt != nil && now.After(*output.TransientExpiresAt) {
		if err := s.MarkRunResultLost(ctx, run); err != nil {
			return nil, err
		}
		return nil, ErrCreativeOutputExpired
	}
	if s.TransientStore == nil {
		return nil, ErrCreativeTransientFailed
	}
	data, err := s.TransientStore.LoadOutput(ctx, runID, outputIndex)
	if err != nil {
		if errors.Is(err, ErrCreativeTransientUnavailable) {
			return nil, ErrCreativeTransientFailed
		}
		// 临时输出已被 TTL 清理或丢失：成功任务转为 result_lost。
		if markErr := s.MarkRunResultLost(ctx, run); markErr != nil {
			return nil, markErr
		}
		return nil, ErrCreativeResultLost
	}
	contentType := "application/octet-stream"
	if output.MimeType != nil && strings.TrimSpace(*output.MimeType) != "" {
		contentType = strings.TrimSpace(*output.MimeType)
	}
	return &CreativeOutputContent{Content: data, ContentType: contentType}, nil
}

// markRunResultLost 把 succeeded 任务降级为 result_lost；数据库失败必须返回以便重试。
func (s *Queries) MarkRunResultLost(ctx context.Context, run *CreativeRun) error {
	if run == nil || run.Status != CreativeRunStatusSucceeded {
		return nil
	}
	if err := s.Repo.TransitionCreativeRunStatus(ctx, run.RunID, CreativeRunStatusResultLost, CreativeRunTransitionOptions{
		ErrorCode:    CreativeStringPtr("RESULT_EXPIRED"),
		ErrorMessage: CreativeStringPtr("transient output expired before client acknowledgment"),
	}); err != nil {
		return err
	}
	return nil
}

// AckOutput 在客户端确认保存后删除临时输出并标记 acked，幂等。
func (s *Queries) AckOutput(ctx context.Context, scope CreativeRunScope, runID string, outputIndex int) error {
	normalizedScope, err := NormalizeCreativeRunScope(scope)
	if err != nil {
		return err
	}
	scope = normalizedScope
	if s.Enabled == nil || !s.Enabled(ctx) {
		return ErrCreativeDisabled
	}
	run, err := s.Repo.GetCreativeRunByRunIDForOwner(ctx, scope, runID)
	if err != nil {
		return err
	}
	if !IsTerminalCreativeRunStatus(run.Status) {
		return ErrCreativeOutputNotReady
	}
	output, err := s.Repo.GetCreativeRunOutput(ctx, runID, outputIndex)
	if err != nil {
		return err
	}
	if output.Status == CreativeRunOutputStatusAcked {
		// 重复 ack 视为成功（幂等）。
		if s.TransientStore != nil {
			_ = s.TransientStore.DeleteOutput(ctx, runID, outputIndex)
		}
		return nil
	}
	if output.Status != CreativeRunOutputStatusSucceeded {
		return ErrCreativeOutputNotReady
	}
	if err := s.Repo.MarkCreativeRunOutputAcked(ctx, runID, outputIndex, s.now()); err != nil {
		return err
	}
	if s.TransientStore != nil {
		if err := s.TransientStore.DeleteOutput(ctx, runID, outputIndex); err != nil {
			s.warn("creative.ack_delete_output_failed",
				"run_id", runID,
				"output_index", outputIndex,
				"error", err,
			)
			// 数据库已经记录 ack，删除失败由 transient cleanup 负责补偿。
		}
	}
	return nil
}

// now 保持各原取时点，构造时可注入同一时钟来源。
func (s *Queries) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
