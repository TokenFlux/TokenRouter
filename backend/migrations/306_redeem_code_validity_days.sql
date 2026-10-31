-- 306_redeem_code_validity_days.sql preserves the subscription duration accepted
-- when a plugin issues a redeem code. Existing credentials retain NULL and use
-- the live plan at redemption, while an explicit day count remains separate
-- from the credential's own expires_at deadline.
ALTER TABLE redeem_codes
    ADD COLUMN IF NOT EXISTS validity_days INTEGER
    CONSTRAINT redeem_codes_validity_days_check
    CHECK (validity_days IS NULL OR (type = 'subscription' AND validity_days BETWEEN 1 AND 36500));
