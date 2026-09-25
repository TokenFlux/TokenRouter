// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	"context"
	"slices"
)

// ModelSyncService 拥有请求触发的模型查询生命周期，供应商解析通过端口执行。
type ModelSyncService struct {
	fetch    func(context.Context, *Record) ([]string, error)
	activity operationActivity
}

func NewModelSyncService(fetch func(context.Context, *Record) ([]string, error)) *ModelSyncService {
	if fetch == nil {
		return nil
	}
	return &ModelSyncService{fetch: fetch}
}
func (s *ModelSyncService) Fetch(ctx context.Context, v *Record) ([]string, error) {
	ctx, finish, err := s.activity.begin(ctx, ErrRefreshStopped)
	if err != nil {
		return nil, err
	}
	defer finish()
	models, err := s.fetch(ctx, CloneRecord(v))
	return slices.Clone(models), err
}
func (s *ModelSyncService) StopContext(ctx context.Context) error {
	return s.activity.stop(ctx, "account model list requests")
}
