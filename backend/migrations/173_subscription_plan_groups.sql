-- 173: Allow each subscription plan to bind multiple OpenAI groups.

CREATE TABLE IF NOT EXISTS subscription_plan_groups (
    subscription_plan_id BIGINT NOT NULL REFERENCES subscription_plans(id) ON DELETE CASCADE,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (subscription_plan_id, group_id)
);

CREATE INDEX IF NOT EXISTS idx_subscription_plan_groups_group_id
    ON subscription_plan_groups(group_id);

INSERT INTO subscription_plan_groups (subscription_plan_id, group_id)
SELECT id, group_id
FROM subscription_plans
WHERE group_id IS NOT NULL
ON CONFLICT DO NOTHING;
