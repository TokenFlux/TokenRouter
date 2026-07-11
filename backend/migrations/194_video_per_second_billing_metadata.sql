-- Grok 视频按输出秒数计费，因此用量记录必须保存计费分辨率和时长以供审计。
-- 图片尺寸约束按 video_count 豁免所有视频行，而不能只判断 billing_mode='video'：
-- 使用 token 渠道价的视频会写入 billing_mode='token' 和兼容旧展示的 image_count=1，
-- 但没有 image_size；旧约束会拒绝该行并导致整个计费事务丢失。

ALTER TABLE usage_logs
    ADD COLUMN IF NOT EXISTS video_count INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS video_resolution VARCHAR(10),
    ADD COLUMN IF NOT EXISTS video_duration_seconds INTEGER;

COMMENT ON COLUMN usage_logs.video_count IS '视频生成数量；>0 表示本行是视频生成用量';
COMMENT ON COLUMN usage_logs.video_resolution IS '计费用视频分辨率 480p/720p/1080p';
COMMENT ON COLUMN usage_logs.video_duration_seconds IS '提交时请求的视频时长（秒），按秒计费的乘数';

ALTER TABLE usage_logs
    DROP CONSTRAINT IF EXISTS usage_logs_image_billing_size_check;

ALTER TABLE usage_logs
    ADD CONSTRAINT usage_logs_image_billing_size_check
    CHECK (
        image_count <= 0
        OR billing_mode = 'video'
        OR COALESCE(video_count, 0) > 0
        OR (
            image_size IS NOT NULL
            AND image_size IN ('1K', '2K', '4K', 'mixed')
        )
    ) NOT VALID;

-- 迁移 192 新增的分组视频价格是与 xAI 价卡一致的每秒单价（USD/s），
-- 总价 = 每秒单价 × 视频时长（秒）。
COMMENT ON COLUMN groups.video_price_480p IS '480p 视频生成每秒单价 (USD/s)，Grok 平台使用';
COMMENT ON COLUMN groups.video_price_720p IS '720p 视频生成每秒单价 (USD/s)，Grok 平台使用';
COMMENT ON COLUMN groups.video_price_1080p IS '1080p 视频生成每秒单价 (USD/s)，Grok 平台使用';
