-- 账号成本不再复用用户自定义价；删除旧开关不改写历史金额或任务价格快照。
-- 删列升级前需要停止旧实例。
ALTER TABLE pricing_configs DROP COLUMN IF EXISTS apply_pricing_to_account_stats;

-- 分组图片行为统一在协议控制中设置，保留账号覆盖和全局默认值。
UPDATE groups
SET routing_policy = routing_policy #- '{features_config,codex_image_generation_bridge}'
WHERE (routing_policy -> 'features_config') ? 'codex_image_generation_bridge';
