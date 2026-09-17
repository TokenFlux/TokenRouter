package postgres

import (
	"context"
	"strings"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/creativerun"
	"github.com/TokenFlux/TokenRouter/ent/creativerunoutput"
	service "github.com/TokenFlux/TokenRouter/internal/creative"
)

// creativeRunRepository 基于 Ent 实现创作台任务元数据仓储。
// 状态转换统一走 version 乐观锁：WHERE run_id = ? AND version = ?，冲突即视为并发推进。
type creativeRunRepository struct {
	client *dbent.Client
}

// NewCreativeRunRepository 创建创作台任务仓储。
func NewCreativeRunRepository(client *dbent.Client) service.CreativeRunRepository {
	return &creativeRunRepository{client: client}
}

func (r *creativeRunRepository) CreateCreativeRun(ctx context.Context, params service.CreateCreativeRunParams) (*service.CreativeRun, error) {
	if params.RunID == "" {
		runID, err := service.NewCreativeRunID()
		if err != nil {
			return nil, err
		}
		params.RunID = runID
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	builder := tx.CreativeRun.Create().
		SetRunID(params.RunID).
		SetUserID(params.UserID).
		SetGroupID(params.GroupID).
		SetAPIKeyID(params.APIKeyID).
		SetModel(params.Model).
		SetRequestedModel(params.RequestedModel).
		SetOperation(params.Operation).
		SetRequestedOutputCount(params.RequestedOutputCount).
		SetImageSize(params.ImageSize).
		SetAspectRatio(params.AspectRatio).
		SetResponseMimeType(params.ResponseMIMEType).
		SetPromptHash(params.PromptHash).
		SetRequestFingerprint(params.RequestFingerprint).
		SetStatus(service.CreativeRunStatusQueued).
		SetEstimatedCost(params.EstimatedCost).
		SetHoldAmount(params.HoldAmount).
		SetBaseUnitPrice(params.BaseUnitPrice).
		SetSubscriptionRateMultiplier(params.SubscriptionRateMultiplier).
		SetBalanceRateMultiplier(params.BalanceRateMultiplier).
		SetPlanGroupRateMultiplierEnabled(params.PlanGroupRateEnabled).
		SetProvisioningPhase(service.CreativeProvisioningPhaseCreated)
	if params.WorkspaceID != "" {
		builder.SetWorkspaceID(params.WorkspaceID)
	}
	if params.IdempotencyKey != nil {
		builder.SetIdempotencyKey(*params.IdempotencyKey)
	}
	entity, err := builder.Save(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, nil, service.ErrCreativeRunExists)
	}
	// 同事务创建全部 pending 输出行，保证任务与输出元数据原子出现。
	outputBuilders := make([]*dbent.CreativeRunOutputCreate, 0, params.RequestedOutputCount)
	for index := 0; index < params.RequestedOutputCount; index++ {
		outputBuilders = append(outputBuilders, tx.CreativeRunOutput.Create().
			SetRunID(params.RunID).
			SetOutputIndex(index).
			SetStatus(service.CreativeRunOutputStatusPending))
	}
	if len(outputBuilders) > 0 {
		if err := tx.CreativeRunOutput.CreateBulk(outputBuilders...).Exec(ctx); err != nil {
			return nil, translatePersistenceError(err, nil, service.ErrCreativeOutputExists)
		}
	}
	// 创建任务与 provisioning outbox 在同一事务提交，避免数据库已有 queued 任务却没有入队意图。
	if _, err := tx.CreativeRunOutbox.Create().
		SetRunID(params.RunID).
		SetOperation(string(service.CreativeRunOutboxProvision)).
		SetStatus(string(service.CreativeRunOutboxPending)).
		Save(ctx); err != nil {
		return nil, translatePersistenceError(err, nil, service.ErrCreativeRunExists)
	}
	if err := tx.Commit(); err != nil {
		return nil, translatePersistenceError(err, nil, service.ErrCreativeRunExists)
	}
	return creativeRunEntityToService(entity), nil
}

func (r *creativeRunRepository) GetCreativeRunByRunID(ctx context.Context, runID string) (*service.CreativeRun, error) {
	entity, err := r.client.CreativeRun.Query().
		Where(creativerun.RunIDEQ(runID)).
		Only(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrCreativeRunNotFound, nil)
	}
	return creativeRunEntityToService(entity), nil
}

func (r *creativeRunRepository) GetCreativeRunByRunIDForOwner(ctx context.Context, scope service.CreativeRunScope, runID string) (*service.CreativeRun, error) {
	entity, err := r.client.CreativeRun.Query().
		Where(
			creativerun.RunIDEQ(runID),
			creativerun.UserIDEQ(scope.UserID),
			creativerun.WorkspaceIDEQ(scope.WorkspaceID),
		).
		Only(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrCreativeRunNotFound, nil)
	}
	return creativeRunEntityToService(entity), nil
}

func (r *creativeRunRepository) GetCreativeRunByIdempotencyKey(ctx context.Context, scope service.CreativeRunScope, key string) (*service.CreativeRun, error) {
	entity, err := r.client.CreativeRun.Query().
		Where(
			creativerun.UserIDEQ(scope.UserID),
			creativerun.WorkspaceIDEQ(scope.WorkspaceID),
			creativerun.IdempotencyKeyEQ(key),
		).
		Order(dbent.Desc(creativerun.FieldID)).
		First(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrCreativeRunNotFound, nil)
	}
	return creativeRunEntityToService(entity), nil
}

func (r *creativeRunRepository) ListCreativeRunsForOwner(ctx context.Context, scope service.CreativeRunScope, filter service.CreativeRunFilter) ([]*service.CreativeRun, error) {
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	query := r.client.CreativeRun.Query().
		Where(
			creativerun.UserIDEQ(scope.UserID),
			creativerun.WorkspaceIDEQ(scope.WorkspaceID),
		)
	if filter.Status != "" {
		if filter.Status == "active" {
			query = query.Where(creativerun.StatusIn(
				service.CreativeRunStatusQueued,
				service.CreativeRunStatusRunning,
				service.CreativeRunStatusProviderSucceeded,
				service.CreativeRunStatusSettlementPending,
			))
		} else {
			query = query.Where(creativerun.StatusEQ(filter.Status))
		}
	}
	entities, err := query.
		Order(dbent.Desc(creativerun.FieldCreatedAt), dbent.Desc(creativerun.FieldID)).
		Limit(limit).
		Offset(filter.Offset).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*service.CreativeRun, 0, len(entities))
	for _, entity := range entities {
		out = append(out, creativeRunEntityToService(entity))
	}
	return out, nil
}

// TransitionCreativeRunStatus 先读后改：CanTransition 校验 + version 乐观锁。
func (r *creativeRunRepository) TransitionCreativeRunStatus(ctx context.Context, runID, toStatus string, opts service.CreativeRunTransitionOptions) error {
	now := time.Now()
	if opts.Now != nil {
		now = *opts.Now
	}
	current, err := r.client.CreativeRun.Query().
		Where(creativerun.RunIDEQ(runID)).
		Only(ctx)
	if err != nil {
		return translatePersistenceError(err, service.ErrCreativeRunNotFound, nil)
	}
	if !service.CanTransitionCreativeRun(current.Status, toStatus) {
		return service.ErrCreativeInvalidTransition
	}
	builder := r.client.CreativeRun.Update().
		Where(
			creativerun.RunIDEQ(runID),
			creativerun.VersionEQ(current.Version),
		).
		SetStatus(toStatus).
		SetVersion(current.Version + 1).
		SetUpdatedAt(now)
	if toStatus == service.CreativeRunStatusRunning {
		builder.SetStartedAt(now)
	}
	if service.IsTerminalCreativeRunStatus(toStatus) {
		builder.SetCompletedAt(now)
	}
	if toStatus == service.CreativeRunStatusCancelled {
		builder.SetCancelledAt(now)
	}
	if toStatus == service.CreativeRunStatusFailed {
		if opts.ErrorCode != nil {
			builder.SetErrorCode(*opts.ErrorCode)
		}
		if opts.ErrorMessage != nil {
			builder.SetErrorMessage(*opts.ErrorMessage)
		}
	}
	if toStatus == service.CreativeRunStatusReleasePending {
		target := opts.ReleaseTargetStatus
		if target == "" {
			target = service.CreativeRunStatusFailed
		}
		builder.SetReleaseTargetStatus(target)
		if opts.ErrorCode != nil {
			builder.SetErrorCode(*opts.ErrorCode)
		}
		if opts.ErrorMessage != nil {
			builder.SetErrorMessage(*opts.ErrorMessage)
		}
	}
	if toStatus == service.CreativeRunStatusResultLost {
		if opts.ErrorCode != nil {
			builder.SetErrorCode(*opts.ErrorCode)
		}
		if opts.ErrorMessage != nil {
			builder.SetErrorMessage(*opts.ErrorMessage)
		}
	}
	affected, err := builder.Save(ctx)
	if err != nil {
		return err
	}
	if affected == 0 {
		// version 冲突：任务已被并发推进，按非法转换处理（调用方通常重读状态）。
		return service.ErrCreativeInvalidTransition
	}
	return nil
}

// MarkCreativeRunRunning 幂等推进 queued -> running 并回填执行账号。
func (r *creativeRunRepository) MarkCreativeRunRunning(ctx context.Context, runID string, accountID int64, now time.Time) error {
	current, err := r.client.CreativeRun.Query().
		Where(creativerun.RunIDEQ(runID)).
		Only(ctx)
	if err != nil {
		return translatePersistenceError(err, service.ErrCreativeRunNotFound, nil)
	}
	if current.Status == service.CreativeRunStatusRunning {
		// 重复执行（worker 重试）视为成功，但确保账号已回填。
		if accountID > 0 && (current.AccountID == nil || *current.AccountID != accountID) {
			_, err := r.client.CreativeRun.Update().
				Where(creativerun.RunIDEQ(runID)).
				SetAccountID(accountID).
				SetUpdatedAt(now).
				Save(ctx)
			return err
		}
		return nil
	}
	if !service.CanTransitionCreativeRun(current.Status, service.CreativeRunStatusRunning) {
		return service.ErrCreativeInvalidTransition
	}
	builder := r.client.CreativeRun.Update().
		Where(
			creativerun.RunIDEQ(runID),
			creativerun.VersionEQ(current.Version),
		).
		SetStatus(service.CreativeRunStatusRunning).
		SetVersion(current.Version + 1).
		SetStartedAt(now).
		SetUpdatedAt(now)
	if accountID > 0 {
		builder.SetAccountID(accountID)
	}
	affected, err := builder.Save(ctx)
	if err != nil {
		return err
	}
	if affected == 0 {
		return service.ErrCreativeInvalidTransition
	}
	return nil
}

// SetCreativeRunAccountID 持久化执行器最终选中的真实上游账号。
func (r *creativeRunRepository) SetCreativeRunAccountID(ctx context.Context, runID string, accountID int64, now time.Time) error {
	if accountID <= 0 {
		return nil
	}
	affected, err := r.client.CreativeRun.Update().
		Where(creativerun.RunIDEQ(runID)).
		SetAccountID(accountID).
		SetUpdatedAt(now).
		Save(ctx)
	if err != nil {
		return err
	}
	if affected == 0 {
		return service.ErrCreativeRunNotFound
	}
	return nil
}

// MarkCreativeRunSucceeded 记录实际成本并进入 succeeded，仅在 running 时生效。
func (r *creativeRunRepository) MarkCreativeRunSucceeded(ctx context.Context, runID string, actualCost float64, now time.Time) error {
	affected, err := r.client.CreativeRun.Update().
		Where(
			creativerun.RunIDEQ(runID),
			creativerun.StatusIn(
				service.CreativeRunStatusRunning,
				service.CreativeRunStatusProviderSucceeded,
				service.CreativeRunStatusSettlementPending,
			),
		).
		SetStatus(service.CreativeRunStatusSucceeded).
		SetActualCost(actualCost).
		SetCompletedAt(now).
		SetUpdatedAt(now).
		AddVersion(1).
		Save(ctx)
	if err != nil {
		return err
	}
	if affected == 0 {
		current, getErr := r.client.CreativeRun.Query().
			Where(creativerun.RunIDEQ(runID)).
			Only(ctx)
		if getErr != nil {
			return translatePersistenceError(getErr, service.ErrCreativeRunNotFound, nil)
		}
		if current.Status == service.CreativeRunStatusSucceeded || current.Status == service.CreativeRunStatusCancelled {
			return nil
		}
		return service.ErrCreativeInvalidTransition
	}
	return nil
}

// MarkCreativeRunProviderSucceeded 在输出已经写入 Redis 后记录 provider 成功，
// 后续只允许 settlement worker 重试计费和落库，不重新调用 provider。
func (r *creativeRunRepository) MarkCreativeRunProviderSucceeded(ctx context.Context, runID string, accountID int64, now time.Time) error {
	current, err := r.client.CreativeRun.Query().Where(creativerun.RunIDEQ(runID)).Only(ctx)
	if err != nil {
		return translatePersistenceError(err, service.ErrCreativeRunNotFound, nil)
	}
	if current.Status == service.CreativeRunStatusSucceeded || current.Status == service.CreativeRunStatusResultLost {
		return nil
	}
	builder := r.client.CreativeRun.Update().
		Where(creativerun.RunIDEQ(runID), creativerun.VersionEQ(current.Version)).
		SetProviderResultRecordedAt(now).
		SetUpdatedAt(now).
		AddVersion(1)
	if accountID > 0 {
		builder.SetAccountID(accountID)
	}
	if current.Status == service.CreativeRunStatusRunning {
		builder.SetStatus(service.CreativeRunStatusProviderSucceeded)
	}
	if _, err := builder.Save(ctx); err != nil {
		return err
	}
	return nil
}

// UpdateCreativeRunOutput 幂等更新输出行：已 acked 的行不允许被覆盖。
func (r *creativeRunRepository) UpdateCreativeRunOutput(ctx context.Context, runID string, outputIndex int, status, mimeType string, byteSize int64, transientExpiresAt *time.Time, errorCode, errorMessage string) error {
	builder := r.client.CreativeRunOutput.Update().
		Where(
			creativerunoutput.RunIDEQ(runID),
			creativerunoutput.OutputIndexEQ(outputIndex),
			// acked 是客户端已确认接收的终态，任何后续更新都不得覆盖。
			creativerunoutput.StatusNEQ(service.CreativeRunOutputStatusAcked),
		).
		SetStatus(status).
		SetUpdatedAt(time.Now())
	if mimeType != "" {
		builder.SetMimeType(mimeType)
	}
	if byteSize > 0 {
		builder.SetByteSize(byteSize)
	}
	if transientExpiresAt != nil {
		builder.SetTransientExpiresAt(*transientExpiresAt)
	}
	if errorCode != "" {
		builder.SetErrorCode(errorCode)
	}
	if errorMessage != "" {
		builder.SetErrorMessage(errorMessage)
	}
	affected, err := builder.Save(ctx)
	if err != nil {
		return err
	}
	if affected == 0 {
		// 行不存在或已 acked：已 acked 视为幂等成功，不存在才报错。
		exists, existsErr := r.client.CreativeRunOutput.Query().
			Where(
				creativerunoutput.RunIDEQ(runID),
				creativerunoutput.OutputIndexEQ(outputIndex),
			).
			Exist(ctx)
		if existsErr != nil {
			return existsErr
		}
		if !exists {
			return service.ErrCreativeOutputNotFound
		}
	}
	return nil
}

func (r *creativeRunRepository) GetCreativeRunOutput(ctx context.Context, runID string, outputIndex int) (*service.CreativeRunOutput, error) {
	entity, err := r.client.CreativeRunOutput.Query().
		Where(
			creativerunoutput.RunIDEQ(runID),
			creativerunoutput.OutputIndexEQ(outputIndex),
		).
		Only(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, service.ErrCreativeOutputNotFound, nil)
	}
	return creativeRunOutputEntityToService(entity), nil
}

func (r *creativeRunRepository) ListCreativeRunOutputs(ctx context.Context, runID string) ([]*service.CreativeRunOutput, error) {
	entities, err := r.client.CreativeRunOutput.Query().
		Where(creativerunoutput.RunIDEQ(runID)).
		Order(dbent.Asc(creativerunoutput.FieldOutputIndex)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*service.CreativeRunOutput, 0, len(entities))
	for _, entity := range entities {
		out = append(out, creativeRunOutputEntityToService(entity))
	}
	return out, nil
}

// ListCreativeRunOutputsForRuns 一次读取多个 run 的输出元数据，供历史/活动列表使用。
func (r *creativeRunRepository) ListCreativeRunOutputsForRuns(ctx context.Context, runIDs []string) (map[string][]*service.CreativeRunOutput, error) {
	result := make(map[string][]*service.CreativeRunOutput, len(runIDs))
	if len(runIDs) == 0 {
		return result, nil
	}
	entities, err := r.client.CreativeRunOutput.Query().
		Where(creativerunoutput.RunIDIn(runIDs...)).
		Order(dbent.Asc(creativerunoutput.FieldRunID), dbent.Asc(creativerunoutput.FieldOutputIndex)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	for _, entity := range entities {
		result[entity.RunID] = append(result[entity.RunID], creativeRunOutputEntityToService(entity))
	}
	return result, nil
}

// MarkCreativeRunOutputAcked 幂等标记 acked：只在 succeeded 上生效，重复 ack 无副作用。
func (r *creativeRunRepository) MarkCreativeRunOutputAcked(ctx context.Context, runID string, outputIndex int, now time.Time) error {
	affected, err := r.client.CreativeRunOutput.Update().
		Where(
			creativerunoutput.RunIDEQ(runID),
			creativerunoutput.OutputIndexEQ(outputIndex),
			creativerunoutput.StatusEQ(service.CreativeRunOutputStatusSucceeded),
		).
		SetStatus(service.CreativeRunOutputStatusAcked).
		SetAckedAt(now).
		SetUpdatedAt(now).
		Save(ctx)
	if err != nil {
		return err
	}
	if affected == 0 {
		exists, existsErr := r.client.CreativeRunOutput.Query().
			Where(
				creativerunoutput.RunIDEQ(runID),
				creativerunoutput.OutputIndexEQ(outputIndex),
			).
			Only(ctx)
		if existsErr != nil {
			return translatePersistenceError(existsErr, service.ErrCreativeOutputNotFound, nil)
		}
		if exists.Status == service.CreativeRunOutputStatusAcked {
			return nil
		}
		return service.ErrCreativeOutputNotReady
	}
	return nil
}

// ListCreativeRunsDueForTransientCleanup 返回终态且完成时间早于 cutoff 的任务，供第二阶段清理暂存。
func (r *creativeRunRepository) ListCreativeRunsDueForTransientCleanup(ctx context.Context, cutoff time.Time, limit int) ([]*service.CreativeRun, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	entities, err := r.client.CreativeRun.Query().
		Where(
			creativerun.StatusIn(
				service.CreativeRunStatusSucceeded,
				service.CreativeRunStatusFailed,
				service.CreativeRunStatusCancelled,
				service.CreativeRunStatusResultLost,
			),
			creativerun.CompletedAtLTE(cutoff),
		).
		Order(dbent.Asc(creativerun.FieldID)).
		Limit(limit).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*service.CreativeRun, 0, len(entities))
	for _, entity := range entities {
		out = append(out, creativeRunEntityToService(entity))
	}
	return out, nil
}

// IncrementCreativeRunAttempt 原子递增 attempt_count 并返回最新值。
func (r *creativeRunRepository) IncrementCreativeRunAttempt(ctx context.Context, runID string) (int, error) {
	affected, err := r.client.CreativeRun.Update().
		Where(creativerun.RunIDEQ(runID)).
		AddAttemptCount(1).
		SetUpdatedAt(time.Now()).
		Save(ctx)
	if err != nil {
		return 0, err
	}
	if affected == 0 {
		return 0, service.ErrCreativeRunNotFound
	}
	entity, err := r.client.CreativeRun.Query().
		Where(creativerun.RunIDEQ(runID)).
		Select(creativerun.FieldAttemptCount).
		Only(ctx)
	if err != nil {
		return 0, translatePersistenceError(err, service.ErrCreativeRunNotFound, nil)
	}
	return entity.AttemptCount, nil
}

func (r *creativeRunRepository) IncrementCreativeRunSettlementAttempt(ctx context.Context, runID string) (int, error) {
	return r.incrementCreativeRunCounter(ctx, runID, true)
}

func (r *creativeRunRepository) IncrementCreativeRunReleaseAttempt(ctx context.Context, runID string) (int, error) {
	return r.incrementCreativeRunCounter(ctx, runID, false)
}

func (r *creativeRunRepository) incrementCreativeRunCounter(ctx context.Context, runID string, settlement bool) (int, error) {
	update := r.client.CreativeRun.Update().Where(creativerun.RunIDEQ(runID)).SetUpdatedAt(time.Now())
	if settlement {
		update.AddSettlementAttemptCount(1)
	} else {
		update.AddReleaseAttemptCount(1)
	}
	if _, err := update.Save(ctx); err != nil {
		return 0, err
	}
	query := r.client.CreativeRun.Query().Where(creativerun.RunIDEQ(runID))
	if settlement {
		entity, err := query.Select(creativerun.FieldSettlementAttemptCount).Only(ctx)
		if err != nil {
			return 0, translatePersistenceError(err, service.ErrCreativeRunNotFound, nil)
		}
		return entity.SettlementAttemptCount, nil
	}
	entity, err := query.Select(creativerun.FieldReleaseAttemptCount).Only(ctx)
	if err != nil {
		return 0, translatePersistenceError(err, service.ErrCreativeRunNotFound, nil)
	}
	return entity.ReleaseAttemptCount, nil
}

func (r *creativeRunRepository) SetCreativeRunProvisioningPhase(ctx context.Context, runID, phase string) error {
	if strings.TrimSpace(phase) == "" {
		return service.ErrCreativeInvalidParams
	}
	affected, err := r.client.CreativeRun.Update().
		Where(creativerun.RunIDEQ(runID)).
		SetProvisioningPhase(phase).
		SetUpdatedAt(time.Now()).
		Save(ctx)
	if err != nil {
		return err
	}
	if affected == 0 {
		return service.ErrCreativeRunNotFound
	}
	return nil
}

// SetCreativeRunAllowanceReserved 持久化预占事实，释放成功后复位。
func (r *creativeRunRepository) SetCreativeRunAllowanceReserved(ctx context.Context, runID string, reserved bool) error {
	affected, err := r.client.CreativeRun.Update().
		Where(creativerun.RunIDEQ(runID)).
		SetAllowanceReserved(reserved).
		SetUpdatedAt(time.Now()).
		Save(ctx)
	if err != nil {
		return err
	}
	if affected == 0 {
		return service.ErrCreativeRunNotFound
	}
	return nil
}

func (r *creativeRunRepository) SetCreativeRunReconcileError(ctx context.Context, runID, message string, next time.Time) error {
	builder := r.client.CreativeRun.Update().
		Where(creativerun.RunIDEQ(runID)).
		SetUpdatedAt(time.Now())
	if strings.TrimSpace(message) == "" {
		builder.ClearLastReconcileError()
	} else {
		builder.SetLastReconcileError(message)
	}
	if next.IsZero() {
		builder.ClearNextReconcileAt()
	} else {
		builder.SetNextReconcileAt(next)
	}
	affected, err := builder.Save(ctx)
	if err != nil {
		return err
	}
	if affected == 0 {
		return service.ErrCreativeRunNotFound
	}
	return nil
}

func creativeRunEntityToService(entity *dbent.CreativeRun) *service.CreativeRun {
	if entity == nil {
		return nil
	}
	requestedModel := entity.RequestedModel
	if requestedModel == "" {
		requestedModel = entity.Model
	}
	return &service.CreativeRun{
		ID:                          entity.ID,
		RunID:                       entity.RunID,
		UserID:                      entity.UserID,
		WorkspaceID:                 entity.WorkspaceID,
		GroupID:                     entity.GroupID,
		APIKeyID:                    entity.APIKeyID,
		AccountID:                   entity.AccountID,
		Model:                       entity.Model,
		RequestedModel:              requestedModel,
		Operation:                   entity.Operation,
		RequestedOutputCount:        entity.RequestedOutputCount,
		ImageSize:                   entity.ImageSize,
		AspectRatio:                 entity.AspectRatio,
		ResponseMIMEType:            entity.ResponseMimeType,
		PromptHash:                  entity.PromptHash,
		RequestFingerprint:          entity.RequestFingerprint,
		IdempotencyKey:              entity.IdempotencyKey,
		Status:                      entity.Status,
		EstimatedCost:               entity.EstimatedCost,
		HoldAmount:                  entity.HoldAmount,
		ActualCost:                  entity.ActualCost,
		BalanceHoldAmount:           entity.BalanceHoldAmount,
		SubscriptionHoldAllocations: entity.SubscriptionHoldAllocations,
		AllowanceReserved:           entity.AllowanceReserved,
		BaseUnitPrice:               entity.BaseUnitPrice,
		SubscriptionRateMultiplier:  entity.SubscriptionRateMultiplier,
		BalanceRateMultiplier:       entity.BalanceRateMultiplier,
		PlanGroupRateEnabled:        entity.PlanGroupRateMultiplierEnabled,
		ErrorCode:                   entity.ErrorCode,
		ErrorMessage:                entity.ErrorMessage,
		ReleaseTargetStatus:         entity.ReleaseTargetStatus,
		AttemptCount:                entity.AttemptCount,
		SettlementAttemptCount:      entity.SettlementAttemptCount,
		ReleaseAttemptCount:         entity.ReleaseAttemptCount,
		ProvisioningPhase:           entity.ProvisioningPhase,
		ProviderResultRecordedAt:    entity.ProviderResultRecordedAt,
		NextReconcileAt:             entity.NextReconcileAt,
		LastReconcileError:          entity.LastReconcileError,
		Version:                     entity.Version,
		CreatedAt:                   entity.CreatedAt,
		UpdatedAt:                   entity.UpdatedAt,
		StartedAt:                   entity.StartedAt,
		CompletedAt:                 entity.CompletedAt,
		CancelledAt:                 entity.CancelledAt,
	}
}

func creativeRunOutputEntityToService(entity *dbent.CreativeRunOutput) *service.CreativeRunOutput {
	if entity == nil {
		return nil
	}
	return &service.CreativeRunOutput{
		ID:                 entity.ID,
		RunID:              entity.RunID,
		OutputIndex:        entity.OutputIndex,
		Status:             entity.Status,
		MimeType:           entity.MimeType,
		ByteSize:           entity.ByteSize,
		TransientExpiresAt: entity.TransientExpiresAt,
		AckedAt:            entity.AckedAt,
		ErrorCode:          entity.ErrorCode,
		ErrorMessage:       entity.ErrorMessage,
		CreatedAt:          entity.CreatedAt,
		UpdatedAt:          entity.UpdatedAt,
	}
}
