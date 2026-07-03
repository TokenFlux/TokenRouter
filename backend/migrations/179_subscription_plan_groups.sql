-- Add subscription plan group availability mapping.

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

-- Backfill legacy plans as available to all current groups to preserve behavior on upgrade.
INSERT INTO subscription_plan_groups (plan_id, group_id)
SELECT sp.id, g.id
FROM subscription_plans sp
CROSS JOIN groups g
WHERE g.deleted_at IS NULL
ON CONFLICT DO NOTHING;

UPDATE subscription_plans sp
SET group_ids = COALESCE((
	SELECT jsonb_agg(spg.group_id ORDER BY spg.group_id)
	FROM subscription_plan_groups spg
	WHERE spg.plan_id = sp.id
), '[]'::jsonb)
WHERE group_ids = '[]'::jsonb
	AND EXISTS (
		SELECT 1
		FROM subscription_plan_groups spg
		WHERE spg.plan_id = sp.id
	);
