-- 为待处理 scheduler outbox 事件增加去重键。
ALTER TABLE scheduler_outbox
    ADD COLUMN IF NOT EXISTS dedup_key TEXT;
