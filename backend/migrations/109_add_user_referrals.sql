ALTER TABLE users ADD COLUMN IF NOT EXISTS referral_code VARCHAR(32) NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS referred_by_user_id BIGINT DEFAULT NULL;
ALTER TABLE users ADD COLUMN IF NOT EXISTS referral_reward_amount DECIMAL(20,8) NOT NULL DEFAULT 0;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'users_referred_by_user_id_fkey'
    ) THEN
        ALTER TABLE users
            ADD CONSTRAINT users_referred_by_user_id_fkey
                FOREIGN KEY (referred_by_user_id) REFERENCES users(id) ON DELETE SET NULL;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS users_referred_by_user_id_idx ON users(referred_by_user_id);
CREATE UNIQUE INDEX IF NOT EXISTS users_referral_code_unique_active
    ON users(referral_code)
    WHERE deleted_at IS NULL AND referral_code <> '';
