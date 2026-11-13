//go:build integration

package postgres

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/requestlog"
	"github.com/TokenFlux/TokenRouter/internal/testutil/postgrescontainer"
	"github.com/TokenFlux/TokenRouter/migrations"
)

// store_integration_test.go checks request lookup scope, child billing aliases,
// retained sources, and sparse query plans against migrated PostgreSQL.
// Migrated scenarios store real snapshots and business rows, then compare
// ordinary-user and administrator visibility after replay, cleanup, and
// team-owner changes. Usage cases also preserve distinct charges sharing an ID
// and filter inaccessible rows before applying the global result limit.
// Provider labels resolve retained rows for administrator details, including
// attempts that are still pending a write.
//
// Execution-plan cases populate large sparse-match fixtures and capture nested
// SQL through auto_explain notices on a dedicated pq connection. Recreating the
// billing expression index after table analysis leaves its new statistics
// absent, exposing upgrade-sensitive planning. The old lookup function is
// required to reproduce a usage scan before staged migration is checked under
// custom and generic plans. Timing logs record fixture migration duration;
// assertions inspect IDs and scan nodes rather than imposing a latency
// threshold. Usage-query cases omit system metadata statistics and inspect
// executed rows and discarded rows, catching time-index traversals that return
// few results without using a sequential scan.

// lookupPlanNode decodes recursive scan identity and execution counts from
// explained PostgreSQL plans.
type lookupPlanNode struct {
	Type     string           `json:"Node Type"`              // explained execution node kind
	Relation string           `json:"Relation Name"`          // scanned database relation name
	Plans    []lookupPlanNode `json:"Plans"`                  // nested execution plan nodes
	Loops    float64          `json:"Actual Loops"`           // actual node execution count
	Index    string           `json:"Index Name"`             // selected database index name
	Rows     float64          `json:"Actual Rows"`            // average rows per execution
	Filtered float64          `json:"Rows Removed by Filter"` // average discarded rows per execution
}

// TestStoreFindUsageWithoutSystemStatistics reproduces sparse usage lookup after
// an upgrade leaves system metadata statistics absent. The production SQL must
// restrict executed scans under custom and generic plans, before and after
// statistics are collected, including zero-match and parent-run lookups.
func TestStoreFindUsageWithoutSystemStatistics(t *testing.T) {
	db := postgrescontainer.New(t)
	ctx := t.Context()
	// Keep unrelated column statistics while suppressing system metadata sampling,
	// then recreate its expression index to reproduce the newly migrated catalog.
	_, err := db.ExecContext(ctx, `
ALTER TABLE usage_logs SET (autovacuum_enabled=false);
ALTER TABLE usage_logs ALTER COLUMN system_usage SET STATISTICS 0;
INSERT INTO users(id,email,password_hash) VALUES (101,'usage-plan@requests.test','hash');
INSERT INTO api_keys(id,user_id,key,name) VALUES (101,101,'usage-plan-key','lookup');
INSERT INTO providers(id,name,platform,type) VALUES (101,'usage-plan','openai','apikey');
INSERT INTO usage_logs(user_id,billing_user_id,api_key_id,provider_id,request_id,upstream_request_id,model,created_at)
SELECT 101,101,101,101,'request-'||g,'supplier-'||g,'model','2026-01-01'::timestamptz+g*interval '1 second'
FROM generate_series(1,100000) g;
INSERT INTO usage_logs(provider_id,request_id,request_source,system_usage,model)
VALUES (101,'system-child','group_probe','{"parent_request_id":"system-parent"}','model');
ANALYZE usage_logs;
ALTER TABLE usage_logs ALTER COLUMN system_usage SET STATISTICS -1;
DROP INDEX idx_usage_logs_system_parent;
CREATE INDEX idx_usage_logs_system_parent ON usage_logs ((system_usage->>'parent_request_id')) WHERE system_usage IS NOT NULL;`)
	require.NoError(t, err)
	var statistics int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM pg_stats
 WHERE tablename='idx_usage_logs_system_parent' OR (tablename='usage_logs' AND attname='system_usage')`).Scan(&statistics))
	require.Zero(t, statistics)
	for _, analyzed := range []bool{false, true} {
		if analyzed {
			_, err = db.ExecContext(ctx, "ANALYZE usage_logs")
			require.NoError(t, err)
		}
		for _, mode := range []string{"force_custom_plan", "force_generic_plan"} {
			t.Run(fmt.Sprintf("analyzed=%t/%s", analyzed, mode), func(t *testing.T) {
				conn, err := db.Conn(ctx)
				require.NoError(t, err)
				t.Cleanup(func() { require.NoError(t, conn.Close()) })
				// Plan both modes on one connection using the same statement as Find;
				// retain PostgreSQL's normal scan choices so cost regressions remain visible.
				_, err = conn.ExecContext(ctx, "SET plan_cache_mode="+mode+"; DEALLOCATE ALL; PREPARE usage_lookup(text[],bigint,boolean,text,boolean) AS "+findUsageSQL)
				require.NoError(t, err)
				for _, statement := range []string{
					"EXECUTE usage_lookup(ARRAY['request-1'],0,true,'request-1',true)",
					"EXECUTE usage_lookup(ARRAY['request-1'],0,true,'supplier-1',false)",
					"EXECUTE usage_lookup(ARRAY['system-parent'],0,true,'system-parent',true)",
					"EXECUTE usage_lookup(ARRAY['missing'],0,true,'missing',false)",
				} {
					plan := explainUsageLookup(t, conn, statement)
					assertSparseUsageLookup(t, plan)
				}
			})
		}
	}
}

// explainUsageLookup executes a prepared usage lookup and returns its analyzed
// root plan. The caller owns the connection and supplies a fixture-only SQL
// statement; production query parameters are defined by that prepared statement.
func explainUsageLookup(t *testing.T, conn *sql.Conn, statement string) lookupPlanNode {
	t.Helper()
	var raw string
	require.NoError(t, conn.QueryRowContext(t.Context(), "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON, TIMING OFF) "+statement).Scan(&raw))
	var plans []struct {
		Plan lookupPlanNode `json:"Plan"` // decoded execution plan root
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &plans))
	require.Len(t, plans, 1)
	return plans[0].Plan
}

// assertSparseUsageLookup rejects broad executed scans of the sparse fixture.
// Check rows discarded by filters as well as returned rows because a time-index
// scan can traverse the entire table while returning only one matching record.
func assertSparseUsageLookup(t *testing.T, node lookupPlanNode) {
	t.Helper()
	if node.Relation == "usage_logs" && node.Loops > 0 {
		require.NotContains(t, node.Type, "Seq Scan", "%+v", node)
		require.NotEqual(t, "idx_usage_logs_created_at", node.Index, "%+v", node)
		// Each lookup matches at most two branch rows; allow planner variation while
		// rejecting work that scales with the hundred-thousand-row fixture size.
		require.LessOrEqual(t, (node.Rows+node.Filtered)*node.Loops, float64(10), "%+v", node)
	}
	for _, child := range node.Plans {
		assertSparseUsageLookup(t, child)
	}
}

// TestStoreFindUsageUnionPreservesVisibility checks overlapping ID sources and
// multiple charges sharing one request ID. Permissions precede the global row
// limit, and active team owners retain access without exposing system records.
func TestStoreFindUsageUnionPreservesVisibility(t *testing.T) {
	db := postgrescontainer.New(t)
	ctx := t.Context()
	_, err := db.ExecContext(ctx, `
INSERT INTO users(id,email,password_hash) VALUES
 (101,'usage-owner@requests.test','hash'),(202,'usage-other@requests.test','hash'),(303,'usage-team@requests.test','hash');
INSERT INTO api_keys(id,user_id,key,name) VALUES
 (101,101,'usage-owner-one','lookup'),(102,101,'usage-owner-two','lookup'),(202,202,'usage-other-key','lookup');
INSERT INTO providers(id,name,platform,type) VALUES (101,'usage-union','openai','apikey');
INSERT INTO usage_logs(id,user_id,billing_user_id,api_key_id,provider_id,request_id,upstream_request_id,model) VALUES
 (901,101,101,101,101,'local','shared','model'),
 (902,101,101,102,101,'local','shared','model'),
 (903,202,202,202,101,'foreign','local','model');
INSERT INTO usage_logs(id,provider_id,request_id,upstream_request_id,request_source,system_usage,model)
VALUES (905,101,'system','shared','group_probe','{"parent_request_id":"shared"}','model');`)
	require.NoError(t, err)
	var teamID int64
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO teams(name) VALUES('usage-lookup') RETURNING id`).Scan(&teamID))
	_, err = db.ExecContext(ctx, `INSERT INTO team_memberships(team_id,user_id,role) VALUES($1,303,'owner')`, teamID)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO usage_logs(id,user_id,billing_user_id,team_id,api_key_id,provider_id,request_id,model)
VALUES(906,202,202,$1,202,101,'team','model')`, teamID)
	require.NoError(t, err)
	store := NewStore(db)
	for _, test := range []struct {
		name   string   // visibility scenario label
		ids    []string // resolved local request identifiers
		search string   // original supplied search identifier
		userID int64    // authenticated requesting user identifier
		admin  bool     // trusted administrator access flag
		exact  bool     // existing local identifier flag
		want   []int64  // expected stored usage identifiers
	}{
		{"exact excludes upstream collision", []string{"local"}, "local", 0, true, true, []int64{901, 902}},
		{"external includes collision", []string{"local"}, "local", 0, true, false, []int64{901, 902, 903}},
		{"overlapping sources deduplicate rows", []string{"local", "system"}, "shared", 0, true, false, []int64{901, 902, 905}},
		{"user cannot read system usage", []string{"local", "system"}, "shared", 101, false, false, []int64{901, 902}},
		{"unrelated user has no access", []string{"local", "system"}, "shared", 202, false, false, nil},
		{"active team owner", []string{"team"}, "team", 303, false, true, []int64{906}},
		{"empty search result", []string{"missing"}, "missing", 0, true, false, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := findUsageIDs(t, store, test.ids, test.search, test.userID, test.admin, test.exact)
			require.ElementsMatch(t, test.want, got)
		})
	}
	_, err = db.ExecContext(ctx, `UPDATE team_memberships SET left_at=now() WHERE team_id=$1 AND user_id=303`, teamID)
	require.NoError(t, err)
	require.Empty(t, findUsageIDs(t, store, []string{"team"}, "team", 303, false, true))
	// Place inaccessible rows newer than every visible row so limiting before the
	// ownership check would incorrectly hide the user's entire matching history.
	_, err = db.ExecContext(ctx, `
INSERT INTO usage_logs(id,user_id,billing_user_id,api_key_id,provider_id,request_id,upstream_request_id,model,created_at)
SELECT 910000+g,101,101,101,101,'visible-'||g,'limited','model','2026-01-01'::timestamptz+g*interval '1 second'
FROM generate_series(1,120) g;
INSERT INTO usage_logs(id,user_id,billing_user_id,api_key_id,provider_id,request_id,upstream_request_id,model,created_at)
SELECT 920000+g,202,202,202,101,'hidden-'||g,'limited','model','2026-02-01'::timestamptz+g*interval '1 second'
FROM generate_series(1,150) g;`)
	require.NoError(t, err)
	var ids []string
	for i := 1; i <= 120; i++ {
		ids = append(ids, fmt.Sprintf("visible-%d", i))
	}
	got := findUsageIDs(t, store, ids, "limited", 101, false, false)
	require.Len(t, got, 101)
	for i, id := range got {
		require.Equal(t, int64(910120-i), id)
	}
	got = findUsageIDs(t, store, ids, "limited", 0, true, false)
	require.Len(t, got, 101)
	for i, id := range got {
		require.Equal(t, int64(920150-i), id)
	}
}

// findUsageIDs returns stored usage primary keys in the query's result order.
// Each call starts without summaries so the assertions observe usage ordering
// directly, including distinct charges with the same logical request ID.
func findUsageIDs(t *testing.T, store *Store, ids []string, search string, userID int64, admin, exact bool) []int64 {
	t.Helper()
	var details []requestlog.Detail
	require.NoError(t, store.findUsage(t.Context(), ids, search, userID, admin, exact, &details, map[string]int{}))
	var result []int64
	for _, detail := range details {
		for _, usage := range detail.Usage {
			result = append(result, usage.ID)
		}
	}
	return result
}

// TestStoreLookupWithoutBillingStatistics models table statistics with a
// recreated unanalyzed billing index. It requires the older lookup's executed
// usage scan, then checks staged repair, candidate identity, and selected scan
// behavior under custom and generic plans.
func TestStoreLookupWithoutBillingStatistics(t *testing.T) {
	db := postgrescontainer.New(t)
	ctx := t.Context()
	// Analyze populated tables before recreating the billing expression index,
	// leaving that new index without its own statistics while sparse lookup
	// candidates remain realistic.
	_, err := db.ExecContext(ctx, `
ALTER TABLE usage_logs SET (autovacuum_enabled=false);
INSERT INTO users(id,email,password_hash) VALUES (101,'large-lookup@requests.test','hash');
INSERT INTO api_keys(id,user_id,key,name) VALUES (101,101,'large-lookup-key','lookup');
INSERT INTO providers(id,name,platform,type) VALUES (101,'lookup','openai','apikey');
INSERT INTO usage_logs(user_id,billing_user_id,api_key_id,provider_id,request_id,billing_key,upstream_request_id,model)
SELECT 101,101,101,101,'request-'||g,'client:request-'||g,'supplier-'||g,'model'
FROM generate_series(1,100000) g;
INSERT INTO request_records(request_id,started_at,updated_at,revision,record,aliases)
SELECT 'request-'||g,now(),now(),1,jsonb_build_object('request_id','request-'||g,'api_key_id',101,
 'aliases',jsonb_build_array(jsonb_build_object('kind','billing','value','client:request-'||g))),
 ARRAY['client:request-'||g] FROM generate_series(1,20000) g;
INSERT INTO ops_error_logs(request_id,client_request_id,user_id,error_phase,error_type,status_code)
SELECT 'error-'||g,'error-client-'||g,101,'upstream','api_error',502 FROM generate_series(1,20000) g;
INSERT INTO ops_system_logs(request_id,client_request_id,level,message)
SELECT 'system-'||g,'system-client-'||g,'info','lookup fixture' FROM generate_series(1,20000) g;
INSERT INTO audit_logs(request_id,actor_user_id,status_code)
SELECT 'audit-'||g,101,200 FROM generate_series(1,20000) g;
ANALYZE usage_logs;
ANALYZE request_records;
ANALYZE ops_error_logs;
ANALYZE ops_system_logs;
ANALYZE audit_logs;
SELECT gin_clean_pending_list('request_records_aliases_idx');
DROP INDEX idx_usage_logs_billing_key_api_key;
CREATE UNIQUE INDEX idx_usage_logs_billing_key_api_key ON usage_logs(COALESCE(billing_key,request_id),api_key_id);`)
	require.NoError(t, err)
	var statistics int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM pg_stats WHERE tablename='idx_usage_logs_billing_key_api_key'`).Scan(&statistics))
	require.Zero(t, statistics)

	// Restore migration 294 on the unanalyzed-index fixture before applying
	// migration 295, making the old executed scan a prerequisite for this repair
	// comparison.
	previous, err := migrations.FS.ReadFile("294_request_lookup_child_billing.sql")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, string(previous))
	require.NoError(t, err)
	conn, err := db.Conn(ctx)
	require.NoError(t, err)
	oldIDs, oldPlans := captureLookupPlans(t, conn, `SELECT ARRAY(SELECT id FROM request_lookup_ids($1))`, "request-1")
	require.NoError(t, conn.Close())
	require.Equal(t, []string{"request-1"}, oldIDs)
	var scannedUsage bool
	var findScan func(lookupPlanNode)
	findScan = func(node lookupPlanNode) {
		if node.Relation == "usage_logs" && strings.Contains(node.Type, "Seq Scan") && node.Loops > 0 {
			scannedUsage = true
		}
		for _, child := range node.Plans {
			findScan(child)
		}
	}
	for _, plan := range oldPlans {
		findScan(plan)
	}
	require.True(t, scannedUsage, "升级夹具需要复现旧函数的用量表全扫")
	fix, err := migrations.FS.ReadFile("295_request_lookup_stages.sql")
	require.NoError(t, err)
	started := time.Now()
	// Apply the staged lookup migration twice to the same populated fixture so
	// replacement remains usable after repeated upgrade execution.
	for range 2 {
		_, err = db.ExecContext(ctx, string(fix))
		require.NoError(t, err)
	}
	t.Logf("查询函数迁移重复执行耗时：%s", time.Since(started))

	for _, mode := range []string{"force_custom_plan", "force_generic_plan"} {
		t.Run(mode, func(t *testing.T) {
			conn, err := db.Conn(ctx)
			require.NoError(t, err)
			defer func() { require.NoError(t, conn.Close()) }()
			_, err = conn.ExecContext(ctx, "SET plan_cache_mode="+mode)
			require.NoError(t, err)
			for _, test := range []struct {
				// search and candidate pair supplied lookup text with one required resolved
				// ID. Each case compares candidate membership before inspecting the
				// executed nested SQL plans.
				search, candidate string // search: supplied lookup text; candidate: required resolved request identifier
			}{
				{"request-1", "request-1"},
				{"supplier-1", "request-1"},
				{"client:request-1", "request-1"},
				{"error-client-1", "error-1"},
				{"system-client-1", "system-1"},
				{"missing", "missing"},
			} {
				id := test.search
				ids, plans := captureLookupPlans(t, conn, `SELECT ARRAY(SELECT id FROM request_lookup_ids($1))`, id)
				require.Contains(t, ids, test.candidate)
				if id == "request-1" {
					require.ElementsMatch(t, oldIDs, ids)
				}
				seen := map[string]bool{}
				var inspect func(lookupPlanNode)
				inspect = func(node lookupPlanNode) {
					if node.Relation != "" && node.Loops > 0 {
						seen[node.Relation] = true
						if node.Relation == "usage_logs" || node.Relation == "request_records" || node.Relation == "ops_error_logs" || node.Relation == "ops_system_logs" {
							require.NotContains(t, node.Type, "Seq Scan", "%s: %+v", id, node)
						}
					}
					for _, child := range node.Plans {
						inspect(child)
					}
				}
				for _, plan := range plans {
					inspect(plan)
				}
				if id == "request-1" {
					require.True(t, seen["usage_logs"])
					require.False(t, seen["ops_error_logs"])
					require.False(t, seen["ops_system_logs"])
				}
			}
			_, plans := captureLookupPlans(t, conn, `SELECT ARRAY(SELECT id::text FROM audit_logs
 WHERE request_id=ANY(ARRAY(SELECT id FROM request_lookup_ids($1))) ORDER BY created_at DESC LIMIT 101)`, "audit-1")
			var seenAudit bool
			var inspectAudit func(lookupPlanNode)
			inspectAudit = func(node lookupPlanNode) {
				if node.Relation == "audit_logs" && node.Loops > 0 {
					seenAudit = true
					require.NotContains(t, node.Type, "Seq Scan")
				}
				for _, child := range node.Plans {
					inspectAudit(child)
				}
			}
			for _, plan := range plans {
				inspectAudit(plan)
			}
			require.True(t, seenAudit)
		})
	}
}

// captureLookupPlans executes one parameterized array query and collects its
// nested auto_explain plans. Supply an exclusively used pq connection and
// permit replacing its notice handler and session logging settings; the helper
// leaves connection ownership with the caller.
//
// pq invokes notices synchronously, so messages are captured before query
// return without a separate worker. Deferred cleanup disables duration logging
// and clears the notice handler while other session options remain. Messages
// containing the JSON opening are decoded into root plans, and the test
// requires at least one captured plan before returning IDs and nodes.
func captureLookupPlans(t *testing.T, conn *sql.Conn, query string, id string) ([]string, []lookupPlanNode) {
	t.Helper()
	ctx := t.Context()
	var messages []string
	// Replace notice handling on this dedicated connection; pq callbacks run
	// synchronously with command processing so the collected messages precede
	// query completion.
	require.NoError(t, conn.Raw(func(raw any) error {
		driverConn, ok := raw.(driver.Conn)
		require.True(t, ok)
		pq.SetNoticeHandler(driverConn, func(notice *pq.Error) {
			messages = append(messages, notice.Message)
		})
		return nil
	}))
	// Disable duration logging and remove this helper's notice handler after
	// capture so later commands do not append into its completed message slice.
	defer func() {
		_, err := conn.ExecContext(ctx, "SET auto_explain.log_min_duration=-1")
		require.NoError(t, err)
		require.NoError(t, conn.Raw(func(raw any) error {
			driverConn, ok := raw.(driver.Conn)
			require.True(t, ok)
			pq.SetNoticeHandler(driverConn, nil)
			return nil
		}))
	}()
	_, err := conn.ExecContext(ctx, `LOAD 'auto_explain';
SET auto_explain.log_format='json';
SET auto_explain.log_level='notice';
SET auto_explain.log_nested_statements=on;
SET auto_explain.log_analyze=on;
SET auto_explain.log_min_duration=0;`)
	require.NoError(t, err)
	var ids []string
	require.NoError(t, conn.QueryRowContext(ctx, query, id).Scan(pq.Array(&ids)))
	var plans []lookupPlanNode
	for _, message := range messages {
		start := strings.Index(message, "{\n")
		// Skip notices without the plan JSON opening so setup or unrelated database
		// messages do not become malformed execution-plan input.
		if start < 0 {
			continue
		}
		var entry struct {
			Plan lookupPlanNode `json:"Plan"` // decoded explained root node
		}
		require.NoError(t, json.Unmarshal([]byte(message[start:]), &entry))
		plans = append(plans, entry.Plan)
	}
	require.NotEmpty(t, plans)
	return ids, plans
}

// TestStoreFindIncludesChildBillingAliases links creation and polling records
// to a child carrying a historical billing key. Reapplied lookup migrations
// retain one eligible child's usage while excluding unrelated alias and API-key
// collisions.
func TestStoreFindIncludesChildBillingAliases(t *testing.T) {
	db := postgrescontainer.New(t)
	ctx := t.Context()
	_, err := db.ExecContext(ctx, `
INSERT INTO users(id,email,password_hash) VALUES(101,'child-billing@requests.test','hash'),(202,'unrelated@requests.test','hash');
INSERT INTO api_keys(id,user_id,key,name) VALUES(101,101,'child-billing-key','lookup'),(202,202,'unrelated-key','lookup');
INSERT INTO providers(id,name,platform,type) VALUES(101,'child-billing','grok','apikey');
INSERT INTO usage_logs(user_id,billing_user_id,api_key_id,provider_id,request_id,billing_key,model)
VALUES(101,101,101,101,'grok-video:old-task',NULL,'model'),(202,202,202,101,'other-request','grok-video:old-task','model');`)
	require.NoError(t, err)
	store := NewStore(db)
	now := time.Now().UTC()
	require.NoError(t, store.Save(ctx, []telemetry.RequestRecord{
		{RequestID: "created", UserID: 101, StartedAt: now, UpdatedAt: now, State: "completed"},
		{RequestID: "poll", UserID: 101, StartedAt: now, UpdatedAt: now, State: "completed", Aliases: []telemetry.RequestAlias{{Kind: "related", Value: "child"}}},
		{RequestID: "child", ParentRequestID: "created", UserID: 101, APIKeyID: 101, StartedAt: now, UpdatedAt: now, State: "completed", Aliases: []telemetry.RequestAlias{{Kind: "billing", Value: "grok-video:old-task"}}},
		{RequestID: "unrelated", UserID: 202, StartedAt: now, UpdatedAt: now, State: "completed", Aliases: []telemetry.RequestAlias{{Kind: "caller", Value: "created"}}},
	}))
	// Repeat both historical and staged lookup migrations after relationships are
	// stored, checking that function replacement still resolves child billing
	// aliases afterward.
	for _, name := range []string{"294_request_lookup_child_billing.sql", "295_request_lookup_stages.sql"} {
		migration, err := migrations.FS.ReadFile(name)
		require.NoError(t, err)
		for range 2 {
			_, err = db.ExecContext(ctx, string(migration))
			require.NoError(t, err)
		}
	}
	for _, search := range []string{"created", "poll", "child"} {
		items, err := store.Find(ctx, search, 0, true)
		require.NoError(t, err)
		var total int
		for _, item := range items {
			require.NotEqual(t, "unrelated", item.RequestID)
			for range item.Usage {
				total++
				require.Equal(t, int64(101), item.APIKeyID)
			}
		}
		require.Equal(t, 1, total, search)
	}
}

// TestStoreLookupIsolationAndReplay stores shared aliases before advancing one
// snapshot and replaying stale data. It checks direct/admin ownership, retained
// aliases, nanosecond model updates, current team-owner access, unknown-user
// exclusion, and summary cleanup.
func TestStoreLookupIsolationAndReplay(t *testing.T) {
	db := postgrescontainer.New(t)
	store := NewStore(db)
	ctx := context.Background()
	start := time.Now().UTC().Truncate(time.Microsecond)
	first := telemetry.RequestRecord{RequestID: "first", UserID: 101, State: "running", StartedAt: start, UpdatedAt: start, Aliases: []telemetry.RequestAlias{{Kind: "caller", Value: "shared"}}}
	second := telemetry.RequestRecord{RequestID: "second", UserID: 202, State: "failed", StartedAt: start, UpdatedAt: start, Aliases: []telemetry.RequestAlias{{Kind: "caller", Value: "shared"}}}
	require.NoError(t, store.Save(ctx, []telemetry.RequestRecord{first, second}))
	items, err := store.Find(ctx, "shared", 101, false)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "first", items[0].RequestID)
	items, err = store.Find(ctx, "shared", 0, true)
	require.NoError(t, err)
	require.Len(t, items, 2)
	items, err = store.Find(ctx, "first", 202, false)
	require.NoError(t, err)
	require.Empty(t, items)
	completed := first
	completed.UpdatedAt = start.Add(time.Second)
	completed.State = "completed"
	completed.Aliases = []telemetry.RequestAlias{{Kind: "billing", Value: "client:first"}}
	require.NoError(t, store.Save(ctx, []telemetry.RequestRecord{completed}))
	require.NoError(t, store.Save(ctx, []telemetry.RequestRecord{first}))
	items, err = store.Find(ctx, "client:first", 101, false)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "completed", items[0].State)
	items, err = store.Find(ctx, "shared", 101, false)
	require.NoError(t, err)
	require.Len(t, items, 1)
	// Advance the revision by one nanosecond so the model update is
	// distinguishable even when PostgreSQL's timestamp column rounds both times to
	// microseconds.
	completed.UpdatedAt = completed.UpdatedAt.Add(time.Nanosecond)
	completed.Model = "latest"
	require.NoError(t, store.Save(ctx, []telemetry.RequestRecord{completed}))
	items, err = store.Find(ctx, "first", 101, false)
	require.NoError(t, err)
	require.Equal(t, "latest", items[0].Model)

	_, err = db.ExecContext(ctx, `INSERT INTO users(id,email,password_hash) VALUES
      (101,'member@requests.test','hash'),(202,'next-owner@requests.test','hash'),(303,'owner@requests.test','hash')`)
	require.NoError(t, err)
	var teamID int64
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO teams(name) VALUES('requests') RETURNING id`).Scan(&teamID))
	_, err = db.ExecContext(ctx, `INSERT INTO team_memberships(team_id,user_id,role) VALUES($1,101,'member'),($1,303,'owner')`, teamID)
	require.NoError(t, err)
	teamRecord := telemetry.RequestRecord{RequestID: "team-request", UserID: 101, TeamID: teamID, State: "failed", StartedAt: start, UpdatedAt: start}
	unknown := telemetry.RequestRecord{RequestID: "unknown", TeamID: teamID, State: "failed", StartedAt: start, UpdatedAt: start}
	require.NoError(t, store.Save(ctx, []telemetry.RequestRecord{teamRecord, unknown}))
	items, err = store.Find(ctx, "team-request", 303, false)
	require.NoError(t, err)
	require.Len(t, items, 1)
	items, err = store.Find(ctx, "unknown", 303, false)
	require.NoError(t, err)
	require.Empty(t, items)
	items, err = store.Find(ctx, "first", 303, false)
	require.NoError(t, err)
	require.Empty(t, items)
	_, err = db.ExecContext(ctx, `UPDATE team_memberships SET role='member' WHERE user_id=303;
      INSERT INTO team_memberships(team_id,user_id,role) SELECT team_id,202,'owner' FROM team_memberships WHERE user_id=303`)
	require.NoError(t, err)
	items, err = store.Find(ctx, "team-request", 303, false)
	require.NoError(t, err)
	require.Empty(t, items)
	items, err = store.Find(ctx, "team-request", 202, false)
	require.NoError(t, err)
	require.Len(t, items, 1)

	require.NoError(t, store.Cleanup(ctx, start.Add(24*time.Hour)))
	items, err = store.Find(ctx, "first", 0, true)
	require.NoError(t, err)
	require.Empty(t, items)
}

// TestStoreBatchWriterFlushesFinalSnapshot coalesces running and failed
// snapshots before stopping an inactive writer against PostgreSQL. The saved
// result retains status, start, attempt, and alias lookup, while health reports
// no pending records or failures.
func TestStoreBatchWriterFlushesFinalSnapshot(t *testing.T) {
	store := NewStore(postgrescontainer.New(t))
	service := requestlog.NewService(store, 30, nil)
	start := time.Now().UTC().Truncate(time.Microsecond)
	service.Observe(telemetry.RequestRecord{
		RequestID: "batched", UserID: 101, State: "running", StartedAt: start, UpdatedAt: start,
		Aliases: []telemetry.RequestAlias{{Kind: "caller", Value: "client-alias"}},
	})
	finished := start.Add(time.Second)
	service.Observe(telemetry.RequestRecord{
		RequestID: "batched", UserID: 101, State: "failed", UpdatedAt: finished, FinishedAt: &finished,
		Status: 502, Attempts: []telemetry.RequestAttempt{{Number: 1, ProviderID: 7, Status: 502}},
	})
	require.NoError(t, service.Stop(t.Context()))
	items, err := store.Find(t.Context(), "client-alias", 101, false)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "failed", items[0].State)
	require.Equal(t, 502, items[0].Status)
	require.Equal(t, start, items[0].StartedAt)
	require.Len(t, items[0].Attempts, 1)
	health, err := service.Health()
	require.NoError(t, err)
	require.Zero(t, health.Pending)
	require.Zero(t, health.Failures)
}

// TestStoreLookupIndexesAndAliases stores current, child, historical, and
// colliding-owner records alongside large diagnostic fixtures. It checks
// resolved candidates, visible actual IDs, administrator expansion, and exact
// Seq Scan rejection for historical log plan nodes.
func TestStoreLookupIndexesAndAliases(t *testing.T) {
	db := postgrescontainer.New(t)
	ctx := t.Context()
	_, err := db.ExecContext(ctx, `
INSERT INTO users(id,email,password_hash) VALUES (101,'lookup@requests.test','hash'),(202,'other@requests.test','hash');
INSERT INTO api_keys(id,user_id,key,name) VALUES (101,101,'lookup-key','lookup'),(202,202,'other-key','other');
INSERT INTO providers(id,name,platform,type) VALUES (101,'lookup','openai','apikey');
INSERT INTO usage_logs(user_id,billing_user_id,api_key_id,provider_id,request_id,billing_key,upstream_request_id,model)
VALUES (101,101,101,101,'new-request','client:billing','supplier','model'),
       (202,202,202,101,'other-request','client:billing','supplier','model'),
       (101,101,101,101,'client:legacy',NULL,NULL,'model');
INSERT INTO ops_error_logs(request_id,client_request_id,user_id,error_phase,error_type,status_code)
SELECT 'error-'||g, 'error-client-'||g, 101,'upstream','api_error',502 FROM generate_series(1,20000) g;
INSERT INTO ops_system_logs(request_id,client_request_id,level,message)
SELECT 'system-'||g, 'system-client-'||g,'info','lookup fixture' FROM generate_series(1,20000) g;
INSERT INTO ops_system_logs(request_id,client_request_id,level,message) VALUES ('client:legacy','legacy','info','legacy fixture');
INSERT INTO audit_logs(request_id,actor_user_id,status_code) VALUES ('new-request',101,200);
ANALYZE ops_error_logs;
ANALYZE ops_system_logs;
ANALYZE usage_logs;`)
	require.NoError(t, err)
	store := NewStore(db)
	now := time.Now().UTC()
	require.NoError(t, store.Save(ctx, []telemetry.RequestRecord{
		{RequestID: "new-request", UserID: 101, APIKeyID: 101, StartedAt: now, UpdatedAt: now, State: "completed", Aliases: []telemetry.RequestAlias{{Kind: "caller", Value: "shared"}, {Kind: "billing", Value: "client:billing"}, {Kind: "legacy", Value: "client:legacy"}, {Kind: "related", Value: "child"}}},
		{RequestID: "other-request", UserID: 202, APIKeyID: 202, StartedAt: now, UpdatedAt: now, State: "completed", Aliases: []telemetry.RequestAlias{{Kind: "caller", Value: "shared"}}},
		{RequestID: "child", ParentRequestID: "new-request", UserID: 101, StartedAt: now, UpdatedAt: now, State: "failed"},
	}))
	for _, test := range []struct {
		// search names the lookup input and candidate must occur in resolved IDs
		// before Store.Find applies ownership. visible lists the actual detail IDs
		// permitted for user one hundred one.
		search    string   // supplied external or local lookup text
		candidate string   // required resolved candidate identifier
		visible   []string // expected ownership-filtered detail identifiers
	}{
		{"new-request", "new-request", []string{"new-request", "child", "client:legacy"}},
		{"shared", "new-request", []string{"new-request", "child", "client:legacy"}},
		{"supplier", "new-request", []string{"new-request", "child", "client:legacy"}},
		{"client:billing", "new-request", []string{"new-request", "child", "client:legacy"}},
		{"legacy", "client:legacy", []string{"new-request", "child", "client:legacy"}},
		{"client:legacy", "client:legacy", []string{"new-request", "child", "client:legacy"}},
		{"generated:legacy", "client:legacy", []string{"new-request", "child", "client:legacy"}},
		{"error-1", "error-1", []string{"error-1"}},
		{"error-client-1", "error-1", []string{"error-1"}},
		{"system-1", "system-1", nil},
		{"system-client-1", "system-1", nil},
		{"missing", "missing", nil},
	} {
		t.Run(test.search, func(t *testing.T) {
			var candidates []string
			require.NoError(t, db.QueryRowContext(ctx, `SELECT ARRAY(SELECT id FROM request_lookup_ids($1))`, test.search).Scan(pq.Array(&candidates)))
			require.Contains(t, candidates, test.candidate)
			items, err := store.Find(ctx, test.search, 101, false)
			require.NoError(t, err)
			ids := make([]string, 0, len(items))
			for _, item := range items {
				ids = append(ids, item.RequestID)
			}
			require.ElementsMatch(t, test.visible, ids)
		})
	}
	items, err := store.Find(ctx, "shared", 101, false)
	require.NoError(t, err)
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.RequestID)
	}
	require.ElementsMatch(t, []string{"new-request", "child", "client:legacy"}, ids)
	items, err = store.Find(ctx, "shared", 0, true)
	require.NoError(t, err)
	require.Len(t, items, 4)

	// Capture the nested lookup plans on one connection because the outer function
	// call alone cannot identify how historical log tables are scanned.
	conn, err := db.Conn(ctx)
	require.NoError(t, err)
	defer func() { require.NoError(t, conn.Close()) }()
	for _, id := range []string{"error-1", "system-client-1", "missing"} {
		_, plans := captureLookupPlans(t, conn, `SELECT ARRAY(SELECT id FROM request_lookup_ids($1))`, id)
		seen := map[string]bool{}
		var inspect func(lookupPlanNode)
		inspect = func(node lookupPlanNode) {
			if node.Relation == "ops_system_logs" || node.Relation == "ops_error_logs" {
				require.NotEqual(t, "Seq Scan", node.Type, "%s: %+v", id, node)
				seen[node.Relation] = true
			}
			for _, child := range node.Plans {
				inspect(child)
			}
		}
		for _, plan := range plans {
			inspect(plan)
		}
		require.True(t, seen["ops_system_logs"])
		require.True(t, seen["ops_error_logs"])
	}
}

// TestStoreExactIDIgnoresExternalCollisions collides local IDs with another
// request's caller and upstream identifiers. Exact and trimmed queries retain
// their own references, shared aliases respect ownership, and parent lookup
// includes the child while child lookup stays local.
func TestStoreExactIDIgnoresExternalCollisions(t *testing.T) {
	db := postgrescontainer.New(t)
	ctx := t.Context()
	_, err := db.ExecContext(ctx, `
INSERT INTO users(id,email,password_hash) VALUES (101,'exact@requests.test','hash'),(202,'collision@requests.test','hash');
INSERT INTO api_keys(id,user_id,key,name) VALUES (101,101,'exact-key','exact'),(202,202,'collision-key','collision');
INSERT INTO providers(id,name,platform,type) VALUES (101,'exact','openai','apikey');
INSERT INTO usage_logs(user_id,billing_user_id,api_key_id,provider_id,request_id,upstream_request_id,model)
VALUES (101,101,101,101,'local-first','supplier-first','model'),(202,202,202,101,'local-second','local-first','model');
INSERT INTO ops_system_logs(request_id,client_request_id,level,message)
VALUES ('local-first','shared','info','first'),('local-second','shared','info','second');
INSERT INTO ops_error_logs(request_id,client_request_id,user_id,error_phase,error_type,status_code)
VALUES ('local-first','shared',101,'upstream','api_error',502),
       ('local-second','shared',202,'upstream','api_error',502),
       ('local-second','local-first',202,'upstream','api_error',502);
INSERT INTO audit_logs(request_id,actor_user_id,status_code)
VALUES ('local-first',101,502),('local-second',202,502);`)
	require.NoError(t, err)
	store := NewStore(db)
	now := time.Now().UTC()
	require.NoError(t, store.Save(ctx, []telemetry.RequestRecord{
		{RequestID: "local-first", UserID: 101, APIKeyID: 101, StartedAt: now, UpdatedAt: now, State: "failed", Aliases: []telemetry.RequestAlias{{Kind: "caller", Value: "shared"}}},
		{RequestID: "local-second", UserID: 202, APIKeyID: 202, StartedAt: now, UpdatedAt: now, State: "failed", Aliases: []telemetry.RequestAlias{{Kind: "caller", Value: "shared"}, {Kind: "caller", Value: "local-first"}}},
	}))
	var candidates []string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT ARRAY(SELECT id FROM request_lookup_ids('local-first'))`).Scan(pq.Array(&candidates)))
	require.Equal(t, []string{"local-first"}, candidates)
	for _, search := range []string{"local-first", " local-first "} {
		items, err := store.Find(ctx, search, 0, true)
		require.NoError(t, err)
		require.Len(t, items, 1)
		require.Equal(t, "local-first", items[0].RequestID)
		require.Len(t, items[0].Usage, 1)
		require.Len(t, items[0].Errors, 1)
		require.Len(t, items[0].AuditIDs, 1)
	}
	items, err := store.Find(ctx, "local-first", 202, false)
	require.NoError(t, err)
	require.Empty(t, items)
	items, err = store.Find(ctx, "shared", 0, true)
	require.NoError(t, err)
	require.Len(t, items, 2)
	items, err = store.Find(ctx, "shared", 101, false)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "local-first", items[0].RequestID)

	// Add a child snapshot after collision checks to distinguish parent
	// relationship expansion from exact child lookup, which should retain that
	// child's own request scope.
	require.NoError(t, store.Save(ctx, []telemetry.RequestRecord{{
		RequestID: "child", ParentRequestID: "local-first", UserID: 101, StartedAt: now, UpdatedAt: now,
		State: "completed", Aliases: []telemetry.RequestAlias{{Kind: "caller", Value: "shared"}},
	}}))
	items, err = store.Find(ctx, "local-first", 0, true)
	require.NoError(t, err)
	require.Len(t, items, 2)
	for _, item := range items {
		require.Contains(t, []string{"local-first", "child"}, item.RequestID)
	}
	items, err = store.Find(ctx, "child", 0, true)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "child", items[0].RequestID)
}

// TestStoreTimingStagesThroughQueue persists measured zero, stage durations and
// event offsets through the real request buffer and PostgreSQL. A later snapshot
// retains the complete timing map without turning legacy absence into zero.
func TestStoreTimingStagesThroughQueue(t *testing.T) {
	db := postgrescontainer.New(t)
	store := NewStore(db)
	service := requestlog.NewService(store, 30, nil)
	now := time.Now()
	record := telemetry.RequestRecord{RequestID: "timing-stage-request", StartedAt: now, UpdatedAt: now, State: "failed", FailureTimings: map[string]int64{"timing_stream": 1, "timing_admitted": 1, "request_body_read_ms": 118000, "preprocess_ms": 235, "user_slot_wait_ms": 0, "provider_slot_wait_ms": 0, "provider_slot_acquired_ms": 118235, "failed_at_ms": 125000}}
	service.Observe(record)
	require.NoError(t, service.Flush(t.Context()))
	rows, err := store.Find(t.Context(), record.RequestID, 0, true)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, record.FailureTimings, rows[0].FailureTimings)
}

// TestStoreIdentifierProvenanceSurvivesQueueAndLookup stores typed aliases through
// the real batching service. Existing alias lookup returns source and ownership
// metadata while a later sparse snapshot retains the original observations.
func TestStoreIdentifierProvenanceSurvivesQueueAndLookup(t *testing.T) {
	store := NewStore(postgrescontainer.New(t))
	service := requestlog.NewService(store, 30, nil)
	start := time.Now().UTC().Truncate(time.Microsecond)
	aliases := []telemetry.RequestAlias{
		{Kind: "upstream_request", Value: "chosen-id", Source: "X-Client-Request-Id", AttemptNumber: 1, ProviderID: 7},
		{Kind: "response", Value: "resp_result", Source: "response.id", AttemptNumber: 1, ProviderID: 7},
	}
	service.Observe(telemetry.RequestRecord{RequestID: "typed-request", UserID: 101, State: "running", StartedAt: start, UpdatedAt: start, Aliases: aliases, Attempts: []telemetry.RequestAttempt{{Number: 1, ProviderID: 7, RequestID: "chosen-id", RequestIDHeader: "X-Client-Request-Id"}}})
	service.Observe(telemetry.RequestRecord{RequestID: "typed-request", UserID: 101, State: "failed", UpdatedAt: start.Add(time.Second)})
	require.NoError(t, service.Stop(t.Context()))
	for _, search := range []string{"typed-request", "chosen-id", "resp_result"} {
		items, err := store.Find(t.Context(), search, 0, true)
		require.NoError(t, err)
		require.Len(t, items, 1)
		require.ElementsMatch(t, aliases, items[0].Aliases)
		require.Equal(t, "X-Client-Request-Id", items[0].Attempts[0].RequestIDHeader)
	}
}

// TestStoreFindProviderNames checks that administrator lookups label persisted
// attempts with current names after a rename or soft deletion. A pending-only
// attempt also resolves its name, while absent providers and ordinary callers
// receive no names.
func TestStoreFindProviderNames(t *testing.T) {
	db := postgrescontainer.New(t)
	ctx := t.Context()
	_, err := db.ExecContext(ctx, `INSERT INTO providers (id, name, platform, type, deleted_at) VALUES
 (7, 'Original name', 'openai', 'apikey', NULL),
 (8, 'Retired upstream', 'openai', 'apikey', NOW()),
 (9, 'Pending upstream', 'openai', 'apikey', NULL)`)
	require.NoError(t, err)
	store := NewStore(db)
	service := requestlog.NewService(store, 30, nil)
	now := time.Now().UTC()
	require.NoError(t, store.Save(ctx, []telemetry.RequestRecord{{
		RequestID: "stored", UserID: 101, StartedAt: now, UpdatedAt: now, State: "completed",
		Attempts: []telemetry.RequestAttempt{
			{Number: 1, ProviderID: 8, Status: 502},
			{Number: 2, ProviderID: 7, Status: 200},
		},
		Aliases: []telemetry.RequestAlias{
			{Kind: "upstream_request", Value: "unknown-provider", ProviderID: 99},
			{Kind: "upstream_request", Value: "alias-only-provider", ProviderID: 9},
		},
	}}))
	_, err = db.ExecContext(ctx, `UPDATE providers SET name = 'Renamed upstream' WHERE id = 7`)
	require.NoError(t, err)
	service.Observe(telemetry.RequestRecord{
		RequestID: "pending", UserID: 101, StartedAt: now, UpdatedAt: now, State: "running",
		Attempts: []telemetry.RequestAttempt{{Number: 1, ProviderID: 9}},
	})

	items, err := service.Find(ctx, "stored", 0, true)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, map[int64]string{7: "Renamed upstream", 8: "Retired upstream", 9: "Pending upstream"}, items[0].ProviderNames)
	require.Equal(t, int64(99), items[0].Aliases[0].ProviderID)

	items, err = service.Find(ctx, "stored", 101, false)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Empty(t, items[0].ProviderNames)

	items, err = service.Find(ctx, "pending", 0, true)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.True(t, items[0].Pending)
	require.Equal(t, map[int64]string{9: "Pending upstream"}, items[0].ProviderNames)
}
