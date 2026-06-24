-- 为预生成导出游标分页提供复合索引，避免大表按 created_at,id 排序时额外扫描。
CREATE INDEX IF NOT EXISTS idx_data_share_sessions_created_at_id
    ON data_share_sessions (created_at, id);
