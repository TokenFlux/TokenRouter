-- 风控中心：记录关键词拦截命中的具体关键词，便于管理员定位拦截原因。

ALTER TABLE content_moderation_logs ADD COLUMN IF NOT EXISTS matched_keyword VARCHAR(255) NOT NULL DEFAULT '';
