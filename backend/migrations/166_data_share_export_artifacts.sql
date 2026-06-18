-- 数据共享预生成导出文件元数据，下载时只读取本地文件，避免在下载请求中实时处理大批量数据。
CREATE TABLE IF NOT EXISTS data_share_export_artifacts (
    id BIGSERIAL PRIMARY KEY,
    status VARCHAR(20) NOT NULL DEFAULT 'pending',
    filename VARCHAR(255) NOT NULL,
    storage_path TEXT NOT NULL DEFAULT '',
    encoding VARCHAR(20) NOT NULL DEFAULT 'zstd',
    filters JSONB NOT NULL DEFAULT '{}'::jsonb,
    session_count BIGINT NOT NULL DEFAULT 0,
    file_size BIGINT NOT NULL DEFAULT 0,
    sha256 VARCHAR(64) NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    started_at TIMESTAMPTZ NULL,
    completed_at TIMESTAMPTZ NULL,
    deleted_at TIMESTAMPTZ NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_data_share_export_artifacts_status_created_at
    ON data_share_export_artifacts (status, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_data_share_export_artifacts_created_at
    ON data_share_export_artifacts (created_at DESC);
