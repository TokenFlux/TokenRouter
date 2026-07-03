-- PR 3593 增加了 Grok 图片生成、图片编辑和视频生成路由。
-- 既有 Grok 分组早于该能力开关创建，需要补齐同一个生成能力 gate。
UPDATE groups
SET allow_image_generation = true
WHERE platform = 'grok'
  AND allow_image_generation = false;
