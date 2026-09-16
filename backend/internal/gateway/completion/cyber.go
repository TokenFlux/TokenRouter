package completion

import (
	"context"
	"strings"
)

// RecordCyber 保留错误路径的补记资格和原 RecordOpenAI 计费/零费用规则。
// 调用者在提交前冻结 Input；这里不重试转发，也不绕开唯一记录实现。
func (s *Recorder) RecordCyber(ctx context.Context, in *Input) {
	if s == nil || in == nil || in.APIKey == nil || in.User == nil || in.Account == nil || in.Result == nil || strings.TrimSpace(in.Result.Model) == "" {
		return
	}
	snapshot := Snapshot(in)
	snapshot.Result.Model = strings.TrimSpace(snapshot.Result.Model)
	snapshot.CyberBlocked = true
	if err := s.Record(ctx, snapshot, true); err != nil {
		s.printf("service.openai_gateway", "cyber usage record failed: request_id=%s err=%v", snapshot.Result.RequestID, err)
	}
}
