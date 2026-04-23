-- 112_add_group_default_flag.sql
-- 为 groups 表增加显式默认分组标记
-- 约束：同一平台仅允许一个处于 active 状态的默认分组

ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS is_default BOOLEAN NOT NULL DEFAULT FALSE;

CREATE UNIQUE INDEX IF NOT EXISTS groups_platform_default_active_unique
    ON groups(platform)
    WHERE deleted_at IS NULL AND is_default = TRUE AND status = 'active';
