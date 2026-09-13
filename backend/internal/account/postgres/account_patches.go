// 本文件维护 postgres 的所属能力；兼容入口复用唯一实现。
package postgres

import (
	context "context"
	json "encoding/json"
	errors "errors"
	dbent "github.com/TokenFlux/TokenRouter/ent"
	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
	pq "github.com/lib/pq"
	strconv "strconv"
	strings "strings"
	time "time"
)

func (r *AccountStore) UpdateExtra(ctx context.Context, id int64, updates map[string]any) error {
	return r.updateExtra(ctx, id, updates, nil)
}

// updateExtra 复用原 JSONB 合并及 outbox 原子范围；观测写入只附加行条件。
func (r *AccountStore) updateExtra(ctx context.Context, id int64, updates map[string]any, version *acctcore.UsageObservationVersion) error {
	acctcore.DiscardDeprecatedExtra(updates)
	updates = stripCodexFingerprintSeedFromExtraUpdate(updates)
	if len(updates) == 0 {
		return nil
	}

	// 使用 JSONB 合并操作实现原子更新，避免读-改-写的并发丢失更新问题
	payload, err := json.Marshal(updates)
	if err != nil {
		return err
	}

	durableSchedulerChange := ShouldEnqueueSchedulerOutboxForExtraUpdates(updates)
	baseCtx := ctx
	contextTx := dbent.TxFromContext(ctx)
	client := clientFromContext(ctx, r.client)
	var tx *dbent.Tx
	if durableSchedulerChange && contextTx == nil {
		var txErr error
		tx, txErr = r.client.Tx(ctx)
		if txErr != nil && !errors.Is(txErr, dbent.ErrTxStarted) {
			return txErr
		}
		if tx != nil {
			defer func() { _ = tx.Rollback() }()
			ctx = dbent.NewTxContext(ctx, tx)
			client = tx.Client()
		}
	}
	extraExpression := "(COALESCE(extra, '{}'::jsonb) - 'upstream_billing_probe' - 'upstream_billing_probe_enabled' - 'openai_long_context_billing_enabled') || $1::jsonb"
	if cnUsageMonitorIdentityExtraPatch(updates) {
		extraExpression = "(" + extraExpression + ") - '" + acctcore.CNUsageMonitorSnapshotExtraKey + "'"
	}
	if acctcore.ShouldEnsureCodexFingerprintSeedForExtraUpdates(updates) {
		extraExpression = ensureCodexFingerprintSeedSQL(extraExpression)
	}
	where := "id = $2 AND deleted_at IS NULL"
	args := []any{string(payload), id}
	if version != nil {
		condition, values, err := usageObservationPredicate(*version, 3, false)
		if err != nil {
			return err
		}
		where += " AND " + condition
		args = append(args, values...)
	}
	result, err := client.ExecContext(ctx, "UPDATE accounts SET extra = "+extraExpression+", updated_at = NOW() WHERE "+where, args...)

	if err != nil {
		return err
	}

	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		if version != nil {
			return acctcore.ErrUsageObservationChanged
		}
		return acctcore.ErrAccountNotFound
	}
	if durableSchedulerChange {
		if err := r.enqueue(ctx, client, AccountChanged, &id, nil, nil); err != nil {
			return err
		}
		if tx != nil {
			if err := tx.Commit(); err != nil {
				return err
			}
		}
		if contextTx == nil {
			r.afterChange(baseCtx, id)
		}
	} else {
		// 观测型 extra 字段不需要触发 bucket 重建，但仍同步单账号快照，
		// 让 sticky session / GetAccount 命中缓存时也能读到最新数据，
		// 同时避免缓存局部 patch 覆盖掉并发写入的其它账号字段。
		if dbent.TxFromContext(ctx) == nil {
			r.afterChange(ctx, id)
		}
	}
	return nil
}

// UpdateCNUsageMonitorSnapshotCAS 仅在账号 updated_at 仍与探测身份快照一致时写入。
// 快照会影响调度阈值，因此写入与 scheduler outbox 事件处于同一事务。
func (r *AccountStore) UpdateCNUsageMonitorSnapshotCAS(
	ctx context.Context,
	accountID int64,
	expectedUpdatedAt time.Time,
	snapshot *acctcore.CNUsageMonitorSnapshot,
	clearExtraKey string,
) (bool, error) {
	if snapshot == nil {
		return false, nil
	}
	payload, err := json.Marshal(snapshot)
	if err != nil {
		return false, err
	}

	baseCtx := ctx
	contextTx := dbent.TxFromContext(ctx)
	client := clientFromContext(ctx, r.client)
	var tx *dbent.Tx
	if contextTx == nil {
		tx, err = r.client.Tx(ctx)
		if err != nil && !errors.Is(err, dbent.ErrTxStarted) {
			return false, err
		}
		if tx != nil {
			defer func() { _ = tx.Rollback() }()
			ctx = dbent.NewTxContext(ctx, tx)
			client = tx.Client()
		}
	}
	result, err := client.ExecContext(ctx, `
		WITH updated AS (
			UPDATE accounts
			SET extra = CASE
					WHEN $1 = '' THEN COALESCE(extra, '{}'::jsonb) || jsonb_build_object($2, $3::jsonb)
					ELSE (COALESCE(extra, '{}'::jsonb) || jsonb_build_object($2, $3::jsonb)) - $1
				END,
				updated_at = NOW()
			WHERE id = $4
				AND deleted_at IS NULL
				AND updated_at = $5
			RETURNING id
		)
		INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload)
		SELECT $6, updated.id, NULL, NULL FROM updated
	`, strings.TrimSpace(clearExtraKey), acctcore.CNUsageMonitorSnapshotExtraKey, string(payload),
		accountID, expectedUpdatedAt, r.eventName(AccountChanged))
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected == 0 {
		return false, nil
	}
	if tx != nil {
		if err := tx.Commit(); err != nil {
			return false, err
		}
	}
	if contextTx == nil {
		r.afterChange(baseCtx, accountID)
	}
	return true, nil
}

func (r *AccountStore) BulkUpdate(ctx context.Context, ids []int64, updates acctcore.AccountBulkUpdate) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	acctcore.DiscardDeprecatedExtra(updates.Extra)
	updates.Extra = stripCodexFingerprintSeedFromExtraUpdate(updates.Extra)

	setClauses := make([]string, 0, 8)
	args := make([]any, 0, 8)

	idx := 1
	ollamaProxyIdentityChanged := ""
	if updates.Name != nil {
		setClauses = append(setClauses, "name = $"+strconv.Itoa(idx))
		args = append(args, *updates.Name)
		idx++
	}
	if updates.ProxyID != nil {
		// 0 表示清除代理（前端发送 0 而不是 null 来表达清除意图）
		if *updates.ProxyID == 0 {
			setClauses = append(setClauses, "proxy_id = NULL")
			ollamaProxyIdentityChanged = "proxy_id IS NOT NULL"
		} else {
			proxyPlaceholder := "$" + strconv.Itoa(idx)
			setClauses = append(setClauses, "proxy_id = "+proxyPlaceholder)
			ollamaProxyIdentityChanged = "proxy_id IS DISTINCT FROM " + proxyPlaceholder
			args = append(args, *updates.ProxyID)
			idx++
		}
	}
	if updates.Concurrency != nil {
		setClauses = append(setClauses, "concurrency = $"+strconv.Itoa(idx))
		args = append(args, *updates.Concurrency)
		idx++
	}
	if updates.Priority != nil {
		setClauses = append(setClauses, "priority = $"+strconv.Itoa(idx))
		args = append(args, *updates.Priority)
		idx++
	}
	if updates.RateMultiplier != nil {
		setClauses = append(setClauses, "rate_multiplier = $"+strconv.Itoa(idx))
		args = append(args, *updates.RateMultiplier)
		idx++
	}
	if updates.LoadFactor != nil {
		if *updates.LoadFactor <= 0 {
			setClauses = append(setClauses, "load_factor = NULL")
		} else {
			setClauses = append(setClauses, "load_factor = $"+strconv.Itoa(idx))
			args = append(args, *updates.LoadFactor)
			idx++
		}
	}
	if updates.Status != nil {
		setClauses = append(setClauses, "status = $"+strconv.Itoa(idx))
		args = append(args, *updates.Status)
		idx++
	}
	if updates.Schedulable != nil {
		setClauses = append(setClauses, "schedulable = $"+strconv.Itoa(idx))
		args = append(args, *updates.Schedulable)
		idx++
	}
	// JSONB 需要合并而非覆盖，使用 raw SQL 保持旧行为。
	credentialPlaceholder := ""
	if len(updates.Credentials) > 0 {
		payload, err := json.Marshal(updates.Credentials)
		if err != nil {
			return 0, err
		}
		credentialPlaceholder = "$" + strconv.Itoa(idx)
		credentialExpression := "COALESCE(credentials, '{}'::jsonb) || " + credentialPlaceholder + "::jsonb"
		if len(updates.ProtocolUpdates) > 0 {
			encoded, encodeErr := json.Marshal(updates.ProtocolUpdates)
			if encodeErr != nil {
				return 0, encodeErr
			}
			credentialExpression = "(" + credentialExpression + " || COALESCE($" + strconv.Itoa(idx+1) + "::jsonb -> id::text, '{}'::jsonb)) - 'api_protocol' - 'openai_workload_capabilities' - 'openai_capabilities'"
			args = append(args, payload, encoded)
			idx += 2
		} else {
			args = append(args, payload)
			idx++
		}
		setClauses = append(setClauses, "credentials = "+credentialExpression)
	}

	if len(updates.Credentials) == 0 && len(updates.ProtocolUpdates) > 0 {
		payload, err := json.Marshal(updates.ProtocolUpdates)
		if err != nil {
			return 0, err
		}
		setClauses = append(setClauses, "credentials = (COALESCE(credentials, '{}'::jsonb) || COALESCE($"+strconv.Itoa(idx)+"::jsonb -> id::text, '{}'::jsonb)) - 'api_protocol' - 'openai_workload_capabilities' - 'openai_capabilities'")
		args = append(args, payload)
		idx++
	}

	ollamaGroupIdentityChanges := make([]string, 0, 2)
	if _, ok := updates.Credentials["api_key"]; ok {
		ollamaGroupIdentityChanges = append(ollamaGroupIdentityChanges, "credentials -> 'api_key' IS DISTINCT FROM "+credentialPlaceholder+"::jsonb -> 'api_key'")
	}
	if _, ok := updates.Credentials["base_url"]; ok {
		ollamaGroupIdentityChanges = append(ollamaGroupIdentityChanges,
			"NOT ("+OllamaCloudBaseURLMatchesSQL("credentials ->> 'base_url'")+
				" AND "+OllamaCloudBaseURLMatchesSQL(credentialPlaceholder+"::jsonb ->> 'base_url'")+")")
	}

	cnUsageIdentityChanged := len(updates.Credentials) > 0 || updates.ProxyID != nil || cnUsageMonitorIdentityExtraPatch(updates.Extra)
	if len(updates.Extra) > 0 || len(ollamaGroupIdentityChanges) > 0 || ollamaProxyIdentityChanged != "" || cnUsageIdentityChanged || updates.EnsureCodexFingerprintSeed {
		extraExpression := "COALESCE(extra, '{}'::jsonb) - 'upstream_billing_probe' - 'upstream_billing_probe_enabled' - 'openai_long_context_billing_enabled'"
		if len(updates.Extra) > 0 {
			payload, err := json.Marshal(updates.Extra)
			if err != nil {
				return 0, err
			}
			extraExpression += " || $" + strconv.Itoa(idx) + "::jsonb"
			args = append(args, payload)
			idx++
			if ollamaCloudUsageSnapshotClearRequested(updates.Extra) {
				extraExpression = "(" + extraExpression + ") - 'ollama_cloud_usage_snapshot'"
			}
		}
		eligibleAccount := "platform IN ('openai', 'anthropic') AND type = 'apikey'"
		groupIdentityChanged := ""
		if len(ollamaGroupIdentityChanges) > 0 {
			groupIdentityChanged = "(" + eligibleAccount + " AND (" + strings.Join(ollamaGroupIdentityChanges, " OR ") + "))"
		}
		snapshotIdentityChanged := groupIdentityChanged
		if ollamaProxyIdentityChanged != "" {
			proxyChanged := "(" + eligibleAccount + " AND " + ollamaProxyIdentityChanged + ")"
			if snapshotIdentityChanged == "" {
				snapshotIdentityChanged = proxyChanged
			} else {
				snapshotIdentityChanged = "(" + snapshotIdentityChanged + " OR " + proxyChanged + ")"
			}
		}
		if groupIdentityChanged != "" {
			extraExpression = "CASE" +
				" WHEN " + groupIdentityChanged + " THEN (" + extraExpression + ") - 'ollama_cloud_usage_session' - 'ollama_cloud_usage_auto_refresh' - 'ollama_cloud_usage_snapshot'" +
				" WHEN " + snapshotIdentityChanged + " THEN (" + extraExpression + ") - 'ollama_cloud_usage_snapshot'" +
				" ELSE " + extraExpression + " END"
		} else if snapshotIdentityChanged != "" {
			extraExpression = "CASE WHEN " + snapshotIdentityChanged + " THEN (" + extraExpression + ") - 'ollama_cloud_usage_snapshot' ELSE " + extraExpression + " END"
		}
		if cnUsageIdentityChanged {
			extraExpression = "CASE WHEN platform IN ('kimi', 'zhipu', 'deepseek') AND type = 'apikey'" +
				" THEN (" + extraExpression + ") - '" + acctcore.CNUsageMonitorSnapshotExtraKey + "' ELSE " + extraExpression + " END"
		}
		if updates.EnsureCodexFingerprintSeed {
			extraExpression = ensureCodexFingerprintSeedSQL(extraExpression)
		}
		if len(updates.ProtocolUpdates) > 0 {
			extraExpression = "(" + extraExpression + ") - 'openai_text_route_mode' - 'openai_responses_mode'"
		}
		setClauses = append(setClauses, "extra = "+extraExpression)
	}

	if len(setClauses) == 0 {
		return 0, nil
	}

	setClauses = append(setClauses, "updated_at = NOW()")

	whereClause := " WHERE id = ANY($" + strconv.Itoa(idx) + ") AND deleted_at IS NULL"
	args = append(args, pq.Array(ids))
	query := "UPDATE accounts SET " + strings.Join(setClauses, ", ") + whereClause

	baseCtx := ctx
	contextTx := dbent.TxFromContext(ctx)
	exec := r.sql
	var tx *dbent.Tx
	if contextTx != nil {
		exec = contextTx.Client()
	} else if r.client != nil {
		var txErr error
		tx, txErr = r.client.Tx(ctx)
		if txErr != nil && !errors.Is(txErr, dbent.ErrTxStarted) {
			return 0, txErr
		}
		if tx != nil {
			defer func() { _ = tx.Rollback() }()
			ctx = dbent.NewTxContext(ctx, tx)
			exec = tx.Client()
		}
	}

	result, err := exec.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if rows > 0 {
		payload := map[string]any{"account_ids": ids}
		if err := r.enqueue(ctx, exec, AccountBulkChanged, nil, nil, payload); err != nil {
			return 0, err
		}
	}
	if tx != nil {
		if err := tx.Commit(); err != nil {
			return 0, err
		}
	}
	if rows > 0 && contextTx == nil {
		shouldSync := false
		if updates.Status != nil && (*updates.Status == acctcore.StatusError || *updates.Status == acctcore.StatusDisabled) {
			shouldSync = true
		}
		if updates.Schedulable != nil && !*updates.Schedulable {
			shouldSync = true
		}
		if shouldSync {
			r.afterChanges(baseCtx, ids)
		}
	}
	return rows, nil
}

var schedulerNeutralExtraKeyPrefixes = []string{
	"codex_primary_",
	"codex_secondary_",
	"codex_5h_",
	"codex_7d_",
	"codex_reset_credit_",
	"passive_usage_",
	"ollama_cloud_usage",
	"cn_usage_monitor",
}

var schedulerNeutralExtraKeys = map[string]struct{}{
	"codex_usage_updated_at":                {},
	"grok_billing_snapshot":                 {},
	"qoder_quota_snapshot":                  {},
	"qoder_quota_updated_at":                {},
	"session_window_utilization":            {},
	acctcore.CNUsageMonitorSnapshotExtraKey: {},
}

const codexFingerprintSeedCanonicalPattern = "^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$"

const codexFingerprintNilSeed = "00000000-0000-0000-0000-000000000000"

func codexFingerprintSeedValidSQL(extraExpr string) string {
	value := "(" + extraExpr + " ->> 'codex_fingerprint_seed')"
	return "(" + value + " ~ '" + codexFingerprintSeedCanonicalPattern + "' AND " + value + " <> '" + codexFingerprintNilSeed + "')"
}

// ensureCodexFingerprintSeedSQL 在同一条 SQL 中保留合法 seed，避免并发更新产生身份漂移。
func ensureCodexFingerprintSeedSQL(extraExpr string) string {
	return "CASE WHEN platform = 'openai' AND type = 'oauth' THEN " +
		"jsonb_set(" + extraExpr + ", '{codex_fingerprint_seed}', " +
		"CASE WHEN " + codexFingerprintSeedValidSQL("extra") +
		" THEN to_jsonb(extra ->> 'codex_fingerprint_seed') ELSE to_jsonb(gen_random_uuid()::text) END, true) " +
		"ELSE " + extraExpr + " END"
}

func stripCodexFingerprintSeedFromExtraUpdate(extra map[string]any) map[string]any {
	if extra == nil {
		return nil
	}
	if _, exists := extra["codex_fingerprint_seed"]; !exists {
		return extra
	}
	stripped := make(map[string]any, len(extra)-1)
	for key, value := range extra {
		if key != "codex_fingerprint_seed" {
			stripped[key] = value
		}
	}
	return stripped
}

func ShouldEnqueueSchedulerOutboxForExtraUpdates(updates map[string]any) bool {
	if len(updates) == 0 {
		return false
	}
	for key := range updates {
		if IsSchedulerNeutralExtraKey(key) {
			continue
		}
		return true
	}
	return false
}

func IsSchedulerNeutralExtraKey(key string) bool {
	key = strings.TrimSpace(key)
	if key == "" {
		return false
	}
	if _, ok := schedulerNeutralExtraKeys[key]; ok {
		return true
	}
	for _, prefix := range schedulerNeutralExtraKeyPrefixes {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

func cnUsageMonitorIdentityExtraPatch(updates map[string]any) bool {
	for _, key := range []string{
		acctcore.UpstreamUsageQueryExtraKey,
		"enable_tls_fingerprint",
		"tls_fingerprint_profile_id",
		"tls_fingerprint_router_id",
	} {
		if _, ok := updates[key]; ok {
			return true
		}
	}
	return false
}

func ollamaCloudUsageSnapshotClearRequested(extra map[string]any) bool {
	value, ok := extra[acctcore.OllamaCloudUsageSnapshotExtraKey]
	return ok && value == nil
}
