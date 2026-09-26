-- 将共享价格与分组路由策略分开；迁移期间必须停止全部旧实例。
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '10min';

ALTER TABLE groups ADD COLUMN IF NOT EXISTS routing_policy JSONB NOT NULL DEFAULT '{}';

-- 归档包含未关联分组的配置；重复执行不会覆盖第一次升级时的原始数据。
CREATE TABLE IF NOT EXISTS pricing_policy_migration_archive (
    original_config_id BIGINT PRIMARY KEY,
    policy JSONB NOT NULL,
    group_ids JSONB NOT NULL,
    archived_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

DO $$
DECLARE
    item RECORD;
    old_name TEXT;
    new_name TEXT;
BEGIN
    IF to_regclass('public.channels') IS NOT NULL THEN
        INSERT INTO pricing_policy_migration_archive (original_config_id, policy, group_ids)
        SELECT c.id, jsonb_build_object(
            'enabled', c.status = 'active',
            'model_mapping', COALESCE(c.model_mapping, '{}'),
            'restrict_models', c.restrict_models,
            'restriction_model_source', CASE WHEN c.billing_model_source = 'channel_mapped' THEN 'group_mapped' ELSE c.billing_model_source END,
            'allowed_models', COALESCE((
                SELECT jsonb_object_agg(p.platform, p.models) FROM (
                    SELECT mp.platform, jsonb_agg(DISTINCT m.model) AS models
                    FROM channel_model_pricing mp CROSS JOIN LATERAL jsonb_array_elements_text(
                        CASE WHEN jsonb_typeof(mp.models) = 'array' THEN mp.models ELSE '[]'::jsonb END
                    ) m(model)
                    WHERE mp.channel_id = c.id AND m.model IS NOT NULL GROUP BY mp.platform
                ) p
            ), '{}'),
            'features', COALESCE(c.features, ''),
            'features_config', COALESCE(c.features_config, '{}')
        ), COALESCE((SELECT jsonb_agg(cg.group_id ORDER BY cg.group_id) FROM channel_groups cg WHERE cg.channel_id = c.id), '[]')
        FROM channels c ON CONFLICT (original_config_id) DO NOTHING;

        UPDATE groups g SET routing_policy = a.policy
        FROM channel_groups cg JOIN pricing_policy_migration_archive a ON a.original_config_id = cg.channel_id
        WHERE g.id = cg.group_id;

        ALTER TABLE channels RENAME TO pricing_configs;
        FOR item IN SELECT tablename FROM pg_tables WHERE schemaname = 'public' AND tablename LIKE 'channel\_%' ESCAPE '\' LOOP
            EXECUTE format('ALTER TABLE %I RENAME TO %I', item.tablename, replace(item.tablename, 'channel_', 'pricing_config_'));
        END LOOP;
    END IF;

    -- PostgreSQL 重命名表时保留依赖对象，继续更新列、约束、序列和索引的名称。
    FOR item IN SELECT table_name FROM information_schema.columns
        WHERE table_schema = 'public' AND column_name = 'channel_id'
          AND (table_name LIKE 'pricing_config\_%' ESCAPE '\' OR table_name = 'usage_logs') LOOP
        EXECUTE format('ALTER TABLE %I RENAME COLUMN channel_id TO pricing_config_id', item.table_name);
    END LOOP;
    FOR item IN SELECT c.conname, t.relname FROM pg_constraint c JOIN pg_class t ON t.oid = c.conrelid
        JOIN pg_namespace n ON n.oid = t.relnamespace
        WHERE n.nspname = 'public' AND (t.relname = 'pricing_configs' OR t.relname LIKE 'pricing_config\_%' ESCAPE '\')
          AND (c.conname LIKE '%channel%' OR c.conname LIKE 'idx_cas%') LOOP
        new_name := replace(replace(item.conname, 'channels', 'pricing_configs'), 'channel_', 'pricing_config_');
        IF new_name <> item.conname THEN
            EXECUTE format('ALTER TABLE %I RENAME CONSTRAINT %I TO %I', item.relname, item.conname, new_name);
        END IF;
    END LOOP;
    FOR item IN SELECT c.relname, c.relkind FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
        WHERE n.nspname = 'public' AND c.relkind IN ('i', 'S')
          AND (c.relname LIKE 'channels\_%' ESCAPE '\' OR c.relname LIKE 'channel\_%' ESCAPE '\' OR c.relname LIKE 'idx_channel%' OR c.relname LIKE 'idx_cas%channel%') LOOP
        old_name := item.relname;
        new_name := replace(replace(old_name, 'channels', 'pricing_configs'), 'channel_', 'pricing_config_');
        IF new_name <> old_name THEN
            EXECUTE format('ALTER %s %I RENAME TO %I', CASE WHEN item.relkind = 'S' THEN 'SEQUENCE' ELSE 'INDEX' END, old_name, new_name);
        END IF;
    END LOOP;
END $$;

UPDATE pricing_configs SET billing_model_source = 'group_mapped' WHERE billing_model_source = 'channel_mapped';
ALTER TABLE pricing_configs ALTER COLUMN billing_model_source SET DEFAULT 'group_mapped';
ALTER TABLE pricing_configs DROP COLUMN IF EXISTS model_mapping, DROP COLUMN IF EXISTS restrict_models,
    DROP COLUMN IF EXISTS features, DROP COLUMN IF EXISTS features_config;
COMMENT ON TABLE pricing_configs IS '共享价格配置：仅定义价格与计费口径';
COMMENT ON COLUMN groups.routing_policy IS '分组独立模型映射、白名单和功能策略';
COMMENT ON COLUMN usage_logs.pricing_config_id IS '请求使用的共享价格配置 ID，历史记录保留原 ID';
