-- 为 ops_system_logs 增加 api_key_id，支持按 API Key 定位系统日志。
ALTER TABLE ops_system_logs
  ADD COLUMN IF NOT EXISTS api_key_id BIGINT;
