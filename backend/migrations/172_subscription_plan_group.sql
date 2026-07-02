ALTER TABLE subscription_plans
    ADD COLUMN IF NOT EXISTS group_id BIGINT NULL;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'subscription_plans_groups_subscription_plans'
    ) THEN
        ALTER TABLE subscription_plans
            ADD CONSTRAINT subscription_plans_groups_subscription_plans
            FOREIGN KEY (group_id) REFERENCES groups(id)
            ON DELETE SET NULL;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS subscription_plans_group_id
    ON subscription_plans(group_id);
