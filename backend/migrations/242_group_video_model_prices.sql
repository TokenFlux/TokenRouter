-- Grok Imagine 支持按模型族配置视频每秒价格。
-- 数据形态：{"grok-imagine-video":{"480p":0.05,"720p":0.07},"grok-imagine-video-1.5":{"480p":0.08,"720p":0.14,"1080p":0.25}}
-- 计费解析顺序：按模型配置、旧版 video_price_* 列、按模型区分的代码默认值。
ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS video_model_prices JSONB;

COMMENT ON COLUMN groups.video_model_prices IS
    '可选：按模型族×分辨率覆盖视频每秒单价 (USD/s)。key 为规范模型族 (grok-imagine-video / grok-imagine-video-1.5)，value 为分辨率→单价映射；NULL/空表示不覆盖，回退到 video_price_* 列或官方默认';
