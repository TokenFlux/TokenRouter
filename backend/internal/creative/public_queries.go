// 查询与创建共用同一任务仓储和暂存实例，HTTP 直接消费 Public。
package creative

import "context"

func (s *Public) queries() *Queries {
	return &Queries{Now: s.Now, Repo: s.Repo, TransientStore: s.TransientStore, Enabled: s.Enabled, Observe: s.Observe}
}
func (s *Public) GetRun(ctx context.Context, scope CreativeRunScope, id string) (*CreativeRunPublic, error) {
	return s.queries().GetRun(ctx, scope, id)
}
func (s *Public) ListRuns(ctx context.Context, scope CreativeRunScope, filter CreativeRunFilter) (*CreativeListRunsResponse, error) {
	return s.queries().ListRuns(ctx, scope, filter)
}
func (s *Public) GetOutputContent(ctx context.Context, scope CreativeRunScope, id string, index int) (*CreativeOutputContent, error) {
	return s.queries().GetOutputContent(ctx, scope, id, index)
}
func (s *Public) AckOutput(ctx context.Context, scope CreativeRunScope, id string, index int) error {
	return s.queries().AckOutput(ctx, scope, id, index)
}
