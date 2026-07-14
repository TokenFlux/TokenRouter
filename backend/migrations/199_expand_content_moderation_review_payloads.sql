-- 扩展内容审计复审材料，旧记录明确标记为内容不完整，不伪造历史正文。

ALTER TABLE content_moderation_logs
    ADD COLUMN IF NOT EXISTS source VARCHAR(16) NOT NULL DEFAULT 'user',
    ADD COLUMN IF NOT EXISTS input_items JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS content_complete BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS audit_complete BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS text_unit_count INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS image_unit_count INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS failed_unit_count INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS failed_units JSONB NOT NULL DEFAULT '[]'::jsonb;

ALTER TABLE content_moderation_cyber_warnings
    ADD COLUMN IF NOT EXISTS source VARCHAR(16) NOT NULL DEFAULT 'user',
    ADD COLUMN IF NOT EXISTS input_items JSONB NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS content_complete BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS audit_complete BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS text_unit_count INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS image_unit_count INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS failed_unit_count INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS failed_units JSONB NOT NULL DEFAULT '[]'::jsonb;

CREATE TABLE IF NOT EXISTS content_moderation_media (
    id                  BIGSERIAL PRIMARY KEY,
    log_id              BIGINT REFERENCES content_moderation_logs(id) ON DELETE CASCADE,
    cyber_warning_id    BIGINT REFERENCES content_moderation_cyber_warnings(id) ON DELETE CASCADE,
    source_index        INT NOT NULL DEFAULT 0,
    source              VARCHAR(16) NOT NULL DEFAULT 'user',
    mime_type           VARCHAR(255) NOT NULL DEFAULT '',
    sha256              VARCHAR(64) NOT NULL DEFAULT '',
    byte_size           BIGINT NOT NULL DEFAULT 0,
    original_ref        TEXT NOT NULL DEFAULT '',
    snapshot_status     VARCHAR(32) NOT NULL DEFAULT 'pending',
    snapshot_error      TEXT NOT NULL DEFAULT '',
    content             BYTEA,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT content_moderation_media_single_owner CHECK (
        (log_id IS NOT NULL AND cyber_warning_id IS NULL)
        OR (log_id IS NULL AND cyber_warning_id IS NOT NULL)
    )
);

CREATE INDEX IF NOT EXISTS idx_content_moderation_media_log_id
    ON content_moderation_media(log_id) WHERE log_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_content_moderation_media_cyber_warning_id
    ON content_moderation_media(cyber_warning_id) WHERE cyber_warning_id IS NOT NULL;
