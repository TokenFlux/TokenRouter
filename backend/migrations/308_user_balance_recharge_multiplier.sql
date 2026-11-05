-- Unset accounts follow the current global recharge rate. Existing orders retain
-- their saved credited amounts, including when this override later changes.
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS balance_recharge_multiplier DOUBLE PRECISION;

-- PostgreSQL orders NaN above infinity; the upper bound rejects both values.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conrelid = 'users'::regclass
          AND conname = 'users_balance_recharge_multiplier_valid'
    ) THEN
        ALTER TABLE users ADD CONSTRAINT users_balance_recharge_multiplier_valid
            CHECK (balance_recharge_multiplier > 0
                   AND balance_recharge_multiplier < 'Infinity'::DOUBLE PRECISION);
    END IF;
END $$;
