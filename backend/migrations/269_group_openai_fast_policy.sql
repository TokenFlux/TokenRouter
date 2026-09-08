-- 新增互斥策略并仅在首次创建列时回填旧开关，重复运行不覆盖新配置。
DO $$
BEGIN
 IF NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='groups' AND column_name='openai_fast_policy') THEN
  ALTER TABLE groups ADD COLUMN openai_fast_policy VARCHAR(32) NOT NULL DEFAULT 'follow_request';
  UPDATE groups SET openai_fast_policy='force_priority' WHERE force_openai_fast=TRUE AND platform IN ('openai','composite');
  ALTER TABLE groups ADD CONSTRAINT groups_openai_fast_policy_check CHECK (openai_fast_policy IN ('follow_request','force_priority','force_ultrafast','force_off'));
 END IF;
END $$;
COMMENT ON COLUMN groups.openai_fast_policy IS 'OpenAI/Composite 分组加速策略；全局规则保持最终裁决权';
