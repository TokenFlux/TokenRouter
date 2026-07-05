-- Add subscription plan group availability mapping.

ALTER TABLE subscription_plans
	ADD COLUMN IF NOT EXISTS group_ids JSONB NOT NULL DEFAULT '[]'::jsonb;

CREATE TABLE IF NOT EXISTS subscription_plan_groups (
	plan_id BIGINT NOT NULL REFERENCES subscription_plans(id) ON DELETE CASCADE,
	group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
	rate_multiplier DECIMAL(20,8),
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	PRIMARY KEY (plan_id, group_id)
);

CREATE INDEX IF NOT EXISTS idx_subscription_plan_groups_group_id
	ON subscription_plan_groups(group_id);

COMMENT ON TABLE subscription_plan_groups IS '套餐可用分组映射；没有映射记录表示套餐对全部分组可用。';
COMMENT ON COLUMN subscription_plan_groups.rate_multiplier IS '套餐在该分组使用订阅额度时的专属计费倍率；NULL 表示沿用分组默认倍率。';
