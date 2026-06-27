ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS unavailable_fallback_group_id BIGINT REFERENCES groups(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_groups_unavailable_fallback_group_id
    ON groups(unavailable_fallback_group_id)
    WHERE deleted_at IS NULL AND unavailable_fallback_group_id IS NOT NULL;

COMMENT ON COLUMN groups.unavailable_fallback_group_id IS '当前分组不可用时优先回退使用的分组 ID';
