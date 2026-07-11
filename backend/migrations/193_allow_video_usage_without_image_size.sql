-- Grok 视频生成使用 billing_mode='video'，并保留 image_count=1 作为兼容旧展示的媒体计数。
-- 视频计费依赖 video_resolution 和请求元数据，不应强制写入 image_size。

ALTER TABLE usage_logs
    DROP CONSTRAINT IF EXISTS usage_logs_image_billing_size_check;

ALTER TABLE usage_logs
    ADD CONSTRAINT usage_logs_image_billing_size_check
    CHECK (
        image_count <= 0
        OR billing_mode = 'video'
        OR (
            image_size IS NOT NULL
            AND image_size IN ('1K', '2K', '4K', 'mixed')
        )
    ) NOT VALID;
