-- 为 api_key_id + 创建时间添加并发索引，避免线上建索引长时间阻塞写入。
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_ops_system_logs_api_key_id_created_at
  ON ops_system_logs (api_key_id, created_at DESC);
