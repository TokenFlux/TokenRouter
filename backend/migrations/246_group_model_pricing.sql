ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS long_context_pricing_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN IF NOT EXISTS model_pricing JSONB;

UPDATE groups
SET long_context_pricing_enabled = TRUE
WHERE long_context_pricing_enabled IS DISTINCT FROM TRUE;

COMMENT ON COLUMN groups.long_context_pricing_enabled IS
    '是否启用内置长上下文阶梯价格；默认开启以保持现有计费行为';
COMMENT ON COLUMN groups.model_pricing IS
    '分组逐模型定价；优先级高于渠道和内置模型定价';
