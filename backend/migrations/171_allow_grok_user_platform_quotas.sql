-- user_platform_quotas.platform 的数据库约束需要和 service.AllowedQuotaPlatforms 保持一致。
-- 旧约束缺少 grok，会导致注册默认平台配额快照写入 grok 时触发 CHECK 失败。
ALTER TABLE user_platform_quotas
    DROP CONSTRAINT IF EXISTS user_platform_quotas_platform_check;

ALTER TABLE user_platform_quotas
    ADD CONSTRAINT user_platform_quotas_platform_check
    CHECK (platform IN ('anthropic', 'openai', 'gemini', 'antigravity', 'grok'));
