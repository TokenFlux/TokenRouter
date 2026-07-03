-- Add per-plan group rate multipliers for subscription billing.

ALTER TABLE subscription_plans
	ADD COLUMN IF NOT EXISTS group_rate_multipliers JSONB NOT NULL DEFAULT '{}'::jsonb;
