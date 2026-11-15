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
	store                *Store                        // owning PostgreSQL usage store
	scope                string                        // user or admin export scope
	maxAssocCacheEntries int                           // per-entity association cache bound
	teamResolved         bool                          // whether ownedTeamID was already resolved
	ownedTeamUserID      int64                         // userID for which ownedTeamID was resolved
	ownedTeamID          int64                         // cached owned team ID or zero
	userCache            map[int64]*usage.UserView     // per-task acting user display cache
	apiKeyCache          map[int64]*usage.KeyView      // per-task API key display cache
	providerCache        map[int64]*usage.ProviderView // per-task provider display cache
	groupCache           map[int64]*usage.GroupView    // per-task group display cache
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
		store:                r,
		scope:                scope,
		maxAssocCacheEntries: maxExportAssocCacheEntries,
		userCache:            make(map[int64]*usage.UserView),
		apiKeyCache:          make(map[int64]*usage.KeyView),
		providerCache:        make(map[int64]*usage.ProviderView),
		groupCache:           make(map[int64]*usage.GroupView),
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
// querying Ent only for uncached positive identifiers while preserving all
// current-batch hits across bounded cache eviction.
func (b *exportBatchReader) hydrateExportBatch(ctx context.Context, logs []usage.UsageLog) error {
	if b.store == nil || b.store.client == nil || len(logs) == 0 {
		return nil
	}

	keyViews, err := resolveExportBatchRelations(
		ctx,
		logs,
		b.apiKeyCache,
		b.maxAssocCacheEntries,
		func(l *usage.UsageLog) int64 { return l.APIKeyID },
		b.store.loadAPIKeys,
	)
	if err != nil {
		return err
	}

	var (
		userViews     map[int64]*usage.UserView
		providerViews map[int64]*usage.ProviderView
		groupViews    map[int64]*usage.GroupView
	)
	if b.scope == usage.UsageExportScopeAdmin {
		userViews, err = resolveExportBatchRelations(
			ctx,
			logs,
			b.userCache,
			b.maxAssocCacheEntries,
			func(l *usage.UsageLog) int64 { return l.UserID },
			b.store.loadUsers,
		)
		if err != nil {
			return err
		}

		providerViews, err = resolveExportBatchRelations(
			ctx,
			logs,
			b.providerCache,
			b.maxAssocCacheEntries,
			func(l *usage.UsageLog) int64 { return l.ProviderID },
			b.store.loadProviders,
		)
		if err != nil {
			return err
		}

		groupViews, err = resolveExportBatchRelations(
			ctx,
			logs,
			b.groupCache,
			b.maxAssocCacheEntries,
			func(l *usage.UsageLog) int64 {
				if l.GroupID == nil {
					return 0
				}
				return *l.GroupID
			},
			b.store.loadGroups,
		)
		if err != nil {
			return err
		}
	}

	for i := range logs {
		if logs[i].APIKeyID > 0 {
			logs[i].APIKey = keyViews[logs[i].APIKeyID]
		}
		if b.scope == usage.UsageExportScopeAdmin {
			if logs[i].UserID > 0 {
				logs[i].User = userViews[logs[i].UserID]
			}
			if logs[i].ProviderID > 0 {
				logs[i].Provider = providerViews[logs[i].ProviderID]
			}
			if logs[i].GroupID != nil && *logs[i].GroupID > 0 {
				logs[i].Group = groupViews[*logs[i].GroupID]
			}
		}
	}
	return nil
}

// resolveExportBatchRelations builds a complete ID-to-view map for every positive
// relation referenced in logs, loading only uncached IDs from loader.
//
// Pre-existing cache hits for the current batch are copied into batchViews before
// any capacity eviction runs on cache. When adding missingIDs would push cache
// past maxEntries, cache is cleared and repopulated from batchViews up to
// maxEntries so neither the current batch nor hot IDs in the next batch lose
// already-resolved display names.
func resolveExportBatchRelations[V any](
	ctx context.Context,
	logs []usage.UsageLog,
	cache map[int64]*V,
	maxEntries int,
	extract func(*usage.UsageLog) int64,
	loader func(context.Context, []int64) (map[int64]*V, error),
) (map[int64]*V, error) {
	if maxEntries <= 0 {
		maxEntries = maxExportAssocCacheEntries
	}

	batchViews := make(map[int64]*V)
	var (
		batchIDs   []int64
		missingIDs []int64
	)
	for i := range logs {
		id := extract(&logs[i])
		if id <= 0 {
			continue
		}
		if _, seenInBatch := batchViews[id]; seenInBatch {
			continue
		}
		batchIDs = append(batchIDs, id)
		if cachedView, inCache := cache[id]; inCache {
			batchViews[id] = cachedView
			continue
		}
		batchViews[id] = nil
		missingIDs = append(missingIDs, id)
	}

	if len(missingIDs) == 0 {
		return batchViews, nil
	}

	loaded, err := loader(ctx, missingIDs)
	if err != nil {
		return nil, err
	}
	for _, id := range missingIDs {
		batchViews[id] = loaded[id]
	}

	if len(cache)+len(missingIDs) <= maxEntries {
		for _, id := range missingIDs {
			cache[id] = batchViews[id]
		}
		return batchViews, nil
	}

	// Evict stale cross-batch entries at maxEntries and seed cache from batchIDs
	// so every hot ID referenced in the current batch stays warm for the next batch.
	clear(cache)
	for _, id := range batchIDs {
		if len(cache) >= maxEntries {
			break
		}
		cache[id] = batchViews[id]
	}
	return batchViews, nil
}
