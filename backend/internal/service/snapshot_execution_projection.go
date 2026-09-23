package service

import (
	"context"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// 剩余旧执行链只在读取边界转换账号，快照核心和生产缓存不持有旧实体。
func readSnapshotAccount(ctx context.Context, source *scheduler.SnapshotService, id int64) (*gatewayprovider.ExecutionAccount, error) {
	value, err := source.GetAccount(ctx, id)
	if err != nil {
		return nil, err
	}
	return LegacySnapshotValue(value)
}

func readSnapshotAccounts(ctx context.Context, source *scheduler.SnapshotService, group *int64, platform string, forced bool) ([]gatewayprovider.ExecutionAccount, bool, error) {
	values, mixed, err := source.ListSchedulableAccounts(ctx, group, platform, forced)
	if err != nil {
		return nil, mixed, err
	}
	records, err := LegacySnapshotValues(values)
	return records, mixed, err
}

// BindSchedulingGroups 在构造时注入原分组读取，保持一次查询及失败语义。
func (s *OpenAIGatewayService) BindSchedulingGroups(read func(context.Context, int64) (*routing.Group, error)) {
	s.schedulingGroups = read
}

func (s *OpenAIGatewayService) readSchedulingGroup(ctx context.Context, id int64) (*routing.Group, error) {
	if s.schedulingGroups == nil {
		return nil, nil
	}
	return s.schedulingGroups(ctx, id)
}

func (s *GeminiMessagesCompatService) readSchedulingGroup(ctx context.Context, id int64) (*routing.Group, error) {
	if s.groupRepo == nil {
		return nil, nil
	}
	return s.groupRepo.GetByID(ctx, id)
}

func (s *GatewayService) readSchedulingGroup(ctx context.Context, id int64) (*routing.Group, error) {
	if s.groupRepo == nil {
		return nil, nil
	}
	return s.groupRepo.GetByID(ctx, id)
}
