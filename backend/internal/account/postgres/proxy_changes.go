// 本文件维护 postgres 的所属能力；兼容入口复用唯一实现。
package postgres

import (
	"context"
	"database/sql"

	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
)

// ProxyChanges 只参与调用方连接；不提交、发布事件或触发缓存失效。
type ProxyChanges struct{ exec postgresinfra.Executor }

func ProxyChangesInTx(exec postgresinfra.Executor) ProxyChanges { return ProxyChanges{exec: exec} }
func (p ProxyChanges) InvalidateSnapshots(ctx context.Context, proxyID int64) ([]int64, error) {
	rows, err := p.exec.QueryContext(ctx, `
		UPDATE accounts
		SET extra = COALESCE(extra, '{}'::jsonb) - 'ollama_cloud_usage_snapshot',
			updated_at = NOW()
		WHERE proxy_id = $1
			AND type = 'apikey'
			AND platform IN ('openai', 'anthropic')
			AND extra ? 'ollama_cloud_usage_snapshot'
			AND extra -> 'ollama_cloud_usage_snapshot' <> 'null'::jsonb
			AND deleted_at IS NULL
		RETURNING id
	`, proxyID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	accountIDs := make([]int64, 0)
	for rows.Next() {
		var accountID int64
		if err := rows.Scan(&accountID); err != nil {
			return nil, err
		}
		accountIDs = append(accountIDs, accountID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return accountIDs, nil
}

func (p ProxyChanges) Reassign(ctx context.Context, proxyID int64, target *int64) ([]int64, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if target == nil {
		rows, err = p.exec.QueryContext(ctx, `
			UPDATE accounts SET proxy_id=NULL, proxy_fallback_origin_id=$1,
				extra=(CASE
					WHEN platform IN ('openai', 'anthropic') AND type='apikey'
					THEN COALESCE(extra, '{}'::jsonb) - 'ollama_cloud_usage_snapshot'
					ELSE COALESCE(extra, '{}'::jsonb)
				END) - 'upstream_billing_probe' - 'upstream_billing_probe_enabled',
				updated_at=NOW()
			WHERE proxy_id=$1 AND proxy_fallback_origin_id IS NULL AND deleted_at IS NULL
			RETURNING id`, proxyID)
	} else {
		rows, err = p.exec.QueryContext(ctx, `
			UPDATE accounts SET proxy_id=$2, proxy_fallback_origin_id=$1,
				extra=(CASE
					WHEN platform IN ('openai', 'anthropic') AND type='apikey'
					THEN COALESCE(extra, '{}'::jsonb) - 'ollama_cloud_usage_snapshot'
					ELSE COALESCE(extra, '{}'::jsonb)
				END) - 'upstream_billing_probe' - 'upstream_billing_probe_enabled',
				updated_at=NOW()
			WHERE proxy_id=$1 AND proxy_fallback_origin_id IS NULL AND deleted_at IS NULL
			RETURNING id`, proxyID, *target)
	}
	if err != nil {
		return nil, err
	}

	// 必须在提交子事务前读完并关闭 RETURNING 结果集，否则连接仍可能处于 busy 状态。
	accountIDs := make([]int64, 0)
	for rows.Next() {
		var accountID int64
		if err := rows.Scan(&accountID); err != nil {
			_ = rows.Close()
			return nil, err
		}
		accountIDs = append(accountIDs, accountID)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return accountIDs, nil
}
