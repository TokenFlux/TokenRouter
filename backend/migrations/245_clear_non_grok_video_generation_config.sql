-- 视频生成仅属于 Grok/xAI；清理其它平台分组中遗留的视频定价。
-- 这些列对应迁移 192/242（video_price_* / video_model_prices），本分支不存在独立的
-- allow_video_generation 字段。

-- 清理不可逆，因此先保存非 Grok 分组的旧配置，避免部署者失去手工配置的恢复依据。
-- CREATE TABLE IF NOT EXISTS ... AS SELECT 重放时不会覆盖首次快照，迁移保持幂等。
CREATE TABLE IF NOT EXISTS groups_video_price_backup_245 AS
SELECT id AS group_id,
       platform,
       video_price_480p,
       video_price_720p,
       video_price_1080p,
       video_model_prices,
       now() AS backed_up_at
FROM groups
WHERE platform IS DISTINCT FROM 'grok'
  AND platform IS DISTINCT FROM 'composite'
  AND (
      video_price_480p IS NOT NULL
      OR video_price_720p IS NOT NULL
      OR video_price_1080p IS NOT NULL
      OR video_model_prices IS NOT NULL
  );

COMMENT ON TABLE groups_video_price_backup_245 IS
    '迁移 245 清空非 Grok/非 composite 分组视频价前的快照。composite 可能路由到 Grok 账号，予以保留。确认无需回滚后可安全 DROP；回滚方式：UPDATE groups g SET video_price_480p = b.video_price_480p, ... FROM groups_video_price_backup_245 b WHERE g.id = b.group_id';

UPDATE groups
SET video_price_480p = NULL,
    video_price_720p = NULL,
    video_price_1080p = NULL,
    video_model_prices = NULL
WHERE platform IS DISTINCT FROM 'grok'
  AND platform IS DISTINCT FROM 'composite'
  AND (
      video_price_480p IS NOT NULL
      OR video_price_720p IS NOT NULL
      OR video_price_1080p IS NOT NULL
      OR video_model_prices IS NOT NULL
  );
