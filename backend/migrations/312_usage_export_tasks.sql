-- 312_usage_export_tasks.sql
-- Persist background CSV export tasks for user and administrator usage records.

CREATE TABLE IF NOT EXISTS usage_export_tasks (
    id BIGSERIAL PRIMARY KEY,
    scope VARCHAR(16) NOT NULL,
    status VARCHAR(20) NOT NULL,
    filters JSONB NOT NULL,
    created_by BIGINT NOT NULL,
    total_rows BIGINT NOT NULL DEFAULT 0,
    exported_rows BIGINT NOT NULL DEFAULT 0,
    file_name TEXT,
    file_path TEXT,
    file_size_bytes BIGINT NOT NULL DEFAULT 0,
    error_message TEXT,
    canceled_by BIGINT,
    canceled_at TIMESTAMPTZ,
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_usage_export_tasks_status_created_at
    ON usage_export_tasks (status, created_at);

CREATE INDEX IF NOT EXISTS idx_usage_export_tasks_scope_creator_created_at
    ON usage_export_tasks (scope, created_by, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_usage_export_tasks_scope_created_at
    ON usage_export_tasks (scope, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS idx_usage_export_tasks_status_expires_at
    ON usage_export_tasks (status, expires_at);
