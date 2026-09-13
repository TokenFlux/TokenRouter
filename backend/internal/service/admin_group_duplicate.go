// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
)

func (s *adminServiceImpl) RecoverDuplicateGroup(ctx context.Context, id int64, actorScope, operationKey string) (*Group, error) {
	value, err := s.routingAdmin.RecoverDuplicateGroup(ctx, id, actorScope, operationKey)
	return GroupFromRouting(value), err
}

func (s *adminServiceImpl) DuplicateGroup(ctx context.Context, id int64, actorScope, operationKey string) (*Group, error) {
	value, err := s.routingAdmin.DuplicateGroup(ctx, id, actorScope, operationKey)
	return GroupFromRouting(value), err
}
