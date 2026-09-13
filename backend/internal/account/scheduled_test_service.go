// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	context "context"
	fmt "fmt"
	time "time"
)

// ScheduledTestService 拥有账号计划测试的管理与结果保留规则。
type ScheduledTestService struct {
	options    ScheduledTestOptions
	planRepo   ScheduledTestPlanRepository
	resultRepo ScheduledTestResultRepository
}

// NewScheduledTestService 仅保存依赖，不启动后台任务。
func NewScheduledTestService(
	planRepo ScheduledTestPlanRepository,
	resultRepo ScheduledTestResultRepository,
	options ScheduledTestOptions,
) *ScheduledTestService {
	return &ScheduledTestService{options: options,
		planRepo:   planRepo,
		resultRepo: resultRepo,
	}
}

// CreatePlan 先校验 cron 并计算下次时间，再持久化计划。
func (s *ScheduledTestService) CreatePlan(ctx context.Context, plan *ScheduledTestPlan) (*ScheduledTestPlan, error) {
	nextRun, err := s.options.NextRun(plan.CronExpression, s.options.Now())
	if err != nil {
		return nil, fmt.Errorf("invalid cron expression: %w", err)
	}
	plan.NextRunAt = &nextRun

	if plan.MaxResults <= 0 {
		plan.MaxResults = 50
	}

	return s.planRepo.Create(ctx, plan)
}

// GetPlan 按 ID 读取计划。
func (s *ScheduledTestService) GetPlan(ctx context.Context, id int64) (*ScheduledTestPlan, error) {
	return s.planRepo.GetByID(ctx, id)
}

// ListPlansByAccount 返回指定账号的全部计划。
func (s *ScheduledTestService) ListPlansByAccount(ctx context.Context, accountID int64) ([]*ScheduledTestPlan, error) {
	return s.planRepo.ListByAccountID(ctx, accountID)
}

// UpdatePlan 校验 cron 后更新计划与下次执行时间。
func (s *ScheduledTestService) UpdatePlan(ctx context.Context, plan *ScheduledTestPlan) (*ScheduledTestPlan, error) {
	nextRun, err := s.options.NextRun(plan.CronExpression, s.options.Now())
	if err != nil {
		return nil, fmt.Errorf("invalid cron expression: %w", err)
	}
	plan.NextRunAt = &nextRun

	return s.planRepo.Update(ctx, plan)
}

// DeletePlan 删除计划，结果由原数据库约束级联删除。
func (s *ScheduledTestService) DeletePlan(ctx context.Context, id int64) error {
	return s.planRepo.Delete(ctx, id)
}

// ListResults 返回计划最近的测试结果。
func (s *ScheduledTestService) ListResults(ctx context.Context, planID int64, limit int) ([]*ScheduledTestResult, error) {
	if limit <= 0 {
		limit = 50
	}
	return s.resultRepo.ListByPlanID(ctx, planID, limit)
}

// SaveResult 先保存结果，再清理超出保留数的旧结果，保留原分步失败语义。
func (s *ScheduledTestService) SaveResult(ctx context.Context, planID int64, maxResults int, result *ScheduledTestResult) error {
	result.PlanID = planID
	if _, err := s.resultRepo.Create(ctx, result); err != nil {
		return err
	}
	return s.resultRepo.PruneOldResults(ctx, planID, maxResults)
}

type ScheduledTestOptions struct {
	Now     func() time.Time
	NextRun func(string, time.Time) (time.Time, error)
}
