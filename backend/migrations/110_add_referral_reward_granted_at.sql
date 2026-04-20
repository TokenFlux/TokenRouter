ALTER TABLE users ADD COLUMN IF NOT EXISTS referral_reward_granted_at TIMESTAMPTZ DEFAULT NULL;

COMMENT ON COLUMN users.referral_reward_granted_at IS '邀请返利实际发放时间；NULL 表示尚未因首次成功付费发放返利';

UPDATE users
SET referral_reward_granted_at = COALESCE(created_at, NOW())
WHERE referral_reward_granted_at IS NULL
  AND referred_by_user_id IS NOT NULL
  AND referral_reward_amount > 0;

CREATE INDEX IF NOT EXISTS users_referred_by_user_id_reward_granted_idx
    ON users(referred_by_user_id, referral_reward_granted_at)
    WHERE referral_reward_granted_at IS NOT NULL;
