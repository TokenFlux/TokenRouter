-- 单个 API Key 的 Fast 模式策略；存量 Key 默认继续跟随下游请求。
ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS fast_mode_policy VARCHAR(32) NOT NULL DEFAULT 'follow_request';

-- 数据库约束防止绕过应用层写入未知策略值。
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conname = 'api_keys_fast_mode_policy_check'
          AND conrelid = 'api_keys'::regclass
    ) THEN
        ALTER TABLE api_keys
            ADD CONSTRAINT api_keys_fast_mode_policy_check
            CHECK (fast_mode_policy IN ('follow_request', 'force_on', 'force_off'));
    END IF;
END $$;
