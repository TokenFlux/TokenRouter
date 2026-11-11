ALTER TABLE user_subscriptions
    ADD COLUMN IF NOT EXISTS refund_hold_operation_id text;
