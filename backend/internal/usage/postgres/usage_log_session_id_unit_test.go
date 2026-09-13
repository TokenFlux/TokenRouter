//go:build unit

package postgres

import (
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/usage"

	"github.com/stretchr/testify/require"
)

func newSessionIDUsageLog(sessionID *string) *usage.UsageLog {
	return &usage.UsageLog{
		UserID:       1,
		APIKeyID:     2,
		AccountID:    3,
		RequestID:    "req-session-id",
		Model:        "claude-3",
		InputTokens:  10,
		OutputTokens: 5,
		TotalCost:    1.0,
		ActualCost:   1.0,
		SessionID:    sessionID,
		CreatedAt:    time.Now().UTC(),
	}
}

// TestPrepareUsageLogInsert_SessionIDArgWiring 固定 session_id 在参数切片和类型表
// 中的位置，确保所有 INSERT 列表保持同步；新增字段均追加在末尾。
func TestPrepareUsageLogInsert_SessionIDArgWiring(t *testing.T) {
	require.Len(t, usageLogInsertArgTypes, 65, "arg-type table must include upstream request ID and compaction flag")

	sessionID := "sess-persisted-123"
	prepared := prepareUsageLogInsert(newSessionIDUsageLog(&sessionID))

	require.Len(t, prepared.args, len(usageLogInsertArgTypes),
		"prepared args must match the arg-type table length")

	// session_id 位于 created_at 之前，新增字段按顺序追加在末尾。
	sessionArg := prepared.args[len(prepared.args)-4]
	ns, ok := sessionArg.(sql.NullString)
	require.True(t, ok, "session_id arg should be a sql.NullString, got %T", sessionArg)
	require.True(t, ns.Valid)
	require.Equal(t, sessionID, ns.String)

	require.Equal(t, "text", usageLogInsertArgTypes[len(usageLogInsertArgTypes)-4],
		"session_id arg type must be text")
	require.Equal(t, "timestamptz", usageLogInsertArgTypes[len(usageLogInsertArgTypes)-3],
		"created_at arg type must remain timestamptz")
	require.Equal(t, "text", usageLogInsertArgTypes[len(usageLogInsertArgTypes)-2],
		"requested reasoning effort arg type must be text")
	require.Equal(t, "boolean", usageLogInsertArgTypes[len(usageLogInsertArgTypes)-1],
		"native compaction arg type must be boolean")
}

// TestPrepareUsageLogInsert_SessionIDNullWhenAbsent 验证缺失的会话标识会持久化为
// SQL NULL，而不是空字符串。
func TestPrepareUsageLogInsert_SessionIDNullWhenAbsent(t *testing.T) {
	prepared := prepareUsageLogInsert(newSessionIDUsageLog(nil))
	sessionArg := prepared.args[len(prepared.args)-4]
	ns, ok := sessionArg.(sql.NullString)
	require.True(t, ok, "session_id arg should be a sql.NullString, got %T", sessionArg)
	require.False(t, ns.Valid, "absent session id must be NULL, not empty string")

	empty := ""
	preparedEmpty := prepareUsageLogInsert(newSessionIDUsageLog(&empty))
	nsEmpty := preparedEmpty.args[len(preparedEmpty.args)-4].(sql.NullString)
	require.False(t, nsEmpty.Valid, "empty session id must also be NULL")
}

// TestUsageLogInsertQueries_IncludeSessionID 验证每条 INSERT 路径和 SELECT 列表
// 都包含 session_id。
func TestUsageLogInsertQueries_IncludeSessionID(t *testing.T) {
	require.Contains(t, usageLogSelectColumns, "session_id",
		"SELECT column list must include session_id")
	require.Contains(t, usageLogSelectColumns, "billing_user_id",
		"SELECT column list must include billing attribution")
	require.Contains(t, usageLogSelectColumns, "team_id",
		"SELECT column list must include team attribution")

	sessionID := "sess-in-query"
	log := newSessionIDUsageLog(&sessionID)
	prepared := prepareUsageLogInsert(log)
	key := usageLogBatchKey(log.RequestID, log.APIKeyID)

	batchQuery, batchArgs := buildUsageLogBatchInsertQuery([]string{key},
		map[string]usageLogInsertPrepared{key: prepared})
	require.Contains(t, batchQuery, "session_id")
	// 两处列引用（INSERT 列表和 SELECT ... FROM input）加上一处 CTE 定义。
	require.GreaterOrEqual(t, strings.Count(batchQuery, "session_id"), 3)
	require.Len(t, batchArgs, len(prepared.args)+1,
		"batch args include the synthetic input_index before usage-log values")

	bestEffortQuery, bestEffortArgs := buildUsageLogBestEffortInsertQuery([]usageLogInsertPrepared{prepared})
	require.Contains(t, bestEffortQuery, "session_id")
	require.Len(t, bestEffortArgs, len(prepared.args))
}

// TestPrepareUsageLogInsert_RequestedReasoningEffortArgWiring 验证新增字段位于参数末尾。
func TestPrepareUsageLogInsert_RequestedReasoningEffortArgWiring(t *testing.T) {
	requested := "max"
	prepared := prepareUsageLogInsert(&usage.UsageLog{
		UserID: 1, APIKeyID: 2, AccountID: 3, RequestID: "req-effort", Model: "gpt-5",
		RequestedReasoningEffort: &requested,
	})
	value, ok := prepared.args[len(prepared.args)-2].(sql.NullString)
	require.True(t, ok)
	require.True(t, value.Valid)
	require.Equal(t, requested, value.String)
}
