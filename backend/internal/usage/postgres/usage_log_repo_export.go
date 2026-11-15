package postgres

import (
	"context"
	"fmt"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// usage_log_repo_export.go counts and streams usage_logs in keyset batches for
// background CSV exports while caching per-task display associations.
// Large exports avoid deep SQL OFFSET scans by ordering strictly on
// (created_at DESC, id DESC) and advancing a tuple cursor across batches inside
// the half-open [StartTime, EndTime) window. Reusing the same filter builders as
// ListWithFilters keeps exported rows aligned with interactive Usage Records pages.
//
// Each task execution receives an isolated exportBatchReader that resolves
// owned-team membership once and caches up to 4,096 User, APIKey, Provider, and
// Group display views. User-scope exports hydrate only APIKey views because the
// 19-column user CSV omits account emails, provider identities, and group names.

// Keyset batch sizing and per-task association cache bounds protect memory
// consumption during multi-hundred-thousand-row export scans.
const (
	defaultExportRepoBatchLimit = 2000 // fallback batch row limit when caller passes nonpositive limit
	maxExportAssocCacheEntries  = 4096 // maximum cached association views per entity kind in one task
)

// exportBatchReader streams keyset-paginated usage rows for one export task and
// caches resolved team scope and display associations across batches.
type exportBatchReader struct {
	store           *Store                        // owning PostgreSQL usage store
	scope           string                        // user or admin export scope
	teamResolved    bool                          // whether ownedTeamID was already resolved
	ownedTeamUserID int64                         // userID for which ownedTeamID was resolved
	ownedTeamID     int64                         // cached owned team ID or zero
	userCache       map[int64]*usage.UserView     // per-task acting user display cache
	apiKeyCache     map[int64]*usage.KeyView      // per-task API key display cache
	providerCache   map[int64]*usage.ProviderView // per-task provider display cache
	groupCache      map[int64]*usage.GroupView    // per-task group display cache
}

// CountExportLogs returns the exact row count matching filters at export start
// using the same half-open time window and scope semantics as ListWithFilters.
func (r *Store) CountExportLogs(ctx context.Context, filters usage.UsageExportFilters) (int64, error) {
	logFilters := filters.ToUsageLogFilters()
	source, whereClause, args, err := r.buildExportQueryParts(ctx, logFilters, nil, nil)
	if err != nil {
		return 0, err
	}
	countQuery := "SELECT COUNT(*) FROM " + source + " " + whereClause
	var total int64
	if err := scanSingleRow(ctx, r.sql, countQuery, args, &total); err != nil {
		return 0, err
	}
	return total, nil
}

// NewExportBatchReader creates a per-task keyset reader with isolated display
// caches scoped to user or administrator CSV requirements.
func (r *Store) NewExportBatchReader(scope string) usage.UsageExportBatchReader {
	return &exportBatchReader{
		store:         r,
		scope:         scope,
		userCache:     make(map[int64]*usage.UserView),
		apiKeyCache:   make(map[int64]*usage.KeyView),
		providerCache: make(map[int64]*usage.ProviderView),
		groupCache:    make(map[int64]*usage.GroupView),
	}
}

// NextBatch fetches up to limit matching usage rows strictly older than cursor
// in descending (created_at, id) order and hydrates required display views.
func (b *exportBatchReader) NextBatch(
	ctx context.Context,
	filters usage.UsageExportFilters,
	cursor *usage.UsageExportCursor,
	limit int,
) ([]usage.UsageLog, error) {
	if limit <= 0 {
		limit = defaultExportRepoBatchLimit
	}
	logFilters := filters.ToUsageLogFilters()
	ownedTeamResolver := b.resolveOwnedTeamIDOnce
	source, whereClause, args, err := b.store.buildExportQueryParts(ctx, logFilters, cursor, ownedTeamResolver)
	if err != nil {
		return nil, err
	}

	args = append(args, limit)
	limitPos := len(args)
	query := fmt.Sprintf(
		"SELECT %s FROM %s %s ORDER BY created_at DESC, id DESC LIMIT $%d",
		usageLogSelectColumns,
		source,
		whereClause,
		limitPos,
	)

	logs, err := b.store.queryUsageLogs(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	if len(logs) == 0 {
		return logs, nil
	}

	if err := b.hydrateExportBatch(ctx, logs); err != nil {
		return nil, err
	}
	return logs, nil
}

// resolveOwnedTeamIDOnce caches the user's owned team lookup across all batches
// of one export task so keyset scans avoid repeating team membership queries.
func (b *exportBatchReader) resolveOwnedTeamIDOnce(ctx context.Context, userID int64) (int64, error) {
	if b.teamResolved && b.ownedTeamUserID == userID {
		return b.ownedTeamID, nil
	}
	teamID, err := b.store.getOwnedTeamID(ctx, userID)
	if err != nil {
		return 0, err
	}
	b.teamResolved = true
	b.ownedTeamUserID = userID
	b.ownedTeamID = teamID
	return teamID, nil
}

// buildExportQueryParts assembles the SQL source, WHERE clause, and positional
// arguments for CountExportLogs and NextBatch, appending keyset cursor bounds
// when cursor is nonnil.
func (r *Store) buildExportQueryParts(
	ctx context.Context,
	filters UsageLogFilters,
	cursor *usage.UsageExportCursor,
	teamResolver func(context.Context, int64) (int64, error),
) (string, string, []any, error) {
	conditions := make([]string, 0, 10)
	args := make([]any, 0, 12)

	source, scopeCondition, args, err := r.buildExportScopeSource(ctx, args, filters.UserID, filters.IncludeOwnedTeam, teamResolver)
	if err != nil {
		return "", "", nil, err
	}
	if scopeCondition != "" {
		conditions = append(conditions, scopeCondition)
	}
	if filters.APIKeyID > 0 {
		conditions = append(conditions, fmt.Sprintf("api_key_id = $%d", len(args)+1))
		args = append(args, filters.APIKeyID)
	}
	if filters.ProviderID > 0 {
		conditions = append(conditions, fmt.Sprintf("provider_id = $%d", len(args)+1))
		args = append(args, filters.ProviderID)
	}
	if filters.GroupID > 0 {
		conditions = append(conditions, fmt.Sprintf("group_id = $%d", len(args)+1))
		args = append(args, filters.GroupID)
	}
	if filters.TeamID > 0 {
		conditions = append(conditions, fmt.Sprintf("team_id = $%d", len(args)+1))
		args = append(args, filters.TeamID)
	}
	if filters.PersonalOnly {
		conditions = append(conditions, "team_id IS NULL")
	}
	if requestID := strings.TrimSpace(filters.RequestID); requestID != "" {
		conditions = append(conditions, usageRequestIDCondition("", len(args)+1))
		args = append(args, requestID)
	}
	conditions, args = appendRequestSourceCondition(conditions, args, filters.RequestSource, "")
	conditions, args = appendUsageLogModelWhereCondition(conditions, args, filters.Model, filters.ModelFilterSource)
	conditions, args = appendRequestTypeOrStreamWhereCondition(conditions, args, filters.RequestType, filters.Stream)
	conditions, args = appendNativeCompactionV2WhereCondition(conditions, args, filters.NativeCompactionV2, "")
	if filters.BillingType != nil {
		conditions = append(conditions, fmt.Sprintf("billing_type = $%d", len(args)+1))
		args = append(args, int16(*filters.BillingType))
	}
	conditions, args = appendUsageLogBillingModeWhereCondition(conditions, args, filters.BillingMode)
	if filters.StartTime != nil {
		conditions = append(conditions, fmt.Sprintf("created_at >= $%d", len(args)+1))
		args = append(args, *filters.StartTime)
	}
	if filters.EndTime != nil {
		conditions = append(conditions, fmt.Sprintf("created_at < $%d", len(args)+1))
		args = append(args, *filters.EndTime)
	}
	if cursor != nil {
		// Advance strictly past the last emitted (created_at, id) pair so rows
		// sharing identical microsecond timestamps are neither skipped nor duplicated.
		cTimePos := len(args) + 1
		cIDPos := len(args) + 2
		conditions = append(
			conditions,
			fmt.Sprintf("(created_at < $%d OR (created_at = $%d AND id < $%d))", cTimePos, cTimePos, cIDPos),
		)
		args = append(args, cursor.CreatedAt, cursor.ID)
	}

	return source, buildWhere(conditions), args, nil
}

// buildExportScopeSource builds the single-table or owned-team UNION ALL SQL
// source using an optional cached team-owner lookup.
func (r *Store) buildExportScopeSource(
	ctx context.Context,
	args []any,
	userID int64,
	includeOwnedTeam bool,
	teamResolver func(context.Context, int64) (int64, error),
) (string, string, []any, error) {
	if teamResolver == nil || userID <= 0 || !includeOwnedTeam {
		return r.buildUsageLogScopeSource(ctx, args, userID, includeOwnedTeam, "")
	}
	args = append(args, userID)
	userPos := len(args)
	teamID, err := teamResolver(ctx, userID)
	if err != nil {
		return "", "", nil, err
	}
	if teamID <= 0 {
		return "usage_logs", fmt.Sprintf("user_id = $%d", userPos), args, nil
	}
	args = append(args, teamID)
	teamPos := len(args)
	source := fmt.Sprintf(`(
		SELECT * FROM usage_logs WHERE user_id = $%d
		UNION ALL
		SELECT * FROM usage_logs WHERE team_id = $%d AND user_id <> $%d
	) usage_scope`, userPos, teamPos, userPos)
	return source, "", args, nil
}

// hydrateExportBatch attaches cached display associations required by b.scope,
// querying Ent only for uncached positive identifiers in the current batch.
func (b *exportBatchReader) hydrateExportBatch(ctx context.Context, logs []usage.UsageLog) error {
	if b.store == nil || b.store.client == nil || len(logs) == 0 {
		return nil
	}

	missingKeys := collectMissingPositiveIDs(logs, b.apiKeyCache, func(l *usage.UsageLog) int64 { return l.APIKeyID })
	if len(missingKeys) > 0 {
		loadedKeys, err := b.store.loadAPIKeys(ctx, missingKeys)
		if err != nil {
			return err
		}
		storeBoundedCacheEntries(b.apiKeyCache, missingKeys, loadedKeys)
	}

	if b.scope == usage.UsageExportScopeAdmin {
		missingUsers := collectMissingPositiveIDs(logs, b.userCache, func(l *usage.UsageLog) int64 { return l.UserID })
		if len(missingUsers) > 0 {
			loadedUsers, err := b.store.loadUsers(ctx, missingUsers)
			if err != nil {
				return err
			}
			storeBoundedCacheEntries(b.userCache, missingUsers, loadedUsers)
		}

		missingProviders := collectMissingPositiveIDs(logs, b.providerCache, func(l *usage.UsageLog) int64 { return l.ProviderID })
		if len(missingProviders) > 0 {
			loadedProviders, err := b.store.loadProviders(ctx, missingProviders)
			if err != nil {
				return err
			}
			storeBoundedCacheEntries(b.providerCache, missingProviders, loadedProviders)
		}

		missingGroups := collectMissingPositiveIDs(logs, b.groupCache, func(l *usage.UsageLog) int64 {
			if l.GroupID == nil {
				return 0
			}
			return *l.GroupID
		})
		if len(missingGroups) > 0 {
			loadedGroups, err := b.store.loadGroups(ctx, missingGroups)
			if err != nil {
				return err
			}
			storeBoundedCacheEntries(b.groupCache, missingGroups, loadedGroups)
		}
	}

	for i := range logs {
		if logs[i].APIKeyID > 0 {
			logs[i].APIKey = b.apiKeyCache[logs[i].APIKeyID]
		}
		if b.scope == usage.UsageExportScopeAdmin {
			if logs[i].UserID > 0 {
				logs[i].User = b.userCache[logs[i].UserID]
			}
			if logs[i].ProviderID > 0 {
				logs[i].Provider = b.providerCache[logs[i].ProviderID]
			}
			if logs[i].GroupID != nil && *logs[i].GroupID > 0 {
				logs[i].Group = b.groupCache[*logs[i].GroupID]
			}
		}
	}
	return nil
}

// collectMissingPositiveIDs returns deduplicated positive relation IDs from logs
// that have not yet been recorded in cache.
func collectMissingPositiveIDs[V any](
	logs []usage.UsageLog,
	cache map[int64]*V,
	extract func(*usage.UsageLog) int64,
) []int64 {
	var missing []int64
	seen := make(map[int64]struct{})
	for i := range logs {
		id := extract(&logs[i])
		if id <= 0 {
			continue
		}
		if _, cached := cache[id]; cached {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		missing = append(missing, id)
	}
	return missing
}

// storeBoundedCacheEntries records loaded and missing IDs in cache, clearing the
// map first when adding requestedIDs would exceed maxExportAssocCacheEntries.
func storeBoundedCacheEntries[V any](cache map[int64]*V, requestedIDs []int64, loaded map[int64]*V) {
	if len(cache)+len(requestedIDs) > maxExportAssocCacheEntries {
		// Evict the per-task map wholesale at 4,096 entries so pathological high-cardinality
		// admin exports stay memory-bounded without per-row LRU bookkeeping.
		clear(cache)
	}
	for _, id := range requestedIDs {
		cache[id] = loaded[id]
	}
}
