-- user_platform_quotas.platform 的数据库约束需要和 service.AllowedQuotaPlatforms 保持一致。
-- 旧约束缺少 qoder，会导致 Qoder user × platform quota 写入触发 CHECK 失败。
ALTER TABLE user_platform_quotas
    DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;

ALTER TABLE user_platform_quotas
    ADD CONSTRAINT user_platform_quotas_platform_check
    CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'qoder', 'grok'));
