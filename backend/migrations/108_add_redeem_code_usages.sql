-- Support multi-use redeem codes with per-user usage records and optional expiry.

ALTER TABLE redeem_codes
    ADD COLUMN IF NOT EXISTS max_uses INT NOT NULL DEFAULT 1;

ALTER TABLE redeem_codes
    ADD COLUMN IF NOT EXISTS used_count INT NOT NULL DEFAULT 0;

ALTER TABLE redeem_codes
    ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_redeem_codes_expires_at ON redeem_codes(expires_at);

CREATE TABLE IF NOT EXISTS redeem_code_usages (
    id              BIGSERIAL PRIMARY KEY,
    redeem_code_id  BIGINT NOT NULL REFERENCES redeem_codes(id) ON DELETE CASCADE,
    user_id         BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    used_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_redeem_code_usages_redeem_code_id ON redeem_code_usages(redeem_code_id);
CREATE INDEX IF NOT EXISTS idx_redeem_code_usages_user_id ON redeem_code_usages(user_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_redeem_code_usages_code_user_unique
    ON redeem_code_usages(redeem_code_id, user_id);

INSERT INTO redeem_code_usages (redeem_code_id, user_id, used_at)
SELECT
    rc.id,
    rc.used_by,
    COALESCE(rc.used_at, rc.created_at)
FROM redeem_codes rc
WHERE rc.used_by IS NOT NULL
ON CONFLICT (redeem_code_id, user_id) DO NOTHING;

UPDATE redeem_codes rc
SET used_count = usage_counts.used_count
FROM (
    SELECT redeem_code_id, COUNT(*)::INT AS used_count
    FROM redeem_code_usages
    GROUP BY redeem_code_id
) AS usage_counts
WHERE rc.id = usage_counts.redeem_code_id;

UPDATE redeem_codes
SET used_count = 0
WHERE used_count IS NULL;

UPDATE redeem_codes
SET status = CASE
    WHEN status = 'expired' THEN 'expired'
    WHEN used_count >= max_uses THEN 'used'
    WHEN used_count > 0 THEN 'active'
    ELSE 'unused'
END;
