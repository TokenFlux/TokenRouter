-- 图片和视频统一使用模型价卡；旧单价与独立倍率直接清除，不转换已有 model_pricing。
-- 删列升级必须停止旧实例，历史账单和异步任务定价快照保持原样。
ALTER TABLE groups
    DROP COLUMN IF EXISTS image_price_1k,
    DROP COLUMN IF EXISTS image_price_2k,
    DROP COLUMN IF EXISTS image_price_4k,
    DROP COLUMN IF EXISTS video_price_480p,
    DROP COLUMN IF EXISTS video_price_720p,
    DROP COLUMN IF EXISTS video_price_1080p,
    DROP COLUMN IF EXISTS video_model_prices,
    DROP COLUMN IF EXISTS image_rate_independent,
    DROP COLUMN IF EXISTS image_rate_multiplier,
    DROP COLUMN IF EXISTS video_rate_independent,
    DROP COLUMN IF EXISTS video_rate_multiplier;
