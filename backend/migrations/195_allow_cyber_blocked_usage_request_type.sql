-- Cyber 策略阻断记录使用 request_type=4，确保它们在用量审计中可见，
-- 且不会与 request_type=0 的历史记录混淆。
ALTER TABLE usage_logs
    DROP CONSTRAINT IF EXISTS usage_logs_request_type_check;

ALTER TABLE usage_logs
    ADD CONSTRAINT usage_logs_request_type_check
    CHECK (request_type IN (0, 1, 2, 3, 4)) NOT VALID;
