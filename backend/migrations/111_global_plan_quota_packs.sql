-- 111: 订阅改造为“全局窗口额度包（同 plan 顺延，不同 plan 共存）”

-- 新 schema 列
ALTER TABLE subscription_plans ADD COLUMN IF NOT EXISTS daily_limit_usd DECIMAL(20,8);
ALTER TABLE subscription_plans ADD COLUMN IF NOT EXISTS weekly_limit_usd DECIMAL(20,8);
ALTER TABLE subscription_plans ADD COLUMN IF NOT EXISTS monthly_limit_usd DECIMAL(20,8);

ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS plan_id BIGINT;
ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS daily_limit_usd DECIMAL(20,8);
ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS weekly_limit_usd DECIMAL(20,8);
ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS monthly_limit_usd DECIMAL(20,8);
ALTER TABLE user_subscriptions ADD COLUMN IF NOT EXISTS source_order_id BIGINT;

ALTER TABLE payment_orders ADD COLUMN IF NOT EXISTS plan_snapshot JSONB;

ALTER TABLE redeem_codes ADD COLUMN IF NOT EXISTS plan_id BIGINT;

ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS subscription_amount_usd DECIMAL(20,10) NOT NULL DEFAULT 0;
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS balance_amount_usd DECIMAL(20,10) NOT NULL DEFAULT 0;
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS billing_allocations JSONB;

-- 先将 legacy group 限额回填到现有 plan 模板
UPDATE subscription_plans sp
SET
    daily_limit_usd = g.daily_limit_usd,
    weekly_limit_usd = g.weekly_limit_usd,
    monthly_limit_usd = g.monthly_limit_usd
FROM groups g
WHERE sp.group_id = g.id
  AND (
      sp.daily_limit_usd IS DISTINCT FROM g.daily_limit_usd
      OR sp.weekly_limit_usd IS DISTINCT FROM g.weekly_limit_usd
      OR sp.monthly_limit_usd IS DISTINCT FROM g.monthly_limit_usd
  );

-- 迁移期间允许生成 legacy-only hidden plan（可能没有可用的 group_id）
ALTER TABLE subscription_plans ALTER COLUMN group_id DROP NOT NULL;

DROP TABLE IF EXISTS tmp_unique_existing_plan_map;
CREATE TEMP TABLE tmp_unique_existing_plan_map (
    legacy_group_id BIGINT,
    validity_days   INT NOT NULL,
    plan_id         BIGINT NOT NULL,
    PRIMARY KEY (legacy_group_id, validity_days)
) ON COMMIT DROP;

INSERT INTO tmp_unique_existing_plan_map (legacy_group_id, validity_days, plan_id)
SELECT
    sp.group_id,
    sp.validity_days,
    MIN(sp.id) AS plan_id
FROM subscription_plans sp
WHERE sp.group_id IS NOT NULL
GROUP BY sp.group_id, sp.validity_days
HAVING COUNT(*) = 1;

DROP TABLE IF EXISTS tmp_plan_resolution_requests;
CREATE TEMP TABLE tmp_plan_resolution_requests (
    mapping_key       TEXT PRIMARY KEY,
    legacy_group_id   BIGINT,
    validity_days     INT NOT NULL,
    daily_limit_usd   DECIMAL(20,8),
    weekly_limit_usd  DECIMAL(20,8),
    monthly_limit_usd DECIMAL(20,8)
) ON COMMIT DROP;

INSERT INTO tmp_plan_resolution_requests (mapping_key, legacy_group_id, validity_days, daily_limit_usd, weekly_limit_usd, monthly_limit_usd)
SELECT DISTINCT
    md5(jsonb_build_array(
        us.group_id,
        GREATEST(1, CEIL(EXTRACT(EPOCH FROM (us.expires_at - us.starts_at)) / 86400.0)::INT),
        COALESCE(us.daily_limit_usd, g.daily_limit_usd),
        COALESCE(us.weekly_limit_usd, g.weekly_limit_usd),
        COALESCE(us.monthly_limit_usd, g.monthly_limit_usd)
    )::TEXT),
    us.group_id,
    GREATEST(1, CEIL(EXTRACT(EPOCH FROM (us.expires_at - us.starts_at)) / 86400.0)::INT),
    COALESCE(us.daily_limit_usd, g.daily_limit_usd),
    COALESCE(us.weekly_limit_usd, g.weekly_limit_usd),
    COALESCE(us.monthly_limit_usd, g.monthly_limit_usd)
FROM user_subscriptions us
LEFT JOIN groups g ON g.id = us.group_id
WHERE us.plan_id IS NULL;

INSERT INTO tmp_plan_resolution_requests (mapping_key, legacy_group_id, validity_days, daily_limit_usd, weekly_limit_usd, monthly_limit_usd)
SELECT DISTINCT
    md5(jsonb_build_array(
        po.subscription_group_id,
        COALESCE(NULLIF(po.subscription_days, 0), 30),
        g.daily_limit_usd,
        g.weekly_limit_usd,
        g.monthly_limit_usd
    )::TEXT),
    po.subscription_group_id,
    COALESCE(NULLIF(po.subscription_days, 0), 30),
    g.daily_limit_usd,
    g.weekly_limit_usd,
    g.monthly_limit_usd
FROM payment_orders po
LEFT JOIN groups g ON g.id = po.subscription_group_id
WHERE po.order_type = 'subscription'
  AND po.plan_id IS NULL
  AND po.subscription_group_id IS NOT NULL
ON CONFLICT (mapping_key) DO NOTHING;

INSERT INTO tmp_plan_resolution_requests (mapping_key, legacy_group_id, validity_days, daily_limit_usd, weekly_limit_usd, monthly_limit_usd)
SELECT DISTINCT
    md5(jsonb_build_array(
        rc.group_id,
        COALESCE(NULLIF(rc.validity_days, 0), 30),
        g.daily_limit_usd,
        g.weekly_limit_usd,
        g.monthly_limit_usd
    )::TEXT),
    rc.group_id,
    COALESCE(NULLIF(rc.validity_days, 0), 30),
    g.daily_limit_usd,
    g.weekly_limit_usd,
    g.monthly_limit_usd
FROM redeem_codes rc
LEFT JOIN groups g ON g.id = rc.group_id
WHERE rc.type = 'subscription'
  AND rc.plan_id IS NULL
ON CONFLICT (mapping_key) DO NOTHING;

INSERT INTO tmp_plan_resolution_requests (mapping_key, legacy_group_id, validity_days, daily_limit_usd, weekly_limit_usd, monthly_limit_usd)
SELECT DISTINCT
    md5(jsonb_build_array(
        NULLIF(item->>'group_id', '')::BIGINT,
        COALESCE(NULLIF((item->>'validity_days')::INT, 0), 30),
        g.daily_limit_usd,
        g.weekly_limit_usd,
        g.monthly_limit_usd
    )::TEXT),
    NULLIF(item->>'group_id', '')::BIGINT,
    COALESCE(NULLIF((item->>'validity_days')::INT, 0), 30),
    g.daily_limit_usd,
    g.weekly_limit_usd,
    g.monthly_limit_usd
FROM settings s
CROSS JOIN LATERAL jsonb_array_elements(
    CASE
        WHEN NULLIF(BTRIM(s.value), '') IS NULL THEN '[]'::jsonb
        ELSE s.value::jsonb
    END
) AS e(item)
LEFT JOIN groups g ON g.id = NULLIF(item->>'group_id', '')::BIGINT
WHERE s.key = 'default_subscriptions'
  AND item ? 'group_id'
  AND NOT item ? 'plan_id'
ON CONFLICT (mapping_key) DO NOTHING;

DROP TABLE IF EXISTS tmp_resolved_plan_map;
CREATE TEMP TABLE tmp_resolved_plan_map (
    mapping_key       TEXT PRIMARY KEY,
    legacy_group_id   BIGINT,
    validity_days     INT NOT NULL,
    daily_limit_usd   DECIMAL(20,8),
    weekly_limit_usd  DECIMAL(20,8),
    monthly_limit_usd DECIMAL(20,8),
    plan_id           BIGINT NOT NULL
) ON COMMIT DROP;

INSERT INTO tmp_resolved_plan_map (mapping_key, legacy_group_id, validity_days, daily_limit_usd, weekly_limit_usd, monthly_limit_usd, plan_id)
SELECT
    req.mapping_key,
    req.legacy_group_id,
    req.validity_days,
    req.daily_limit_usd,
    req.weekly_limit_usd,
    req.monthly_limit_usd,
    upm.plan_id
FROM tmp_plan_resolution_requests req
JOIN tmp_unique_existing_plan_map upm
    ON upm.legacy_group_id IS NOT DISTINCT FROM req.legacy_group_id
   AND upm.validity_days = req.validity_days;

DO $$
DECLARE
    rec RECORD;
    hidden_plan_id BIGINT;
    hidden_name TEXT;
BEGIN
    FOR rec IN
        SELECT req.*
        FROM tmp_plan_resolution_requests req
        LEFT JOIN tmp_resolved_plan_map resolved ON resolved.mapping_key = req.mapping_key
        WHERE resolved.mapping_key IS NULL
        ORDER BY req.mapping_key
    LOOP
        hidden_name := CASE
            WHEN rec.legacy_group_id IS NULL THEN FORMAT('Migrated legacy %s-day hidden plan', rec.validity_days)
            ELSE FORMAT('Migrated legacy group %s %s-day hidden plan', rec.legacy_group_id, rec.validity_days)
        END;

        INSERT INTO subscription_plans (
            group_id,
            name,
            description,
            price,
            original_price,
            validity_days,
            daily_limit_usd,
            weekly_limit_usd,
            monthly_limit_usd,
            validity_unit,
            features,
            product_name,
            for_sale,
            sort_order,
            created_at,
            updated_at
        ) VALUES (
            rec.legacy_group_id,
            hidden_name,
            'migration-only hidden legacy plan',
            0,
            NULL,
            rec.validity_days,
            rec.daily_limit_usd,
            rec.weekly_limit_usd,
            rec.monthly_limit_usd,
            'day',
            '',
            '',
            FALSE,
            0,
            NOW(),
            NOW()
        )
        RETURNING id INTO hidden_plan_id;

        INSERT INTO tmp_resolved_plan_map (
            mapping_key,
            legacy_group_id,
            validity_days,
            daily_limit_usd,
            weekly_limit_usd,
            monthly_limit_usd,
            plan_id
        ) VALUES (
            rec.mapping_key,
            rec.legacy_group_id,
            rec.validity_days,
            rec.daily_limit_usd,
            rec.weekly_limit_usd,
            rec.monthly_limit_usd,
            hidden_plan_id
        );
    END LOOP;
END $$;

WITH legacy_user_subscriptions AS (
    SELECT
        us.id,
        resolved.plan_id,
        resolved.daily_limit_usd,
        resolved.weekly_limit_usd,
        resolved.monthly_limit_usd
    FROM user_subscriptions us
    LEFT JOIN groups g ON g.id = us.group_id
    JOIN tmp_resolved_plan_map resolved
        ON resolved.mapping_key = md5(jsonb_build_array(
            us.group_id,
            GREATEST(1, CEIL(EXTRACT(EPOCH FROM (us.expires_at - us.starts_at)) / 86400.0)::INT),
            COALESCE(us.daily_limit_usd, g.daily_limit_usd),
            COALESCE(us.weekly_limit_usd, g.weekly_limit_usd),
            COALESCE(us.monthly_limit_usd, g.monthly_limit_usd)
        )::TEXT)
    WHERE us.plan_id IS NULL
)
UPDATE user_subscriptions us
SET
    plan_id = legacy.plan_id,
    daily_limit_usd = COALESCE(us.daily_limit_usd, legacy.daily_limit_usd),
    weekly_limit_usd = COALESCE(us.weekly_limit_usd, legacy.weekly_limit_usd),
    monthly_limit_usd = COALESCE(us.monthly_limit_usd, legacy.monthly_limit_usd)
FROM legacy_user_subscriptions legacy
WHERE us.id = legacy.id;

UPDATE user_subscriptions us
SET
    daily_limit_usd = COALESCE(us.daily_limit_usd, sp.daily_limit_usd),
    weekly_limit_usd = COALESCE(us.weekly_limit_usd, sp.weekly_limit_usd),
    monthly_limit_usd = COALESCE(us.monthly_limit_usd, sp.monthly_limit_usd)
FROM subscription_plans sp
WHERE us.plan_id = sp.id
  AND (
      us.daily_limit_usd IS NULL
      OR us.weekly_limit_usd IS NULL
      OR us.monthly_limit_usd IS NULL
  );

ALTER TABLE user_subscriptions ALTER COLUMN plan_id SET NOT NULL;

WITH legacy_redeem_codes AS (
    SELECT
        rc.id,
        resolved.plan_id
    FROM redeem_codes rc
    LEFT JOIN groups g ON g.id = rc.group_id
    JOIN tmp_resolved_plan_map resolved
        ON resolved.mapping_key = md5(jsonb_build_array(
            rc.group_id,
            COALESCE(NULLIF(rc.validity_days, 0), 30),
            g.daily_limit_usd,
            g.weekly_limit_usd,
            g.monthly_limit_usd
        )::TEXT)
    WHERE rc.type = 'subscription'
      AND rc.plan_id IS NULL
)
UPDATE redeem_codes rc
SET plan_id = legacy.plan_id
FROM legacy_redeem_codes legacy
WHERE rc.id = legacy.id;

WITH legacy_payment_orders AS (
    SELECT
        po.id,
        resolved.plan_id
    FROM payment_orders po
    LEFT JOIN groups g ON g.id = po.subscription_group_id
    JOIN tmp_resolved_plan_map resolved
        ON resolved.mapping_key = md5(jsonb_build_array(
            po.subscription_group_id,
            COALESCE(NULLIF(po.subscription_days, 0), 30),
            g.daily_limit_usd,
            g.weekly_limit_usd,
            g.monthly_limit_usd
        )::TEXT)
    WHERE po.order_type = 'subscription'
      AND po.plan_id IS NULL
      AND po.subscription_group_id IS NOT NULL
)
UPDATE payment_orders po
SET plan_id = legacy.plan_id
FROM legacy_payment_orders legacy
WHERE po.id = legacy.id;

UPDATE payment_orders po
SET plan_snapshot = jsonb_strip_nulls(jsonb_build_object(
    'name', sp.name,
    'price', po.amount,
    'validity_days', COALESCE(NULLIF(po.subscription_days, 0), sp.validity_days, 30),
    'daily_limit_usd', sp.daily_limit_usd,
    'weekly_limit_usd', sp.weekly_limit_usd,
    'monthly_limit_usd', sp.monthly_limit_usd
))
FROM subscription_plans sp
WHERE po.order_type = 'subscription'
  AND po.plan_id = sp.id
  AND (po.plan_snapshot IS NULL OR po.plan_snapshot = '{}'::jsonb);

UPDATE user_subscriptions us
SET source_order_id = po.id
FROM payment_orders po
WHERE us.source_order_id IS NULL
  AND po.order_type = 'subscription'
  AND us.user_id = po.user_id
  AND us.plan_id = po.plan_id
  AND us.notes = FORMAT('payment order %s', po.id);

UPDATE settings s
SET value = COALESCE((
    SELECT jsonb_agg(jsonb_build_object('plan_id', dedup.plan_id) ORDER BY dedup.first_ord)::TEXT
    FROM (
        SELECT
            MIN(items.ord) AS first_ord,
            items.plan_id
        FROM (
            SELECT
                ordinality AS ord,
                CASE
                    WHEN item ? 'plan_id' THEN NULLIF(item->>'plan_id', '')::BIGINT
                    WHEN item ? 'group_id' THEN resolved.plan_id
                    ELSE NULL
                END AS plan_id
            FROM jsonb_array_elements(
                CASE
                    WHEN NULLIF(BTRIM(s.value), '') IS NULL THEN '[]'::jsonb
                    ELSE s.value::jsonb
                END
            ) WITH ORDINALITY AS e(item, ordinality)
            LEFT JOIN groups g ON g.id = NULLIF(item->>'group_id', '')::BIGINT
            LEFT JOIN tmp_resolved_plan_map resolved
                ON resolved.mapping_key = md5(jsonb_build_array(
                    NULLIF(item->>'group_id', '')::BIGINT,
                    COALESCE(NULLIF((item->>'validity_days')::INT, 0), 30),
                    g.daily_limit_usd,
                    g.weekly_limit_usd,
                    g.monthly_limit_usd
                )::TEXT)
        ) items
        WHERE items.plan_id IS NOT NULL
          AND items.plan_id > 0
        GROUP BY items.plan_id
    ) dedup
), '[]')
WHERE s.key = 'default_subscriptions';

DROP TABLE IF EXISTS tmp_legacy_group_plan_membership;
CREATE TEMP TABLE tmp_legacy_group_plan_membership (
    legacy_group_id BIGINT NOT NULL,
    plan_id         BIGINT NOT NULL,
    PRIMARY KEY (legacy_group_id, plan_id)
) ON COMMIT DROP;

INSERT INTO tmp_legacy_group_plan_membership (legacy_group_id, plan_id)
SELECT DISTINCT sp.group_id, sp.id
FROM subscription_plans sp
WHERE sp.group_id IS NOT NULL;

INSERT INTO tmp_legacy_group_plan_membership (legacy_group_id, plan_id)
SELECT DISTINCT resolved.legacy_group_id, resolved.plan_id
FROM tmp_resolved_plan_map resolved
WHERE resolved.legacy_group_id IS NOT NULL
ON CONFLICT (legacy_group_id, plan_id) DO NOTHING;

UPDATE announcements a
SET targeting = jsonb_build_object(
    'any_of',
    COALESCE((
        SELECT jsonb_agg(
            jsonb_build_object(
                'all_of',
                COALESCE((
                    SELECT jsonb_agg(
                        CASE
                            WHEN COALESCE(cond->>'type', '') = 'subscription' THEN
                                (cond - 'group_ids') || jsonb_build_object(
                                    'plan_ids',
                                    COALESCE((
                                        SELECT jsonb_agg(plan_ids.plan_id ORDER BY plan_ids.plan_id)
                                        FROM (
                                            SELECT DISTINCT pid AS plan_id
                                            FROM (
                                                SELECT NULLIF(existing_plan_id, '')::BIGINT AS pid
                                                FROM jsonb_array_elements_text(COALESCE(cond->'plan_ids', '[]'::jsonb)) AS existing(existing_plan_id)
                                                UNION
                                                SELECT membership.plan_id
                                                FROM jsonb_array_elements_text(COALESCE(cond->'group_ids', '[]'::jsonb)) AS legacy(group_id_text)
                                                JOIN tmp_legacy_group_plan_membership membership
                                                  ON membership.legacy_group_id = legacy.group_id_text::BIGINT
                                            ) combined
                                            WHERE pid IS NOT NULL
                                              AND pid > 0
                                        ) plan_ids
                                    ), '[]'::jsonb)
                                )
                            ELSE cond
                        END
                        ORDER BY cond_ord
                    )
                    FROM jsonb_array_elements(COALESCE(grouping->'all_of', '[]'::jsonb)) WITH ORDINALITY AS conds(cond, cond_ord)
                ), '[]'::jsonb)
            )
            ORDER BY group_ord
        )
        FROM jsonb_array_elements(COALESCE(a.targeting->'any_of', '[]'::jsonb)) WITH ORDINALITY AS groups(grouping, group_ord)
    ), '[]'::jsonb)
)
WHERE EXISTS (
    SELECT 1
    FROM jsonb_array_elements(COALESCE(a.targeting->'any_of', '[]'::jsonb)) AS groups(grouping)
    CROSS JOIN LATERAL jsonb_array_elements(COALESCE(grouping->'all_of', '[]'::jsonb)) AS conds(cond)
    WHERE cond ? 'group_ids'
);

CREATE INDEX IF NOT EXISTS idx_user_subscriptions_plan_id ON user_subscriptions(plan_id);
CREATE INDEX IF NOT EXISTS idx_user_subscriptions_starts_at ON user_subscriptions(starts_at);
CREATE INDEX IF NOT EXISTS idx_user_subscriptions_source_order_id ON user_subscriptions(source_order_id);
CREATE INDEX IF NOT EXISTS idx_user_subscriptions_user_status_starts_expires ON user_subscriptions(user_id, status, starts_at, expires_at);
CREATE INDEX IF NOT EXISTS idx_user_subscriptions_user_plan_starts ON user_subscriptions(user_id, plan_id, starts_at);
CREATE INDEX IF NOT EXISTS idx_redeem_codes_plan_id ON redeem_codes(plan_id);

ALTER TABLE user_subscriptions DROP CONSTRAINT IF EXISTS user_subscriptions_user_id_group_id_key;

DROP INDEX IF EXISTS idx_groups_subscription_type;
DROP INDEX IF EXISTS idx_subscription_plans_group_id;
DROP INDEX IF EXISTS idx_user_subscriptions_group_id;
DROP INDEX IF EXISTS idx_redeem_codes_group_id;

ALTER TABLE groups DROP COLUMN IF EXISTS subscription_type;
ALTER TABLE groups DROP COLUMN IF EXISTS daily_limit_usd;
ALTER TABLE groups DROP COLUMN IF EXISTS weekly_limit_usd;
ALTER TABLE groups DROP COLUMN IF EXISTS monthly_limit_usd;
ALTER TABLE groups DROP COLUMN IF EXISTS default_validity_days;

ALTER TABLE subscription_plans DROP COLUMN IF EXISTS group_id;

ALTER TABLE user_subscriptions DROP COLUMN IF EXISTS group_id;

ALTER TABLE payment_orders DROP COLUMN IF EXISTS subscription_group_id;
ALTER TABLE payment_orders DROP COLUMN IF EXISTS subscription_days;

ALTER TABLE redeem_codes DROP COLUMN IF EXISTS group_id;
ALTER TABLE redeem_codes DROP COLUMN IF EXISTS validity_days;
