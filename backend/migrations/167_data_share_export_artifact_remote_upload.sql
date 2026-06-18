-- 数据共享导出文件远端上传元数据；远端对象删除由管理员在存储桶侧自行管理。
ALTER TABLE data_share_export_artifacts
    ADD COLUMN IF NOT EXISTS remote_status VARCHAR(20) NOT NULL DEFAULT 'not_uploaded',
    ADD COLUMN IF NOT EXISTS remote_bucket TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS remote_key TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS remote_error_message TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS remote_uploaded_at TIMESTAMPTZ NULL;

CREATE INDEX IF NOT EXISTS idx_data_share_export_artifacts_remote_status
    ON data_share_export_artifacts (remote_status, updated_at DESC);
