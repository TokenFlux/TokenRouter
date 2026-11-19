package ops

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// system_log_service.go lists indexed system logs and coordinates filtered
// cleanup with a subsequent audit write.
// System-log cleanup deletes matching indexed rows before attempting a separate
// audit write with the same caller context. Audit failure is logged while the
// completed deletion count is returned, so successful cleanup does not
// establish that its audit row exists. A classified missing-row deletion error
// returns successful zero before audit insertion.
//
// Monitoring gates listing and cleanup, but repository absence produces an
// empty fixed page for listing and an unavailable error for deletion. Cleanup
// requires a positive operator and ordered supplied endpoints; PostgreSQL
// additionally requires at least one effective filter. Audit conditions
// serialize the requested filter, including optional IDs that storage may
// ignore when nonpositive, rather than reproducing the exact generated SQL
// predicates.

// ListSystemLogs returns indexed rows after monitoring checks, creating an
// empty filter when nil and normalizing pagination in a supplied filter when
// storage is available. Nonpositive values become page one and size fifty,
// capped at two hundred; absent storage returns a fixed empty page, and read
// errors retain their cause in an internal application error.
func (s *OpsService) ListSystemLogs(ctx context.Context, filter *OpsSystemLogFilter) (*OpsSystemLogList, error) {
	if err := s.RequireMonitoringEnabled(ctx); err != nil {
		return nil, err
	}
	// When monitoring permits listing but storage is absent, return an allocated
	// empty Logs slice with fixed pagination for the HTTP result.
	if s.opsRepo == nil {
		return &OpsSystemLogList{
			Logs:     []*OpsSystemLog{},
			Total:    0,
			Page:     1,
			PageSize: 50,
		}, nil
	}
	if filter == nil {
		filter = &OpsSystemLogFilter{}
	}
	if filter.Page <= 0 {
		filter.Page = 1
	}
	if filter.PageSize <= 0 {
		filter.PageSize = 50
	}
	// Cap indexed-log pages at two hundred returned rows.
	if filter.PageSize > 200 {
		filter.PageSize = 200
	}

	result, err := s.opsRepo.ListSystemLogs(ctx, filter)
	if err != nil {
		return nil, infraerrors.InternalServer("OPS_SYSTEM_LOG_LIST_FAILED", "Failed to list system logs").WithCause(err)
	}
	return result, nil
}

// CleanupSystemLogs deletes indexed rows for a positive operator identity
// after monitoring, repository, and supplied range checks. Nil filters become
// empty filters, whose effective-constraint requirement belongs to the
// repository.
//
// A classified missing-row error returns successful zero before auditing; a
// missing-filter diagnostic becomes bad request, and other deletion errors
// retain their cause in an internal error. After successful deletion, an
// independent audit attempt records requested conditions and affected rows
// using the same context. Audit failure is logged and ignored, leaving the
// completed deletion count intact. This sequence has no shared transaction or
// rollback of the deletion.
func (s *OpsService) CleanupSystemLogs(ctx context.Context, filter *OpsSystemLogCleanupFilter, operatorID int64) (int64, error) {
	if err := s.RequireMonitoringEnabled(ctx); err != nil {
		return 0, err
	}
	if s.opsRepo == nil {
		return 0, infraerrors.ServiceUnavailable("OPS_REPO_UNAVAILABLE", "Ops repository not available")
	}
	if operatorID <= 0 {
		return 0, infraerrors.BadRequest("OPS_SYSTEM_LOG_CLEANUP_INVALID_OPERATOR", "invalid operator")
	}
	if filter == nil {
		filter = &OpsSystemLogCleanupFilter{}
	}
	if filter.EndTime != nil && filter.StartTime != nil && filter.StartTime.After(*filter.EndTime) {
		return 0, infraerrors.BadRequest("OPS_SYSTEM_LOG_CLEANUP_INVALID_RANGE", "invalid time range")
	}

	// Finish the deletion before creating audit metadata; the subsequent audit is
	// an independent write and cannot roll that deletion back.
	deletedRows, err := s.opsRepo.DeleteSystemLogs(ctx, filter)
	if err != nil {
		// Treat a recognized missing-row failure as successful zero before auditing,
		// matching the service's empty-result behavior for that repository condition.
		if s.isNotFound(err) {
			return 0, nil
		}
		// Use the repository's missing-filter diagnostic substring to select bad
		// request; other returned deletion failures retain the generic internal-error
		// mapping.
		if strings.Contains(strings.ToLower(err.Error()), "requires at least one filter") {
			return 0, infraerrors.BadRequest("OPS_SYSTEM_LOG_CLEANUP_FILTER_REQUIRED", "cleanup requires at least one filter condition")
		}
		return 0, infraerrors.InternalServer("OPS_SYSTEM_LOG_CLEANUP_FAILED", "Failed to cleanup system logs").WithCause(err)
	}

	if auditErr := s.opsRepo.InsertSystemLogCleanupAudit(ctx, &OpsSystemLogCleanupAudit{
		CreatedAt:   time.Now().UTC(),
		OperatorID:  operatorID,
		Conditions:  marshalSystemLogCleanupConditions(filter),
		DeletedRows: deletedRows,
	}); auditErr != nil {
		// The deletion has already completed when audit insertion fails. Log that
		// diagnostic and return its count even if this shared context expired.
		logging.LegacyPrintf("service.ops_system_log", "[OpsSystemLog] cleanup audit failed: %v", auditErr)
	}
	return deletedRows, nil
}

// marshalSystemLogCleanupConditions encodes trimmed requested strings, nonnil
// identities, and nonzero UTC timestamps for the cleanup audit. Blank string
// keys remain present; nil filters or encoding failure yield an empty JSON
// object. The output describes request values, including IDs storage can omit.
func marshalSystemLogCleanupConditions(filter *OpsSystemLogCleanupFilter) string {
	if filter == nil {
		return "{}"
	}
	payload := map[string]any{
		"host":              strings.TrimSpace(filter.Host),
		"level":             strings.TrimSpace(filter.Level),
		"component":         strings.TrimSpace(filter.Component),
		"request_id":        strings.TrimSpace(filter.RequestID),
		"client_request_id": strings.TrimSpace(filter.ClientRequestID),
		"platform":          strings.TrimSpace(filter.Platform),
		"model":             strings.TrimSpace(filter.Model),
		"query":             strings.TrimSpace(filter.Query),
	}
	if filter.UserID != nil {
		payload["user_id"] = *filter.UserID
	}
	if filter.APIKeyID != nil {
		payload["api_key_id"] = *filter.APIKeyID
	}
	if filter.ProviderID != nil {
		payload["provider_id"] = *filter.ProviderID
	}
	// Encode supplied nonzero times in UTC with RFC3339Nano so audit conditions
	// retain their instants independently of the input's displayed offset.
	if filter.StartTime != nil && !filter.StartTime.IsZero() {
		payload["start_time"] = filter.StartTime.UTC().Format(time.RFC3339Nano)
	}
	if filter.EndTime != nil && !filter.EndTime.IsZero() {
		payload["end_time"] = filter.EndTime.UTC().Format(time.RFC3339Nano)
	}
	raw, err := json.Marshal(payload)
	// Use an empty conditions object when encoding fails, leaving cleanup outcome
	// handling to the caller's separate deletion and audit sequence.
	if err != nil {
		return "{}"
	}
	return string(raw)
}

// GetSystemLogSinkHealth returns the bound sink's sampled health without a
// monitoring check. A nil service or unbound sink returns zero values; sink
// construction can expose configured capacity before its worker starts.
func (s *OpsService) GetSystemLogSinkHealth() OpsSystemLogSinkHealth {
	if s == nil || s.systemLogSink == nil {
		return OpsSystemLogSinkHealth{}
	}
	return s.systemLogSink.Health()
}
