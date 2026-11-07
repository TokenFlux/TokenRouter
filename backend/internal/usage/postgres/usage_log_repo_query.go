package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	dbapikey "github.com/TokenFlux/TokenRouter/ent/apikey"
	dbgroup "github.com/TokenFlux/TokenRouter/ent/group"
	dbprovider "github.com/TokenFlux/TokenRouter/ent/provider"
	"github.com/TokenFlux/TokenRouter/ent/schema/mixins"
	dbuser "github.com/TokenFlux/TokenRouter/ent/user"
	dbusersub "github.com/TokenFlux/TokenRouter/ent/usersubscription"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	sqlutil "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// usage_log_repo_query.go queries and paginates usage records and loads their
// current display associations.
// Usage listings read retained analytical rows and apply authorization scopes
// chosen by their callers. An owned-team scope uses separate user and team
// branches, excluding owner rows from the team branch so records belong to the
// result once even when the owner used a team key.
//
// Exact pagination counts matches before listing, while broad filtered lists can
// fetch one extra row and return an approximate total from the requested offset
// and observed rows. Raw row scanning keeps nullable diagnostic fields and
// normalizes legacy transport evidence. ListWithFilters then loads current
// associations in batches; rows with the same association ID share that display
// pointer, so consumers copy associated values before changing them.

// usageLogSelectColumns defines the positional raw-row read shape; scanUsageLog
// consumes the same order, and financial BillingKey is currently outside this
// shape.
const usageLogSelectColumns = "id, user_id, billing_user_id, team_id, api_key_id, provider_id, request_id, model, requested_model, upstream_model, group_id, subscription_id, input_tokens, output_tokens, cache_creation_tokens, cache_read_tokens, cache_creation_5m_tokens, cache_creation_1h_tokens, image_output_tokens, image_output_cost, image_input_tokens, image_input_cost, input_cost, output_cost, cache_creation_cost, cache_read_cost, total_cost, actual_cost, subscription_amount_usd, balance_amount_usd, billing_allocations, rate_multiplier, provider_rate_multiplier, billing_type, request_type, stream, openai_ws_mode, duration_ms, timings, user_agent, ip_address, image_count, image_frame_count, image_cache_read_tokens, image_size, image_input_size, image_output_size, image_size_source, image_size_breakdown, video_count, video_resolution, video_duration_seconds, service_tier, reasoning_effort, inbound_endpoint, upstream_endpoint, cache_ttl_overridden, long_context_billing_applied, pricing_config_id, model_mapping_chain, billing_tier, billing_mode, provider_stats_cost, upstream_request_id, session_id, created_at, requested_reasoning_effort, native_compaction_v2, platform, upstream_response_model, upstream_model_mismatch, request_source, system_usage"

// UsageLogFilters carries the usage module's selectors for authorized record
// listings.
type UsageLogFilters = usage.UsageLogFilters

// usageLogIDs holds unique relation identifiers for batched current-display
// hydration.
type usageLogIDs struct {
	userIDs         []int64 // acting user identifiers
	apiKeyIDs       []int64 // recorded key identifiers
	providerIDs     []int64 // execution provider identifiers
	groupIDs        []int64 // present routing group identifiers
	subscriptionIDs []int64 // present subscription grant identifiers
}

// GetByID returns a retained usage row by its database ID. Missing rows return
// usage.ErrUsageLogNotFound; associations stay unhydrated, and an otherwise
// successful row-close failure clears the result before returning an error.
func (r *Store) GetByID(ctx context.Context, id int64) (log *usage.UsageLog, err error) {
	query := "SELECT " + usageLogSelectColumns + " FROM usage_logs WHERE id = $1"
	rows, err := r.sql.QueryContext(ctx, query, id)
	if err != nil {
		return nil, err
	}
	defer func() {
		// A close failure becomes the error after an otherwise successful scan. Clear
		// returned rows in that case so callers cannot consume an incomplete query.
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
			log = nil
		}
	}()
	if !rows.Next() {
		if err = rows.Err(); err != nil {
			return nil, err
		}
		return nil, usage.ErrUsageLogNotFound
	}
	log, err = scanUsageLog(rows)
	if err != nil {
		return nil, err
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return log, nil
}

// ListByUser returns a counted page for an acting-user ID. The caller authorizes
// that ID; rows follow the pagination sort and retain unhydrated association
// fields from the raw scanner.
func (r *Store) ListByUser(ctx context.Context, userID int64, params pagination.PaginationParams) ([]usage.UsageLog, *pagination.PaginationResult, error) {
	return r.listUsageLogsWithPagination(ctx, "WHERE user_id = $1", []any{userID}, params)
}

// ListByAPIKey returns a counted page for the selected API-key ID. The caller
// authorizes the key; sorting uses pagination's allowed columns, and raw rows
// carry unhydrated association fields.
func (r *Store) ListByAPIKey(ctx context.Context, apiKeyID int64, params pagination.PaginationParams) ([]usage.UsageLog, *pagination.PaginationResult, error) {
	return r.listUsageLogsWithPagination(ctx, "WHERE api_key_id = $1", []any{apiKeyID}, params)
}

// ListByProvider returns a counted page for an execution-provider ID. The caller
// authorizes the provider; row sorting follows allowed pagination columns, and
// display associations remain unhydrated.
func (r *Store) ListByProvider(ctx context.Context, providerID int64, params pagination.PaginationParams) ([]usage.UsageLog, *pagination.PaginationResult, error) {
	return r.listUsageLogsWithPagination(ctx, "WHERE provider_id = $1", []any{providerID}, params)
}

// ListByUserAndTimeRange returns at most ten thousand actor rows in [startTime,
// endTime). Results use descending ID and nil pagination, so callers needing a
// complete count or larger list use paginated queries.
func (r *Store) ListByUserAndTimeRange(ctx context.Context, userID int64, startTime, endTime time.Time) ([]usage.UsageLog, *pagination.PaginationResult, error) {
	// Capping an unpaged scan can omit older row IDs; the returned nil page
	// provides no truncation marker for the selected window.
	query := "SELECT " + usageLogSelectColumns + " FROM usage_logs WHERE user_id = $1 AND created_at >= $2 AND created_at < $3 ORDER BY id DESC LIMIT 10000"
	logs, err := r.queryUsageLogs(ctx, query, userID, startTime, endTime)
	return logs, nil, err
}

// ListByAPIKeyAndTimeRange returns at most ten thousand key rows in the supplied
// half-open range. Results use descending ID and nil pagination; callers
// authorize the key and choose pagination for larger complete listings.
func (r *Store) ListByAPIKeyAndTimeRange(ctx context.Context, apiKeyID int64, startTime, endTime time.Time) ([]usage.UsageLog, *pagination.PaginationResult, error) {
	// Capping an unpaged scan can omit older row IDs; the returned nil page
	// provides no truncation marker for the selected window.
	query := "SELECT " + usageLogSelectColumns + " FROM usage_logs WHERE api_key_id = $1 AND created_at >= $2 AND created_at < $3 ORDER BY id DESC LIMIT 10000"
	logs, err := r.queryUsageLogs(ctx, query, apiKeyID, startTime, endTime)
	return logs, nil, err
}

// ListByProviderAndTimeRange returns at most ten thousand provider rows in the
// supplied half-open range. Rows use descending ID with nil pagination, and the
// caller authorizes the selected provider before exposing results.
func (r *Store) ListByProviderAndTimeRange(ctx context.Context, providerID int64, startTime, endTime time.Time) ([]usage.UsageLog, *pagination.PaginationResult, error) {
	// Capping an unpaged scan can omit older row IDs; the returned nil page
	// provides no truncation marker for the selected window.
	query := "SELECT " + usageLogSelectColumns + " FROM usage_logs WHERE provider_id = $1 AND created_at >= $2 AND created_at < $3 ORDER BY id DESC LIMIT 10000"
	logs, err := r.queryUsageLogs(ctx, query, providerID, startTime, endTime)
	return logs, nil, err
}

// ListByModelAndTimeRange returns at most ten thousand rows whose stored model
// equals modelName in the half-open range. Descending IDs and nil pagination
// apply; requested, upstream, and mapping dimensions use separate report
// helpers.
func (r *Store) ListByModelAndTimeRange(ctx context.Context, modelName string, startTime, endTime time.Time) ([]usage.UsageLog, *pagination.PaginationResult, error) {
	// Capping an unpaged scan can omit older row IDs; the returned nil page
	// provides no truncation marker for the selected window.
	query := fmt.Sprintf("SELECT %s FROM usage_logs WHERE %s = $1 AND created_at >= $2 AND created_at < $3 ORDER BY id DESC LIMIT 10000", usageLogSelectColumns, rawUsageLogModelColumn)
	logs, err := r.queryUsageLogs(ctx, query, modelName, startTime, endTime)
	return logs, nil, err
}

// Delete removes an analytical usage row by database ID. A missing row still
// returns successful SQL execution; callers authorize deletion and arrange any
// required rollup repair, while financial ledger entries remain separate.
func (r *Store) Delete(ctx context.Context, id int64) error {
	_, err := r.sql.ExecContext(ctx, "DELETE FROM usage_logs WHERE id = $1", id)
	return err
}

// ListWithFilters returns a page of matching usage records and current display
// associations. The caller authorizes filters; time endpoints are half-open,
// request IDs are trimmed for compatible lookup, and raw transport filtering
// gives request type precedence over stream.
//
// Owned-team inclusion resolves current ownership and keeps actor and team
// branches disjoint. ExactTotal or a user/key/provider/team selector requests a
// count; other broad filters can return a total derived from the requested
// offset and observed rows. Hydration shares loaded associations among rows
// carrying their IDs, so consumers treat those pointers as read-only.
func (r *Store) ListWithFilters(ctx context.Context, params pagination.PaginationParams, filters UsageLogFilters) ([]usage.UsageLog, *pagination.PaginationResult, error) {
	conditions := make([]string, 0, 9)
	args := make([]any, 0, 9)

	source, scopeCondition, args, err := r.buildUsageLogScopeSource(ctx, args, filters.UserID, filters.IncludeOwnedTeam, "")
	if err != nil {
		return nil, nil, err
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

	whereClause := buildWhere(conditions)
	var (
		logs    []usage.UsageLog
		page    *pagination.PaginationResult
		listErr error
	)
	// Broad filters can use the extra-row probe; narrow identity scopes and
	// ExactTotal use a separate count before listing their selected page.
	if shouldUseFastUsageLogTotal(filters) {
		logs, page, listErr = r.listUsageLogsWithFastPaginationFrom(ctx, source, whereClause, args, params)
	} else {
		logs, page, listErr = r.listUsageLogsWithPaginationFrom(ctx, source, whereClause, args, params)
	}
	if listErr != nil {
		return nil, nil, listErr
	}

	if err := r.hydrateUsageLogAssociations(ctx, logs); err != nil {
		return nil, nil, err
	}
	return logs, page, nil
}

// usageRequestIDCondition returns request-lookup SQL for a trusted column prefix
// and parameter number. Candidate request IDs come from request_lookup_ids, and
// callers bind the incoming ID alongside their ordinary authorization filters.
//
// An exact saved local record disables direct upstream_request_id equality for
// that incoming text. For example, local req-1 keeps a different row whose
// upstream alias is req-1 out of the direct alias branch. When no local record
// exists, that branch also finds older usage rows with a missing local request
// ID. Parent-run matches require system metadata explicitly, allowing the
// partial parent index to participate even in generic prepared plans.
func usageRequestIDCondition(prefix string, parameter int) string {
	lookup := fmt.Sprintf("ARRAY(SELECT id FROM request_lookup_ids($%d))", parameter)
	// Guard the direct upstream comparison with saved-local existence so external
	// evidence sharing that text cannot enlarge the exact local lookup.
	return fmt.Sprintf("(%srequest_id = ANY(%s) OR (%supstream_request_id = $%d"+
		" AND NOT EXISTS (SELECT 1 FROM request_records exact_request WHERE exact_request.request_id=$%d)) OR (%ssystem_usage IS NOT NULL AND %ssystem_usage->>'parent_request_id' = $%d))",
		prefix, lookup, prefix, parameter, parameter, prefix, prefix, parameter)
}

// buildUsageLogScopeSource returns a raw source, an optional scope predicate,
// and appended parameters. A nonpositive userID leaves the source unrestricted;
// the caller authorizes that choice and supplies a trusted alias.
//
// A user selector normally adds equality on user_id. Owned-team inclusion first
// resolves the user's current owner membership, then replaces the source with
// UNION ALL branches for the actor and team. Excluding that actor from the team
// branch prevents double counting owner records while retaining other members'
// team usage.
func (r *Store) buildUsageLogScopeSource(ctx context.Context, args []any, userID int64, includeOwnedTeam bool, alias string) (string, string, []any, error) {
	sourceAlias := alias
	if sourceAlias == "" {
		sourceAlias = "usage_scope"
	}
	baseSource := "usage_logs"
	if alias != "" {
		baseSource += " " + alias
	}
	if userID <= 0 {
		return baseSource, "", args, nil
	}

	args = append(args, userID)
	userPosition := len(args)
	prefix := ""
	if alias != "" {
		prefix = alias + "."
	}
	userCondition := fmt.Sprintf("%suser_id = $%d", prefix, userPosition)
	if !includeOwnedTeam {
		return baseSource, userCondition, args, nil
	}

	teamID, err := r.getOwnedTeamID(ctx, userID)
	if err != nil {
		return "", "", nil, err
	}
	if teamID <= 0 {
		return baseSource, userCondition, args, nil
	}
	args = append(args, teamID)
	teamPosition := len(args)
	source := fmt.Sprintf(`(
		SELECT * FROM usage_logs WHERE user_id = $%d
		UNION ALL
		SELECT * FROM usage_logs WHERE team_id = $%d AND user_id <> $%d
	) %s`, userPosition, teamPosition, userPosition, sourceAlias)
	return source, "", args, nil
}

// shouldUseFastUsageLogTotal reports whether the broad listing can use
// approximate pagination. ExactTotal and any nonzero user, key, provider, or
// team selector require counts; other filters alone retain the fast path.
func shouldUseFastUsageLogTotal(filters UsageLogFilters) bool {
	if filters.ExactTotal {
		return false
	}
	// Any nonzero identity selector selects counted pagination; broad model, date,
	// billing, or request-ID restrictions still permit the approximate total.
	return filters.UserID == 0 && filters.APIKeyID == 0 && filters.ProviderID == 0 && filters.TeamID == 0
}

// listUsageLogsWithPagination counts and lists retained raw rows using
// caller-supplied SQL restrictions. It delegates the ordered page and parameter
// positions to listUsageLogsWithPaginationFrom, with the usage_logs table as its
// source.
func (r *Store) listUsageLogsWithPagination(ctx context.Context, whereClause string, args []any, params pagination.PaginationParams) ([]usage.UsageLog, *pagination.PaginationResult, error) {
	return r.listUsageLogsWithPaginationFrom(ctx, "usage_logs", whereClause, args, params)
}

// listUsageLogsWithPaginationFrom returns a counted page from a trusted SQL
// source and restrictions. It copies args before adding limit/offset; count and
// listing are separate statements, with snapshot consistency determined by the
// selected executor.
func (r *Store) listUsageLogsWithPaginationFrom(ctx context.Context, source, whereClause string, args []any, params pagination.PaginationParams) ([]usage.UsageLog, *pagination.PaginationResult, error) {
	countQuery := "SELECT COUNT(*) FROM " + source + " " + whereClause
	var total int64
	if err := scanSingleRow(ctx, r.sql, countQuery, args, &total); err != nil {
		return nil, nil, err
	}

	limitPos := len(args) + 1
	offsetPos := len(args) + 2
	listArgs := append(append([]any{}, args...), params.Limit(), params.Offset())
	query := fmt.Sprintf("SELECT %s FROM %s %s ORDER BY %s LIMIT $%d OFFSET $%d", usageLogSelectColumns, source, whereClause, usageLogOrderBy(params), limitPos, offsetPos)
	logs, err := r.queryUsageLogs(ctx, query, listArgs...)
	if err != nil {
		return nil, nil, err
	}
	return logs, paginationResultFromTotal(total, params), nil
}

// listUsageLogsWithFastPaginationFrom returns a bounded page and an approximate
// total from a trusted SQL source. It copies the parameter slice and asks for
// one additional row beyond the requested limit.
//
// That extra row establishes hasMore and is removed from the returned page. With
// more rows, Total is offset plus limit plus one; otherwise it is offset plus
// the observed length. The result describes pagination progress, so callers
// needing the full matching cardinality choose the counted path instead.
func (r *Store) listUsageLogsWithFastPaginationFrom(ctx context.Context, source, whereClause string, args []any, params pagination.PaginationParams) ([]usage.UsageLog, *pagination.PaginationResult, error) {
	limit := params.Limit()
	offset := params.Offset()

	limitPos := len(args) + 1
	offsetPos := len(args) + 2
	listArgs := append(append([]any{}, args...), limit+1, offset)
	query := fmt.Sprintf("SELECT %s FROM %s %s ORDER BY %s LIMIT $%d OFFSET $%d", usageLogSelectColumns, source, whereClause, usageLogOrderBy(params), limitPos, offsetPos)

	logs, err := r.queryUsageLogs(ctx, query, listArgs...)
	if err != nil {
		return nil, nil, err
	}

	hasMore := false
	if len(logs) > limit {
		hasMore = true
		logs = logs[:limit]
	}

	total := int64(offset) + int64(len(logs))
	if hasMore {
		// The extra row proves another page exists; the total is a lower bound,
		// avoiding a full COUNT(*).
		total = int64(offset) + int64(limit) + 1
	}

	return logs, paginationResultFromTotal(total, params), nil
}

// usageLogOrderBy returns sorting SQL from allowed pagination columns and
// direction. Model sorting prefers a nonblank requested display model;
// unsupported columns use ID, and other columns add ID with the same direction
// for ties.
func usageLogOrderBy(params pagination.PaginationParams) string {
	sortBy := strings.ToLower(strings.TrimSpace(params.SortBy))
	sortOrder := strings.ToUpper(params.NormalizedSortOrder(pagination.SortOrderDesc))

	var column string
	switch sortBy {
	case "model":
		column = "COALESCE(NULLIF(TRIM(requested_model), ''), model)"
	case "created_at":
		column = "created_at"
	default:
		column = "id"
	}

	if column == "id" {
		return fmt.Sprintf("id %s", sortOrder)
	}
	return fmt.Sprintf("%s %s, id %s", column, sortOrder, sortOrder)
}

// queryUsageLogs reads and decodes every selected usage row into an allocated
// slice. Scan and iteration failures discard the result; an otherwise successful
// close failure also clears it and becomes the returned error.
func (r *Store) queryUsageLogs(ctx context.Context, query string, args ...any) (logs []usage.UsageLog, err error) {
	rows, err := r.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		// A close failure becomes the error after an otherwise successful scan. Clear
		// returned rows in that case so callers cannot consume an incomplete query.
		if closeErr := rows.Close(); closeErr != nil && err == nil {
			err = closeErr
			logs = nil
		}
	}()

	logs = make([]usage.UsageLog, 0)
	for rows.Next() {
		var log *usage.UsageLog
		log, err = scanUsageLog(rows)
		if err != nil {
			return nil, err
		}
		logs = append(logs, *log)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return logs, nil
}

// hydrateUsageLogAssociations attaches current display records to the supplied
// usage slice. Empty input returns immediately, and callers restrict usage rows
// to an authorized scope before association IDs are collected.
//
// Each relation is fetched once per unique ID rather than joined into the raw
// query. User loading includes soft-deleted records for historical display;
// other loaders retain their owning query rules. After every load succeeds,
// matching rows receive the same map-held pointer, so changes through one row
// can affect another unless the consumer copies that association.
func (r *Store) hydrateUsageLogAssociations(ctx context.Context, logs []usage.UsageLog) error {
	// Collect relation IDs after authorized usage selection. Load each relation set
	// through Ent, then publish the same loaded display pointer to its matching
	// rows.
	if len(logs) == 0 {
		return nil
	}

	ids := collectUsageLogIDs(logs)
	users, err := r.loadUsers(ctx, ids.userIDs)
	if err != nil {
		return err
	}
	apiKeys, err := r.loadAPIKeys(ctx, ids.apiKeyIDs)
	if err != nil {
		return err
	}
	providers, err := r.loadProviders(ctx, ids.providerIDs)
	if err != nil {
		return err
	}
	groups, err := r.loadGroups(ctx, ids.groupIDs)
	if err != nil {
		return err
	}
	subs, err := r.loadSubscriptions(ctx, ids.subscriptionIDs)
	if err != nil {
		return err
	}

	for i := range logs {
		if user, ok := users[logs[i].UserID]; ok {
			logs[i].User = user
		}
		if key, ok := apiKeys[logs[i].APIKeyID]; ok {
			logs[i].APIKey = key
		}
		if acc, ok := providers[logs[i].ProviderID]; ok {
			logs[i].Provider = acc
		}
		if logs[i].GroupID != nil {
			if group, ok := groups[*logs[i].GroupID]; ok {
				logs[i].Group = group
			}
		}
		if logs[i].SubscriptionID != nil {
			if sub, ok := subs[*logs[i].SubscriptionID]; ok {
				logs[i].Subscription = sub
			}
		}
	}
	return nil
}

// collectUsageLogIDs collects unique relation IDs needed for display hydration.
// Optional group/subscription IDs participate when present; user, key, and
// provider IDs are collected as supplied, and returned slice order follows map
// iteration.
func collectUsageLogIDs(logs []usage.UsageLog) usageLogIDs {
	idSet := func() map[int64]struct{} { return make(map[int64]struct{}) }

	userIDs := idSet()
	apiKeyIDs := idSet()
	providerIDs := idSet()
	groupIDs := idSet()
	subscriptionIDs := idSet()

	for i := range logs {
		userIDs[logs[i].UserID] = struct{}{}
		apiKeyIDs[logs[i].APIKeyID] = struct{}{}
		providerIDs[logs[i].ProviderID] = struct{}{}
		if logs[i].GroupID != nil {
			groupIDs[*logs[i].GroupID] = struct{}{}
		}
		if logs[i].SubscriptionID != nil {
			subscriptionIDs[*logs[i].SubscriptionID] = struct{}{}
		}
	}

	return usageLogIDs{
		userIDs:         setToSlice(userIDs),
		apiKeyIDs:       setToSlice(apiKeyIDs),
		providerIDs:     setToSlice(providerIDs),
		groupIDs:        setToSlice(groupIDs),
		subscriptionIDs: setToSlice(subscriptionIDs),
	}
}

// loadUsers returns current user views for supplied relation IDs, including
// soft-deleted users. Empty IDs return an empty map; callers authorize usage
// rows before gathering IDs, and one converted view is reused for each identity
// in the result.
func (r *Store) loadUsers(ctx context.Context, ids []int64) (map[int64]*usage.UserView, error) {
	out := make(map[int64]*usage.UserView)
	if len(ids) == 0 {
		return out, nil
	}
	// A user's deletion leaves historical usage visible. Skip user soft-delete
	// filtering after the caller has authorized and selected the referencing
	// records.
	models, err := r.client.User.Query().Where(dbuser.IDIn(ids...)).All(mixins.SkipSoftDelete(ctx))
	if err != nil {
		return nil, err
	}
	for _, m := range models {
		out[m.ID] = userEntityToService(m)
	}
	return out, nil
}

// loadAPIKeys returns mapped key views for supplied relation IDs using ordinary
// Ent query filtering. Empty IDs return an allocated empty map; missing or
// filtered keys are omitted, and callers authorize the usage scope.
func (r *Store) loadAPIKeys(ctx context.Context, ids []int64) (map[int64]*usage.KeyView, error) {
	out := make(map[int64]*usage.KeyView)
	if len(ids) == 0 {
		return out, nil
	}
	models, err := r.client.APIKey.Query().Where(dbapikey.IDIn(ids...)).All(ctx)
	if err != nil {
		return nil, err
	}
	for _, m := range models {
		out[m.ID] = apiKeyEntityToService(m)
	}
	return out, nil
}

// loadProviders returns provider ID/name views for supplied IDs using the Ent
// client's query rules. Empty input yields an allocated empty map, while missing
// or filtered provider entities are omitted.
func (r *Store) loadProviders(ctx context.Context, ids []int64) (map[int64]*usage.ProviderView, error) {
	out := make(map[int64]*usage.ProviderView)
	if len(ids) == 0 {
		return out, nil
	}
	models, err := r.client.Provider.Query().Where(dbprovider.IDIn(ids...)).All(ctx)
	if err != nil {
		return nil, err
	}
	for _, m := range models {
		out[m.ID] = providerEntityToService(m)
	}
	return out, nil
}

// loadGroups returns group views for supplied IDs using ordinary Ent filtering.
// Empty input yields an empty map; retained mutable values follow
// groupEntityToService's sharing rules, so association consumers treat them as
// read-only.
func (r *Store) loadGroups(ctx context.Context, ids []int64) (map[int64]*usage.GroupView, error) {
	out := make(map[int64]*usage.GroupView)
	if len(ids) == 0 {
		return out, nil
	}
	models, err := r.client.Group.Query().Where(dbgroup.IDIn(ids...)).All(ctx)
	if err != nil {
		return nil, err
	}
	for _, m := range models {
		out[m.ID] = groupEntityToService(m)
	}
	return out, nil
}

// loadSubscriptions returns billing-mapped subscriptions for selected relation
// IDs using ordinary Ent filtering. Empty input yields an empty map; optional
// pointer ownership follows billing's mapper, and omitted rows leave
// associations unset.
func (r *Store) loadSubscriptions(ctx context.Context, ids []int64) (map[int64]*billing.UserSubscription, error) {
	out := make(map[int64]*billing.UserSubscription)
	if len(ids) == 0 {
		return out, nil
	}
	models, err := r.client.UserSubscription.Query().Where(dbusersub.IDIn(ids...)).All(ctx)
	if err != nil {
		return nil, err
	}
	for _, m := range models {
		out[m.ID] = userSubscriptionEntityToService(m)
	}
	return out, nil
}

// scanUsageLog decodes the positional usage SELECT into a row with nullable
// diagnostic values. The scanner's order must agree with usageLogSelectColumns,
// and errors return before the row is exposed.
//
// Stored request type is resolved together with legacy stream/WebSocket flags,
// then those flags are normalized from the effective type. RequestedModel
// retains nonblank stored display text or uses model for history. Allocation
// JSON errors abort decoding, while malformed image-size JSON yields nil
// metadata. BillingKey is absent from this SELECT and remains empty in decoded
// rows.
func scanUsageLog(scanner interface {
	// Scan fills positional destinations from the reader's row representation.
	// The implementation owns conversion rules and errors, which scanUsageLog
	// propagates before constructing a returned usage record.
	Scan(...any) error
},
) (*usage.UsageLog, error) {
	var (
		id                        int64
		userID                    sql.NullInt64
		billingUserID             sql.NullInt64
		teamID                    sql.NullInt64
		apiKeyID                  sql.NullInt64
		providerID                int64
		requestID                 sql.NullString
		model                     string
		requestedModel            sql.NullString
		upstreamModel             sql.NullString
		upstreamResponseModel     sql.NullString
		upstreamModelMismatch     sql.NullBool
		groupID                   sql.NullInt64
		subscriptionID            sql.NullInt64
		inputTokens               int
		outputTokens              int
		cacheCreationTokens       int
		cacheReadTokens           int
		cacheCreation5m           int
		cacheCreation1h           int
		imageOutputTokens         int
		imageOutputCost           float64
		imageInputTokens          int
		imageInputCost            float64
		inputCost                 float64
		outputCost                float64
		cacheCreationCost         float64
		cacheReadCost             float64
		totalCost                 float64
		actualCost                float64
		subscriptionAmountUSD     float64
		balanceAmountUSD          float64
		billingAllocationsRaw     []byte
		rateMultiplier            float64
		providerRateMultiplier    sql.NullFloat64
		billingType               int16
		requestTypeRaw            int16
		stream                    bool
		openaiWSMode              bool
		durationMs                sql.NullInt64
		timingsJSON               []byte
		userAgent                 sql.NullString
		ipAddress                 sql.NullString
		imageCount                int
		imageFrameCount           int
		imageCacheReadTokens      int
		imageSize                 sql.NullString
		imageInputSize            sql.NullString
		imageOutputSize           sql.NullString
		imageSizeSource           sql.NullString
		imageSizeBreakdown        sql.NullString
		videoCount                int
		videoResolution           sql.NullString
		videoDurationSeconds      sql.NullInt64
		serviceTier               sql.NullString
		reasoningEffort           sql.NullString
		inboundEndpoint           sql.NullString
		upstreamEndpoint          sql.NullString
		cacheTTLOverridden        bool
		longContextBillingApplied bool
		pricingConfigID           sql.NullInt64
		modelMappingChain         sql.NullString
		billingTier               sql.NullString
		billingMode               sql.NullString
		providerStatsCost         sql.NullFloat64
		upstreamRequestID         sql.NullString
		sessionID                 sql.NullString
		createdAt                 time.Time
		requestedReasoningEffort  sql.NullString
		nativeCompactionV2        bool
		platform                  string
		requestSource             string
		systemJSON                []byte
	)

	if err := scanner.Scan(
		&id,
		&userID,
		&billingUserID,
		&teamID,
		&apiKeyID,
		&providerID,
		&requestID,
		&model,
		&requestedModel,
		&upstreamModel,
		&groupID,
		&subscriptionID,
		&inputTokens,
		&outputTokens,
		&cacheCreationTokens,
		&cacheReadTokens,
		&cacheCreation5m,
		&cacheCreation1h,
		&imageOutputTokens,
		&imageOutputCost,
		&imageInputTokens,
		&imageInputCost,
		&inputCost,
		&outputCost,
		&cacheCreationCost,
		&cacheReadCost,
		&totalCost,
		&actualCost,
		&subscriptionAmountUSD,
		&balanceAmountUSD,
		&billingAllocationsRaw,
		&rateMultiplier,
		&providerRateMultiplier,
		&billingType,
		&requestTypeRaw,
		&stream,
		&openaiWSMode,
		&durationMs,
		&timingsJSON,
		&userAgent,
		&ipAddress,
		&imageCount,
		&imageFrameCount,
		&imageCacheReadTokens,
		&imageSize,
		&imageInputSize,
		&imageOutputSize,
		&imageSizeSource,
		&imageSizeBreakdown,
		&videoCount,
		&videoResolution,
		&videoDurationSeconds,
		&serviceTier,
		&reasoningEffort,
		&inboundEndpoint,
		&upstreamEndpoint,
		&cacheTTLOverridden,
		&longContextBillingApplied,
		&pricingConfigID,
		&modelMappingChain,
		&billingTier,
		&billingMode,
		&providerStatsCost,
		&upstreamRequestID,
		&sessionID,
		&createdAt,
		&requestedReasoningEffort,
		&nativeCompactionV2,
		&platform,
		&upstreamResponseModel,
		&upstreamModelMismatch,
		&requestSource,
		&systemJSON,
	); err != nil {
		return nil, err
	}

	log := &usage.UsageLog{
		ID:                        id,
		RequestSource:             requestSource,
		UserID:                    userID.Int64,
		BillingUserID:             billingUserID.Int64,
		APIKeyID:                  apiKeyID.Int64,
		ProviderID:                providerID,
		Platform:                  platform,
		Model:                     model,
		RequestedModel:            coalesceTrimmedString(requestedModel, model),
		InputTokens:               inputTokens,
		OutputTokens:              outputTokens,
		CacheCreationTokens:       cacheCreationTokens,
		CacheReadTokens:           cacheReadTokens,
		CacheCreation5mTokens:     cacheCreation5m,
		CacheCreation1hTokens:     cacheCreation1h,
		ImageOutputTokens:         imageOutputTokens,
		ImageOutputCost:           imageOutputCost,
		ImageInputTokens:          imageInputTokens,
		ImageInputCost:            imageInputCost,
		InputCost:                 inputCost,
		OutputCost:                outputCost,
		CacheCreationCost:         cacheCreationCost,
		CacheReadCost:             cacheReadCost,
		TotalCost:                 totalCost,
		ActualCost:                actualCost,
		SubscriptionAmountUSD:     subscriptionAmountUSD,
		BalanceAmountUSD:          balanceAmountUSD,
		RateMultiplier:            rateMultiplier,
		ProviderRateMultiplier:    nullFloat64Ptr(providerRateMultiplier),
		BillingType:               int8(billingType),
		RequestType:               usage.RequestTypeFromInt16(requestTypeRaw),
		ImageCount:                imageCount,
		ImageFrameCount:           imageFrameCount,
		ImageCacheReadTokens:      imageCacheReadTokens,
		VideoCount:                videoCount,
		CacheTTLOverridden:        cacheTTLOverridden,
		LongContextBillingApplied: longContextBillingApplied,
		CreatedAt:                 createdAt,
		NativeCompactionV2:        nativeCompactionV2,
	}
	if len(systemJSON) > 0 {
		if err := json.Unmarshal(systemJSON, &log.SystemUsage); err != nil {
			return nil, fmt.Errorf("decode system usage: %w", err)
		}
	}
	// Unknown stored types need both legacy flags to recover transport identity;
	// explicit types then normalize those flags for consumers of older fields.
	log.Stream = stream
	log.OpenAIWSMode = openaiWSMode
	log.RequestType = log.EffectiveRequestType()
	log.Stream, log.OpenAIWSMode = usage.ApplyLegacyRequestFields(log.RequestType, stream, openaiWSMode)

	if requestID.Valid {
		log.RequestID = requestID.String
	}
	if groupID.Valid {
		value := groupID.Int64
		log.GroupID = &value
	}
	if teamID.Valid {
		value := teamID.Int64
		log.TeamID = &value
	}
	if subscriptionID.Valid {
		value := subscriptionID.Int64
		log.SubscriptionID = &value
	}
	if durationMs.Valid {
		value := int(durationMs.Int64)
		log.DurationMs = &value
	}
	if len(timingsJSON) > 0 {
		if err := json.Unmarshal(timingsJSON, &log.Timings); err != nil {
			return nil, fmt.Errorf("decode usage timings: %w", err)
		}
	}
	if userAgent.Valid {
		log.UserAgent = &userAgent.String
	}
	if ipAddress.Valid {
		log.IPAddress = &ipAddress.String
	}
	if imageSize.Valid {
		log.ImageSize = &imageSize.String
	}
	if imageInputSize.Valid {
		log.ImageInputSize = &imageInputSize.String
	}
	if imageOutputSize.Valid {
		log.ImageOutputSize = &imageOutputSize.String
	}
	if imageSizeSource.Valid {
		log.ImageSizeSource = &imageSizeSource.String
	}
	log.ImageSizeBreakdown = stringIntMapFromNullJSON(imageSizeBreakdown)
	if videoResolution.Valid {
		log.VideoResolution = &videoResolution.String
	}
	if videoDurationSeconds.Valid {
		value := int(videoDurationSeconds.Int64)
		log.VideoDurationSeconds = &value
	}
	if serviceTier.Valid {
		log.ServiceTier = &serviceTier.String
	}
	if reasoningEffort.Valid {
		log.ReasoningEffort = &reasoningEffort.String
	}
	if requestedReasoningEffort.Valid {
		log.RequestedReasoningEffort = &requestedReasoningEffort.String
	}
	if inboundEndpoint.Valid {
		log.InboundEndpoint = &inboundEndpoint.String
	}
	if upstreamEndpoint.Valid {
		log.UpstreamEndpoint = &upstreamEndpoint.String
	}
	if upstreamResponseModel.Valid {
		log.UpstreamResponseModel = &upstreamResponseModel.String
	}
	if upstreamModelMismatch.Valid {
		log.UpstreamModelMismatch = &upstreamModelMismatch.Bool
	}
	if upstreamModel.Valid {
		log.UpstreamModel = &upstreamModel.String
	}
	if pricingConfigID.Valid {
		value := pricingConfigID.Int64
		log.PricingConfigID = &value
	}
	if modelMappingChain.Valid {
		log.ModelMappingChain = &modelMappingChain.String
	}
	if billingTier.Valid {
		log.BillingTier = &billingTier.String
	}
	if billingMode.Valid {
		log.BillingMode = &billingMode.String
	}
	if providerStatsCost.Valid {
		log.ProviderStatsCost = &providerStatsCost.Float64
	}
	if len(billingAllocationsRaw) > 0 {
		// Allocation content participates in displayed funding details; malformed JSON
		// aborts this row so callers do not receive a silently incomplete allocation
		// list.
		if err := json.Unmarshal(billingAllocationsRaw, &log.BillingAllocations); err != nil {
			return nil, err
		}
	}
	if sessionID.Valid {
		log.SessionID = &sessionID.String
	}
	if upstreamRequestID.Valid {
		log.UpstreamRequestID = &upstreamRequestID.String
	}

	return log, nil
}

// nullInt64 converts an optional integer pointer through infra's nullable
// representation. Nil becomes an invalid SQL value, while a present zero stays
// valid for consumers that distinguish absence from zero.
func nullInt64(v *int64) sql.NullInt64 { return sqlutil.NullableInt64(v) }

// nullInt converts an optional int to a nullable SQL int64. Nil produces an
// invalid value, while a present zero remains valid and can represent an
// observed zero duration.
func nullInt(v *int) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*v), Valid: true}
}

// nullFloat64Ptr returns a fresh float pointer for a valid SQL value. SQL NULL
// returns nil, keeping absence distinct from a stored zero provider multiplier
// or cost.
func nullFloat64Ptr(v sql.NullFloat64) *float64 {
	if !v.Valid {
		return nil
	}
	out := v.Float64
	return &out
}

// nullString converts nil or empty text to SQL NULL. Nonempty values, including
// whitespace, remain supplied without trimming so the owning field controls
// whether that text is meaningful.
func nullString(v *string) sql.NullString {
	if v == nil || *v == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: *v, Valid: true}
}

// nullStringIntMapJSON serializes nonempty image-size counts for a JSONB
// argument. Empty input or an encoding failure returns nil, letting metadata
// loss remain separate from required usage insert fields.
func nullStringIntMapJSON(v map[string]int) any {
	if len(v) == 0 {
		return nil
	}
	payload, err := json.Marshal(v)
	// Optional image metadata can be omitted if encoding fails; required token,
	// charge, and identity fields remain available to the prepared insert.
	if err != nil {
		return nil
	}
	return string(payload)
}

// stringIntMapFromNullJSON decodes image-size count metadata when SQL text is
// present. Null, blank, malformed, or empty maps return nil; successful decoding
// creates the returned map for this row.
func stringIntMapFromNullJSON(v sql.NullString) map[string]int {
	if !v.Valid || strings.TrimSpace(v.String) == "" {
		return nil
	}
	var out map[string]int
	// Optional image breakdowns are display metadata; malformed text becomes absent
	// metadata so an otherwise readable usage row still reaches listing callers.
	if err := json.Unmarshal([]byte(v.String), &out); err != nil {
		return nil
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// coalesceTrimmedString keeps a valid nonblank stored string or selects
// fallback. Trimming determines blankness alone, so nonblank stored text retains
// its surrounding whitespace in the returned display value.
func coalesceTrimmedString(v sql.NullString, fallback string) string {
	// Legacy rows can lack requested display text. Retain their stored model as the
	// available display value while keeping populated requested text intact.
	if v.Valid && strings.TrimSpace(v.String) != "" {
		return v.String
	}
	return fallback
}

// setToSlice returns a fresh slice containing the map's integer keys.
func setToSlice(set map[int64]struct{}) []int64 {
	out := make([]int64, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	return out
}
