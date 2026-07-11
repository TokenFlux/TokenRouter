-- 新增套餐在各分组下使用订阅额度时的专属计费倍率。

ALTER TABLE subscription_plan_groups
	ADD COLUMN IF NOT EXISTS rate_multiplier DECIMAL(20,8);

ALTER TABLE subscription_plans
	ADD COLUMN IF NOT EXISTS group_rate_multipliers JSONB NOT NULL DEFAULT '{}'::jsonb;

COMMENT ON COLUMN subscription_plan_groups.rate_multiplier IS '套餐在该分组使用订阅额度时的专属计费倍率；NULL 表示沿用分组默认倍率。';
