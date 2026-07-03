-- Add per-plan group rate multipliers for subscription billing.

ALTER TABLE subscription_plans
	ADD COLUMN IF NOT EXISTS group_rate_multipliers JSONB NOT NULL DEFAULT '{}'::jsonb;

UPDATE subscription_plans sp
SET group_rate_multipliers = COALESCE((
	SELECT jsonb_object_agg(group_id::text, 1.0)
	FROM jsonb_array_elements_text(sp.group_ids) AS ids(group_id)
), '{}'::jsonb)
WHERE group_rate_multipliers = '{}'::jsonb;
