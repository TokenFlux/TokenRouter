-- 请求详情按请求 ID 读取审计记录，并发建索引允许业务继续写入。
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_audit_logs_request_id_created_at
    ON audit_logs (request_id, created_at DESC);
