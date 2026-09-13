// 本文件维护 postgres 的所属能力；兼容入口复用唯一实现。
package postgres

import (
	context "context"
	json "encoding/json"
	errors "errors"
	dbent "github.com/TokenFlux/TokenRouter/ent"
	dbaccount "github.com/TokenFlux/TokenRouter/ent/account"
	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
)

func (r *AccountStore) Update(ctx context.Context, account *acctcore.Record) error {
	return r.updateAccount(ctx, account, nil)
}

func (r *AccountStore) updateAccount(ctx context.Context, account *acctcore.Record, change *acctcore.ConfigurationChange) error {
	if account == nil {
		return nil
	}

	baseCtx := ctx
	contextTx := dbent.TxFromContext(ctx)
	client := r.client
	var tx *dbent.Tx
	if contextTx != nil {
		client = contextTx.Client()
	} else {
		var err error
		tx, err = r.client.Tx(ctx)
		if err != nil && !errors.Is(err, dbent.ErrTxStarted) {
			return err
		}
		if tx != nil {
			defer func() { _ = tx.Rollback() }()
			ctx = dbent.NewTxContext(ctx, tx)
			client = tx.Client()
		}
	}

	updated, err := r.updateLockedAccount(ctx, client, account, change)
	if err != nil {
		return translatePersistenceError(err, acctcore.ErrAccountNotFound, nil)
	}
	if err := r.publish(ctx, client, account.ID, account.GroupIDs); err != nil {
		return err
	}
	if tx != nil {
		if err := tx.Commit(); err != nil {
			return err
		}
	}

	account.UpdatedAt = updated.UpdatedAt
	// 普通账号编辑（如 model_mapping / credentials）也需要立即刷新单账号快照，
	// 否则网关在 outbox worker 延迟或异常时仍可能读到旧配置。
	if contextTx == nil {
		r.afterChange(baseCtx, account.ID)
	}
	return nil
}

func (r *AccountStore) updateLockedAccount(ctx context.Context, client *dbent.Client, account *acctcore.Record, change *acctcore.ConfigurationChange) (*dbent.Account, error) {
	if change != nil {
		current, err := r.lockConfigurationRecord(ctx, client, account.ID)
		if err != nil {
			return nil, err
		}
		merged, err := acctcore.ApplyConfigurationChange(current, account, *change)
		if err != nil {
			return nil, err
		}
		*account = *merged
	}
	extra, err := r.LockAndMergeAccountManagedExtra(ctx, client, account)
	if err != nil {
		return nil, err
	}
	account.Extra = extra

	schedulable := account.Schedulable
	if account.Status == acctcore.StatusError {
		// 错误状态账号必须退出调度池，避免后台更新把失效账号重新放回可用列表。
		schedulable = false
	}

	builder := client.Account.UpdateOneID(account.ID).
		SetName(account.Name).
		SetNillableNotes(account.Notes).
		SetPlatform(account.Platform).
		SetType(account.Type).
		SetCredentials(normalizeJSONMap(account.Credentials)).
		SetExtra(extra).
		SetConcurrency(account.Concurrency).
		SetPriority(account.Priority).
		SetStatus(account.Status).
		SetErrorMessage(account.ErrorMessage).
		SetSchedulable(schedulable).
		SetAutoPauseOnExpired(account.AutoPauseOnExpired)

	if account.RateMultiplier != nil {
		builder.SetRateMultiplier(*account.RateMultiplier)
	}
	if account.LoadFactor != nil {
		builder.SetLoadFactor(*account.LoadFactor)
	} else {
		builder.ClearLoadFactor()
	}

	if account.ProxyID != nil {
		builder.SetProxyID(*account.ProxyID)
	} else {
		builder.ClearProxyID()
	}

	// 使用时间、限流/过载和会话窗口只由各自的运行写入口维护。
	// 普通配置更新不能写回加载时的旧快照，也不能借 nil 清除并发变化。
	if account.ExpiresAt != nil {
		builder.SetExpiresAt(*account.ExpiresAt)
	} else {
		builder.ClearExpiresAt()
	}

	if account.Notes == nil {
		builder.ClearNotes()
	}

	builder.SetQuotaDimension(dbaccount.QuotaDimension(account.QuotaDimensionOrDefault()))
	builder.SetNillableParentAccountID(account.ParentAccountID)

	return builder.Save(ctx)
}

func (r *AccountStore) LockAndMergeAccountManagedExtra(ctx context.Context, client *dbent.Client, account *acctcore.Record) (map[string]any, error) {
	credentials, err := json.Marshal(normalizeJSONMap(account.Credentials))
	if err != nil {
		return nil, err
	}
	var proxyID any
	if account.ProxyID != nil {
		proxyID = *account.ProxyID
	}
	rows, err := client.QueryContext(ctx, `
		SELECT
			COALESCE(
				platform IN ('openai', 'anthropic')
				AND $2 IN ('openai', 'anthropic')
				AND type = 'apikey'
				AND $3 = 'apikey'
				AND credentials -> 'api_key' IS NOT DISTINCT FROM $4::jsonb -> 'api_key'
				AND `+OllamaCloudBaseURLMatchesSQL("credentials ->> 'base_url'")+`
				AND `+OllamaCloudBaseURLMatchesSQL("$4::jsonb ->> 'base_url'")+`,
				false
			),
			proxy_id IS NOT DISTINCT FROM $5,
			extra -> 'ollama_cloud_usage_session',
			extra -> 'ollama_cloud_usage_auto_refresh',
			extra -> 'ollama_cloud_usage_snapshot'
		FROM accounts
		WHERE id = $1 AND deleted_at IS NULL
		FOR NO KEY UPDATE
	`, account.ID, account.Platform, account.Type, string(credentials), proxyID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, err
		}
		return nil, acctcore.ErrAccountNotFound
	}

	var (
		ollamaGroupIdentityUnchanged bool
		ollamaProxyIdentityUnchanged bool
		currentOllamaSession         []byte
		currentOllamaAutoRefresh     []byte
		currentOllamaSnapshot        []byte
	)
	if err := rows.Scan(
		&ollamaGroupIdentityUnchanged,
		&ollamaProxyIdentityUnchanged,
		&currentOllamaSession,
		&currentOllamaAutoRefresh,
		&currentOllamaSnapshot,
	); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	extra := acctcore.CloneValues(normalizeJSONMap(account.Extra))
	acctcore.DiscardDeprecatedExtra(extra)
	for _, key := range []string{
		"ollama_cloud_usage_session",
		"ollama_cloud_usage_auto_refresh",
		"ollama_cloud_usage_snapshot",
	} {
		delete(extra, key)
	}
	if r.options.OllamaIdentity(account) && ollamaGroupIdentityUnchanged {
		for key, raw := range map[string][]byte{
			"ollama_cloud_usage_session":      currentOllamaSession,
			"ollama_cloud_usage_auto_refresh": currentOllamaAutoRefresh,
		} {
			if value, ok, err := DecodeAccountExtraJSON(raw); err != nil {
				return nil, err
			} else if ok {
				extra[key] = value
			}
		}
		if ollamaProxyIdentityUnchanged {
			if snapshot, ok, err := DecodeAccountExtraJSON(currentOllamaSnapshot); err != nil {
				return nil, err
			} else if ok {
				extra["ollama_cloud_usage_snapshot"] = snapshot
			}
		}
	}
	return extra, nil
}

func DecodeAccountExtraJSON(raw []byte) (any, bool, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, false, nil
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil, false, err
	}
	return value, true, nil
}

func (r *AccountStore) UpdateCredentials(ctx context.Context, id int64, credentials map[string]any) error {
	payload, err := json.Marshal(normalizeJSONMap(credentials))
	if err != nil {
		return err
	}
	baseCtx := ctx
	contextTx := dbent.TxFromContext(ctx)
	client := r.client
	var tx *dbent.Tx
	if contextTx != nil {
		client = contextTx.Client()
	} else if r.client != nil {
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
	result, err := client.ExecContext(ctx, `
		UPDATE accounts
		SET
			credentials = $1::jsonb,
			extra = CASE
				-- 凭证整体未变化时不清理 Ollama 状态；废弃账号扩展键始终从写入结果剔除。
				WHEN platform IN ('openai', 'anthropic')
					AND type = 'apikey'
					AND credentials IS DISTINCT FROM $1::jsonb
					AND (
						credentials -> 'api_key' IS DISTINCT FROM $1::jsonb -> 'api_key'
						OR NOT (
							`+OllamaCloudBaseURLMatchesSQL("credentials ->> 'base_url'")+`
							AND `+OllamaCloudBaseURLMatchesSQL("$1::jsonb ->> 'base_url'")+`
						)
					)
				THEN (COALESCE(extra, '{}'::jsonb)
						- 'upstream_billing_probe'
						- 'upstream_billing_probe_enabled'
						- 'openai_long_context_billing_enabled')
					- 'ollama_cloud_usage_session'
					- 'ollama_cloud_usage_auto_refresh'
					- 'ollama_cloud_usage_snapshot'
				ELSE CASE
					WHEN platform IN ('kimi', 'zhipu', 'deepseek')
						AND type = 'apikey'
						AND credentials IS DISTINCT FROM $1::jsonb
					THEN (COALESCE(extra, '{}'::jsonb)
							- 'upstream_billing_probe'
							- 'upstream_billing_probe_enabled'
							- 'openai_long_context_billing_enabled')
						- 'cn_usage_monitor_snapshot'
					ELSE COALESCE(extra, '{}'::jsonb)
						- 'upstream_billing_probe'
						- 'upstream_billing_probe_enabled'
						- 'openai_long_context_billing_enabled'
				END
			END,
			updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL
	`, string(payload), id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return acctcore.ErrAccountNotFound
	}
	if err := r.publish(ctx, client, id, nil); err != nil {
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
	return nil
}
