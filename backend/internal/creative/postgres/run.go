package postgres

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/creativerun"
	"github.com/TokenFlux/TokenRouter/ent/creativerunoutput"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// run.go stores creative runs and output metadata and applies versioned task
// transitions.
// Creative run storage persists task identity, hashed inputs, funding
// snapshots, and output metadata while image bytes remain transient. Creating
// a queued run, its pending output rows, and provisioning outbox entry shares
// one transaction, making task provisioning recoverable when later funding,
// transient writes, or queue admission fail.
//
// User queries compare both user and workspace, while background processing
// uses the internal run identifier. Version comparisons fence status
// transitions against independently advancing writers; specialized identity
// and counter updates have their own predicates. Acknowledged output rows
// resist later delivery updates, and optional metadata fields are assigned
// only when their input values are present. Funding participant writes use the
// billing transaction separately from these ordinary Ent operations.

// creativeRunRepository stores creative run metadata through Ent.
// State transitions using version checks reject updates when another writer
// advances the run.
type creativeRunRepository struct {
	// client is retained from application wiring or a caller transaction adapter.
	// Its owner controls lifetime; operations here use the supplied client
	// connection.
	client *dbent.Client // retained run metadata Ent client
}

// translatePersistenceError maps recognized missing-row and unique-conflict
// errors to supplied domain errors. Nil error returns nil; absent mapping
// arguments and other database failures preserve the original error for
// callers.
func translatePersistenceError(err error, notFound, conflict *apperror.ApplicationError) error {
	if err == nil {
		return nil
	}
	if notFound != nil && (errors.Is(err, sql.ErrNoRows) || dbent.IsNotFound(err)) {
		return notFound.WithCause(err)
	}
	if conflict != nil && postgresinfra.IsUniqueConstraintViolation(err) {
		return conflict.WithCause(err)
	}
	return err
}

// NewCreativeRunRepository retains the Ent client for creative metadata
// persistence. Callers supply a usable client and own its lifetime; this
// constructor creates no connection or transaction itself.
func NewCreativeRunRepository(client *dbent.Client) creative.CreativeRunRepository {
	return &creativeRunRepository{client: client}
}

// CreateCreativeRun creates queued metadata, pending output rows, and
// provisioning recovery in one transaction. A missing RunID is generated here,
// while other parameters come from validated submission and funding snapshots.
//
// Only nonblank workspace values and nonnil idempotency keys are assigned to
// their optional columns. Output indexes range from zero through
// RequestedOutputCount minus one, and their initial state is pending. Any
// task, output, outbox, or commit error prevents the complete creation from
// being returned; recognized unique conflicts use the corresponding run or
// output error.
func (r *creativeRunRepository) CreateCreativeRun(ctx context.Context, params creative.CreateCreativeRunParams) (*creative.CreativeRun, error) {
	if params.RunID == "" {
		runID, err := creative.NewCreativeRunID()
		if err != nil {
			return nil, err
		}
		params.RunID = runID
	}
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	// Run creation, pending output metadata, and provisioning recovery share this
	// transaction. Roll back incomplete work on every early exit; discard rollback
	// errors after a commit has already completed the transaction.
	defer func() { _ = tx.Rollback() }()

	builder := tx.CreativeRun.Create().
		SetRunID(params.RunID).
		SetUserID(params.UserID).
		SetGroupID(params.GroupID).
		SetAPIKeyID(params.APIKeyID).
		SetPlatform(params.Platform).
		SetModel(params.Model).
		SetRequestedModel(params.RequestedModel).
		SetOperation(params.Operation).
		SetRequestedOutputCount(params.RequestedOutputCount).
		SetImageSize(params.ImageSize).
		SetFrameCount(params.FrameCount).
		SetFrameDurationMs(params.FrameDurationMS).
		SetAspectRatio(params.AspectRatio).
		SetResponseMimeType(params.ResponseMIMEType).
		SetPromptHash(params.PromptHash).
		SetRequestFingerprint(params.RequestFingerprint).
		SetStatus(creative.CreativeRunStatusQueued).
		SetEstimatedCost(params.EstimatedCost).
		SetHoldAmount(params.HoldAmount).
		SetBaseUnitPrice(params.BaseUnitPrice).
		SetSubscriptionRateMultiplier(params.SubscriptionRateMultiplier).
		SetBalanceRateMultiplier(params.BalanceRateMultiplier).
		SetPlanGroupRateMultiplierEnabled(params.PlanGroupRateEnabled).
		SetProvisioningPhase(creative.CreativeProvisioningPhaseCreated)
	if params.WorkspaceID != "" {
		builder.SetWorkspaceID(params.WorkspaceID)
	}
	if params.IdempotencyKey != nil {
		builder.SetIdempotencyKey(*params.IdempotencyKey)
	}
	entity, err := builder.Save(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, nil, creative.ErrCreativeRunExists)
	}
	// Publish pending output rows with the task so result delivery always has
	// metadata indexes corresponding to the output count saved during validated
	// submission, rather than creating those rows after queue execution starts.
	outputBuilders := make([]*dbent.CreativeRunOutputCreate, 0, params.RequestedOutputCount)
	for index := range params.RequestedOutputCount {
		outputBuilders = append(outputBuilders, tx.CreativeRunOutput.Create().
			SetRunID(params.RunID).
			SetOutputIndex(index).
			SetStatus(creative.CreativeRunOutputStatusPending))
	}
	if len(outputBuilders) > 0 {
		if err := tx.CreativeRunOutput.CreateBulk(outputBuilders...).Exec(ctx); err != nil {
			return nil, translatePersistenceError(err, nil, creative.ErrCreativeOutputExists)
		}
	}
	// Persist provisioning recovery in the creation transaction. Later funding,
	// transient storage, or enqueue failures can leave this queued metadata present,
	// so recovery needs its action to appear with the task itself.
	if _, err := tx.CreativeRunOutbox.Create().
		SetRunID(params.RunID).
		SetOperation(string(creative.CreativeRunOutboxProvision)).
		SetStatus(string(creative.CreativeRunOutboxPending)).
		Save(ctx); err != nil {
		return nil, translatePersistenceError(err, nil, creative.ErrCreativeRunExists)
	}
	if err := tx.Commit(); err != nil {
		return nil, translatePersistenceError(err, nil, creative.ErrCreativeRunExists)
	}
	return creativeRunEntityToService(entity), nil
}

// GetCreativeRunByRunID loads internal task metadata by the supplied run
// identifier. Background recovery uses this lookup after queue admission;
// public callers establish owner/workspace access separately. Missing rows
// become ErrCreativeRunNotFound.
func (r *creativeRunRepository) GetCreativeRunByRunID(ctx context.Context, runID string) (*creative.CreativeRun, error) {
	entity, err := r.client.CreativeRun.Query().
		Where(creativerun.RunIDEQ(runID)).
		Only(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, creative.ErrCreativeRunNotFound, nil)
	}
	return creativeRunEntityToService(entity), nil
}

// GetCreativeRunByRunIDForOwner loads a task only when run, user, and
// workspace all match. It uses supplied scope values directly, and either
// absence or an ownership mismatch becomes ErrCreativeRunNotFound.
func (r *creativeRunRepository) GetCreativeRunByRunIDForOwner(ctx context.Context, scope creative.CreativeRunScope, runID string) (*creative.CreativeRun, error) {
	entity, err := r.client.CreativeRun.Query().
		Where(
			creativerun.RunIDEQ(runID),
			creativerun.UserIDEQ(scope.UserID),
			creativerun.WorkspaceIDEQ(scope.WorkspaceID),
		).
		Only(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, creative.ErrCreativeRunNotFound, nil)
	}
	return creativeRunEntityToService(entity), nil
}

// GetCreativeRunByIdempotencyKey loads the latest matching user/workspace/key
// row by descending ID. Callers normalize scope and key beforehand; no
// matching row becomes ErrCreativeRunNotFound rather than searching another
// workspace.
func (r *creativeRunRepository) GetCreativeRunByIdempotencyKey(ctx context.Context, scope creative.CreativeRunScope, key string) (*creative.CreativeRun, error) {
	entity, err := r.client.CreativeRun.Query().
		Where(
			creativerun.UserIDEQ(scope.UserID),
			creativerun.WorkspaceIDEQ(scope.WorkspaceID),
			creativerun.IdempotencyKeyEQ(key),
		).
		Order(dbent.Desc(creativerun.FieldID)).
		First(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, creative.ErrCreativeRunNotFound, nil)
	}
	return creativeRunEntityToService(entity), nil
}

// ListCreativeRunsForOwner lists supplied-scope rows by descending creation
// time and row ID. Limits outside one to one hundred use twenty, and negative
// offsets use zero. The active shortcut selects queued, running,
// provider_succeeded, and settlement_pending; other nonblank status values
// match exactly.
func (r *creativeRunRepository) ListCreativeRunsForOwner(ctx context.Context, scope creative.CreativeRunScope, filter creative.CreativeRunFilter) ([]*creative.CreativeRun, error) {
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
				creative.CreativeRunStatusQueued,
				creative.CreativeRunStatusRunning,
				creative.CreativeRunStatusProviderSucceeded,
				creative.CreativeRunStatusSettlementPending,
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
	out := make([]*creative.CreativeRun, 0, len(entities))
	for _, entity := range entities {
		out = append(out, creativeRunEntityToService(entity))
	}
	return out, nil
}

// TransitionCreativeRunStatus validates a status pair and applies it only at
// the version that was read. Callers can supply a transition clock; otherwise
// the current system time is used.
//
// Running records a start time, terminal states record completion, and
// cancellation also records cancellation time. Failure, result loss, and
// release-pending transitions accept supplied error details; release pending
// defaults its target to failed. A zero affected-row result means another
// writer changed the version and returns ErrCreativeInvalidTransition so
// callers can reread before choosing recovery.
func (r *creativeRunRepository) TransitionCreativeRunStatus(ctx context.Context, runID, toStatus string, opts creative.CreativeRunTransitionOptions) error {
	now := time.Now()
	if opts.Now != nil {
		now = *opts.Now
	}
	current, err := r.client.CreativeRun.Query().
		Where(creativerun.RunIDEQ(runID)).
		Only(ctx)
	if err != nil {
		return translatePersistenceError(err, creative.ErrCreativeRunNotFound, nil)
	}
	if !creative.CanTransitionCreativeRun(current.Status, toStatus) {
		return creative.ErrCreativeInvalidTransition
	}
	builder := r.client.CreativeRun.Update().
		Where(
			creativerun.RunIDEQ(runID),
			creativerun.VersionEQ(current.Version),
		).
		SetStatus(toStatus).
		SetVersion(current.Version + 1).
		SetUpdatedAt(now)
	if toStatus == creative.CreativeRunStatusRunning {
		builder.SetStartedAt(now)
	}
	if creative.IsTerminalCreativeRunStatus(toStatus) {
		builder.SetCompletedAt(now)
	}
	if toStatus == creative.CreativeRunStatusCancelled {
		builder.SetCancelledAt(now)
	}
	if toStatus == creative.CreativeRunStatusFailed {
		if opts.ErrorCode != nil {
			builder.SetErrorCode(*opts.ErrorCode)
		}
		if opts.ErrorMessage != nil {
			builder.SetErrorMessage(*opts.ErrorMessage)
		}
	}
	if toStatus == creative.CreativeRunStatusReleasePending {
		target := opts.ReleaseTargetStatus
		if target == "" {
			target = creative.CreativeRunStatusFailed
		}
		builder.SetReleaseTargetStatus(target)
		if opts.ErrorCode != nil {
			builder.SetErrorCode(*opts.ErrorCode)
		}
		if opts.ErrorMessage != nil {
			builder.SetErrorMessage(*opts.ErrorMessage)
		}
	}
	if toStatus == creative.CreativeRunStatusResultLost {
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
		// The status was validated against the read version. A zero-row
		// update means
		// that version was displaced before writing, so the caller must
		// reread state
		// before selecting another transition or recovery action.
		return creative.ErrCreativeInvalidTransition
	}
	return nil
}

// MarkCreativeRunRunning advances a valid task to running with a version check
// and start time. If the read row is already running, a positive changed
// provider is updated directly by run ID and the method otherwise returns
// successfully. The already-running provider update does not apply the version
// predicate used for the initial transition.
//
// Provider reselection can reach this method after another attempt already
// marked the task running. That path updates identity while retaining the
// existing start time; callers check current cancellation before transport.
func (r *creativeRunRepository) MarkCreativeRunRunning(ctx context.Context, runID string, providerID int64, now time.Time) error {
	current, err := r.client.CreativeRun.Query().
		Where(creativerun.RunIDEQ(runID)).
		Only(ctx)
	if err != nil {
		return translatePersistenceError(err, creative.ErrCreativeRunNotFound, nil)
	}
	if current.Status == creative.CreativeRunStatusRunning {
		// Repeated running admission can refresh provider identity
		// after reselection.
		// This branch writes by run ID, so it carries neither the
		// version fence nor
		// the start-time update used by the first running transition.
		if providerID > 0 && (current.ProviderID == nil || *current.ProviderID != providerID) {
			_, err := r.client.CreativeRun.Update().
				Where(creativerun.RunIDEQ(runID)).
				SetProviderID(providerID).
				SetUpdatedAt(now).
				Save(ctx)
			return err
		}
		return nil
	}
	if !creative.CanTransitionCreativeRun(current.Status, creative.CreativeRunStatusRunning) {
		return creative.ErrCreativeInvalidTransition
	}
	builder := r.client.CreativeRun.Update().
		Where(
			creativerun.RunIDEQ(runID),
			creativerun.VersionEQ(current.Version),
		).
		SetStatus(creative.CreativeRunStatusRunning).
		SetVersion(current.Version + 1).
		SetStartedAt(now).
		SetUpdatedAt(now)
	if providerID > 0 {
		builder.SetProviderID(providerID)
	}
	affected, err := builder.Save(ctx)
	if err != nil {
		return err
	}
	if affected == 0 {
		return creative.ErrCreativeInvalidTransition
	}
	return nil
}

// SetCreativeRunExecution records a positive provider identifier and supplied
// platform before worker transport begins. Nonpositive provider values return
// success, while no matching run returns ErrCreativeRunNotFound; this update
// uses no version or status predicate.
func (r *creativeRunRepository) SetCreativeRunExecution(ctx context.Context, runID string, providerID int64, platform string, now time.Time) error {
	if providerID <= 0 {
		return nil
	}
	affected, err := r.client.CreativeRun.Update().
		Where(creativerun.RunIDEQ(runID)).
		SetPlatform(platform).
		SetProviderID(providerID).
		SetUpdatedAt(now).
		Save(ctx)
	if err != nil {
		return err
	}
	if affected == 0 {
		return creative.ErrCreativeRunNotFound
	}
	return nil
}

// MarkCreativeRunSucceeded records cost and completion for running,
// provider_succeeded, or settlement_pending rows. If no row updates, a current
// succeeded or cancelled row returns success; other existing states return
// ErrCreativeInvalidTransition.
//
// The status predicate protects a concurrently cancelled row from this success
// update. A separate read classifies the zero-row result, so success can mean
// the task was already complete or cancellation won before this write; callers
// use provider outcome metadata to settle that work separately.
func (r *creativeRunRepository) MarkCreativeRunSucceeded(ctx context.Context, runID string, actualCost float64, now time.Time) error {
	affected, err := r.client.CreativeRun.Update().
		Where(
			creativerun.RunIDEQ(runID),
			creativerun.StatusIn(
				creative.CreativeRunStatusRunning,
				creative.CreativeRunStatusProviderSucceeded,
				creative.CreativeRunStatusSettlementPending,
			),
		).
		SetStatus(creative.CreativeRunStatusSucceeded).
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
			return translatePersistenceError(getErr, creative.ErrCreativeRunNotFound, nil)
		}
		if current.Status == creative.CreativeRunStatusSucceeded || current.Status == creative.CreativeRunStatusCancelled {
			return nil
		}
		return creative.ErrCreativeInvalidTransition
	}
	return nil
}

// MarkCreativeRunProviderSucceeded records provider work timing and an
// optional provider ID against the read version. Already succeeded or
// result_lost rows return success. Running changes to provider_succeeded;
// other read states retain their status, and the affected-row count is not
// checked before success is returned.
//
// The recorded timestamp restores a provider-work fact from succeeded output
// metadata. A competing version update can suppress this write, so callers
// reread current metadata through their recovery flow rather than treating the
// nil error as a guarantee that this attempt installed its timestamp.
func (r *creativeRunRepository) MarkCreativeRunProviderSucceeded(ctx context.Context, runID string, providerID int64, now time.Time) error {
	current, err := r.client.CreativeRun.Query().Where(creativerun.RunIDEQ(runID)).Only(ctx)
	if err != nil {
		return translatePersistenceError(err, creative.ErrCreativeRunNotFound, nil)
	}
	if current.Status == creative.CreativeRunStatusSucceeded || current.Status == creative.CreativeRunStatusResultLost {
		return nil
	}
	builder := r.client.CreativeRun.Update().
		Where(creativerun.RunIDEQ(runID), creativerun.VersionEQ(current.Version)).
		SetProviderResultRecordedAt(now).
		SetUpdatedAt(now).
		AddVersion(1)
	if providerID > 0 {
		builder.SetProviderID(providerID)
	}
	if current.Status == creative.CreativeRunStatusRunning {
		builder.SetStatus(creative.CreativeRunStatusProviderSucceeded)
	}
	if _, err := builder.Save(ctx); err != nil {
		return err
	}
	return nil
}

// UpdateCreativeRunOutput changes metadata for an output that is not
// acknowledged. Status and updated time are assigned; MIME, positive byte
// size, expiry, code, and message are written only when supplied.
//
// Empty code or message therefore keeps the earlier stored text. An
// acknowledged row remains unchanged and is treated as successful, while a
// missing output returns ErrCreativeOutputNotFound. The existence read after a
// zero-row update distinguishes those cases, allowing repeated delivery work
// to leave client receipt intact.
func (r *creativeRunRepository) UpdateCreativeRunOutput(ctx context.Context, runID string, outputIndex int, status, mimeType string, byteSize int64, transientExpiresAt *time.Time, errorCode, errorMessage string) error {
	builder := r.client.CreativeRunOutput.Update().
		Where(
			creativerunoutput.RunIDEQ(runID),
			creativerunoutput.OutputIndexEQ(outputIndex),
			// Receipt persists independently from repeated delivery
			// writes. Exclude acked
			// rows in this update predicate so recovery cannot
			// replace the client receipt
			// with another succeeded or failed metadata state.
			creativerunoutput.StatusNEQ(creative.CreativeRunOutputStatusAcked),
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
		// The update excludes acknowledged rows, so zero affected rows
		// alone cannot
		// distinguish prior client receipt from a missing index. Check
		// existence before
		// classifying the delivery write as successful or absent.
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
			return creative.ErrCreativeOutputNotFound
		}
	}
	return nil
}

// GetCreativeRunOutput loads one output by exact run identifier and output
// index. Public queries establish task ownership before using this internal
// metadata lookup, and a missing output becomes ErrCreativeOutputNotFound.
func (r *creativeRunRepository) GetCreativeRunOutput(ctx context.Context, runID string, outputIndex int) (*creative.CreativeRunOutput, error) {
	entity, err := r.client.CreativeRunOutput.Query().
		Where(
			creativerunoutput.RunIDEQ(runID),
			creativerunoutput.OutputIndexEQ(outputIndex),
		).
		Only(ctx)
	if err != nil {
		return nil, translatePersistenceError(err, creative.ErrCreativeOutputNotFound, nil)
	}
	return creativeRunOutputEntityToService(entity), nil
}

// ListCreativeRunOutputs lists metadata for an exact run identifier in
// ascending output-index order. No rows yields an allocated empty slice, and
// public callers establish task ownership before using this internal lookup.
func (r *creativeRunRepository) ListCreativeRunOutputs(ctx context.Context, runID string) ([]*creative.CreativeRunOutput, error) {
	entities, err := r.client.CreativeRunOutput.Query().
		Where(creativerunoutput.RunIDEQ(runID)).
		Order(dbent.Asc(creativerunoutput.FieldOutputIndex)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*creative.CreativeRunOutput, 0, len(entities))
	for _, entity := range entities {
		out = append(out, creativeRunOutputEntityToService(entity))
	}
	return out, nil
}

// ListCreativeRunOutputsForRuns groups output metadata for supplied run
// identifiers, ordered by run and output index. Empty input returns an
// allocated empty map; runs without rows have no map entry.
func (r *creativeRunRepository) ListCreativeRunOutputsForRuns(ctx context.Context, runIDs []string) (map[string][]*creative.CreativeRunOutput, error) {
	result := make(map[string][]*creative.CreativeRunOutput, len(runIDs))
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

// MarkCreativeRunOutputAcked changes a succeeded output to acknowledged and
// records receipt time. A zero-row update rereads the output: already
// acknowledged succeeds, missing output reports absence, and another status
// reports not ready.
//
// Receipt uses a succeeded-state predicate so a failed or pending output cannot
// become acknowledged through this call. Concurrent receipt can win that update
// first; the following read accepts its acked state, allowing the query caller
// to retry transient deletion after either the initial or repeated receipt.
func (r *creativeRunRepository) MarkCreativeRunOutputAcked(ctx context.Context, runID string, outputIndex int, now time.Time) error {
	affected, err := r.client.CreativeRunOutput.Update().
		Where(
			creativerunoutput.RunIDEQ(runID),
			creativerunoutput.OutputIndexEQ(outputIndex),
			creativerunoutput.StatusEQ(creative.CreativeRunOutputStatusSucceeded),
		).
		SetStatus(creative.CreativeRunOutputStatusAcked).
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
			return translatePersistenceError(existsErr, creative.ErrCreativeOutputNotFound, nil)
		}
		if exists.Status == creative.CreativeRunOutputStatusAcked {
			return nil
		}
		return creative.ErrCreativeOutputNotReady
	}
	return nil
}

// ListCreativeRunsDueForTransientCleanup lists terminal tasks completed at or
// before cutoff in ascending row-ID order. Limits outside one to one thousand
// select one hundred; this read does not mark rows cleaned.
func (r *creativeRunRepository) ListCreativeRunsDueForTransientCleanup(ctx context.Context, cutoff time.Time, limit int) ([]*creative.CreativeRun, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	entities, err := r.client.CreativeRun.Query().
		Where(
			creativerun.StatusIn(
				creative.CreativeRunStatusSucceeded,
				creative.CreativeRunStatusFailed,
				creative.CreativeRunStatusCancelled,
				creative.CreativeRunStatusResultLost,
			),
			creativerun.CompletedAtLTE(cutoff),
		).
		Order(dbent.Asc(creativerun.FieldID)).
		Limit(limit).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*creative.CreativeRun, 0, len(entities))
	for _, entity := range entities {
		out = append(out, creativeRunEntityToService(entity))
	}
	return out, nil
}

// IncrementCreativeRunAttempt atomically adds one execution attempt, then
// independently reads the counter. A missing row reports
// ErrCreativeRunNotFound, and a later concurrent increment can be included in
// the returned count.
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
		return 0, creative.ErrCreativeRunNotFound
	}
	entity, err := r.client.CreativeRun.Query().
		Where(creativerun.RunIDEQ(runID)).
		Select(creativerun.FieldAttemptCount).
		Only(ctx)
	if err != nil {
		return 0, translatePersistenceError(err, creative.ErrCreativeRunNotFound, nil)
	}
	return entity.AttemptCount, nil
}

// IncrementCreativeRunSettlementAttempt adds one settlement recovery attempt
// and then reads its stored counter. A later concurrent increment can be
// included because incrementCreativeRunCounter performs a separate update and
// read.
func (r *creativeRunRepository) IncrementCreativeRunSettlementAttempt(ctx context.Context, runID string) (int, error) {
	return r.incrementCreativeRunCounter(ctx, runID, true)
}

// IncrementCreativeRunReleaseAttempt adds one release recovery attempt and
// then reads its stored counter. A later concurrent increment can be included
// because incrementCreativeRunCounter performs a separate update and read.
func (r *creativeRunRepository) IncrementCreativeRunReleaseAttempt(ctx context.Context, runID string) (int, error) {
	return r.incrementCreativeRunCounter(ctx, runID, false)
}

// incrementCreativeRunCounter updates the selected settlement or release
// counter and subsequently reads it. The separate read translates missing rows
// and can observe increments applied by other writers after this update.
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
			return 0, translatePersistenceError(err, creative.ErrCreativeRunNotFound, nil)
		}
		return entity.SettlementAttemptCount, nil
	}
	entity, err := query.Select(creativerun.FieldReleaseAttemptCount).Only(ctx)
	if err != nil {
		return 0, translatePersistenceError(err, creative.ErrCreativeRunNotFound, nil)
	}
	return entity.ReleaseAttemptCount, nil
}

// SetCreativeRunProvisioningPhase writes a supplied nonblank provisioning
// phase and update time by run ID. Blank phase text is rejected, but the
// nonblank value is stored without trimming or enum validation.
func (r *creativeRunRepository) SetCreativeRunProvisioningPhase(ctx context.Context, runID, phase string) error {
	if strings.TrimSpace(phase) == "" {
		return creative.ErrCreativeInvalidParams
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
		return creative.ErrCreativeRunNotFound
	}
	return nil
}

// SetCreativeRunAllowanceReserved writes the reserved allowance marker for an
// existing run. This ordinary Ent update is separate from funding
// transactions; an absent row returns ErrCreativeRunNotFound for the caller.
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
		return creative.ErrCreativeRunNotFound
	}
	return nil
}

// SetCreativeRunReconcileError writes recovery diagnostics and a next-attempt
// timestamp by run ID. Blank message clears the error, nonblank message keeps
// supplied spelling, and a zero next time clears the timestamp.
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
		return creative.ErrCreativeRunNotFound
	}
	return nil
}

// creativeRunEntityToService converts Ent metadata and defaults an empty
// requested model to Model. Nil entities remain nil; optional pointers and
// allocation storage are retained from the supplied entity rather than
// independently cloned.
func creativeRunEntityToService(entity *dbent.CreativeRun) *creative.CreativeRun {
	if entity == nil {
		return nil
	}
	requestedModel := entity.RequestedModel
	if requestedModel == "" {
		requestedModel = entity.Model
	}
	return &creative.CreativeRun{
		ID:                          entity.ID,
		RunID:                       entity.RunID,
		UserID:                      entity.UserID,
		WorkspaceID:                 entity.WorkspaceID,
		GroupID:                     entity.GroupID,
		APIKeyID:                    entity.APIKeyID,
		ProviderID:                  entity.ProviderID,
		Platform:                    entity.Platform,
		Model:                       entity.Model,
		RequestedModel:              requestedModel,
		Operation:                   entity.Operation,
		RequestedOutputCount:        entity.RequestedOutputCount,
		ImageSize:                   entity.ImageSize,
		FrameCount:                  entity.FrameCount,
		FrameDurationMS:             entity.FrameDurationMs,
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

// creativeRunOutputEntityToService converts Ent output metadata while
// retaining optional pointers from the entity. Nil input returns nil, and
// callers treating those returned pointer values as immutable avoid shared
// mutation.
func creativeRunOutputEntityToService(entity *dbent.CreativeRunOutput) *creative.CreativeRunOutput {
	if entity == nil {
		return nil
	}
	return &creative.CreativeRunOutput{
		ID:                 entity.ID,
		RunID:              entity.RunID,
		OutputIndex:        entity.OutputIndex,
		FrameCount:         entity.FrameCount,
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
