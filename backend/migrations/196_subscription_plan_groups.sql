-- 新增订阅套餐可用分组映射。

ALTER TABLE subscription_plans
	ADD COLUMN IF NOT EXISTS group_ids JSONB NOT NULL DEFAULT '[]'::jsonb;

CREATE TABLE IF NOT EXISTS subscription_plan_groups (
	plan_id BIGINT NOT NULL REFERENCES subscription_plans(id) ON DELETE CASCADE,
	group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
	created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
	PRIMARY KEY (plan_id, group_id)
);

CREATE INDEX IF NOT EXISTS idx_subscription_plan_groups_group_id
	ON subscription_plan_groups(group_id);

COMMENT ON TABLE subscription_plan_groups IS '套餐可用分组映射；没有映射记录表示套餐对全部分组可用。';
