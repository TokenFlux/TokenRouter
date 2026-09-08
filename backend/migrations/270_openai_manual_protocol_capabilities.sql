-- 将旧自动压缩模式冻结为升级时的有效开关，后续只由管理员决定。
-- 显式 force_on/force_off 保持不变；无历史结论的账号保持原有允许调度行为。
UPDATE accounts
SET extra = (COALESCE(extra, '{}'::jsonb) || jsonb_build_object(
    'openai_compact_mode', CASE
        WHEN extra->>'openai_compact_mode' IN ('force_on', 'force_off') THEN extra->>'openai_compact_mode'
        WHEN extra->'openai_compact_supported' = 'false'::jsonb THEN 'force_off'
        ELSE 'force_on' END,
    'openai_native_compaction_v2_mode', CASE
        WHEN extra->>'openai_native_compaction_v2_mode' IN ('force_on', 'force_off') THEN extra->>'openai_native_compaction_v2_mode'
        WHEN extra->'openai_native_compaction_v2_supported' = 'false'::jsonb THEN 'force_off'
        ELSE 'force_on' END
)) - ARRAY[
    'openai_responses_probe_status', 'openai_responses_supported',
    'openai_compact_supported', 'openai_compact_checked_at', 'openai_compact_last_status', 'openai_compact_last_error',
    'openai_native_compaction_v2_supported', 'openai_native_compaction_v2_checked_at',
    'openai_native_compaction_v2_last_status', 'openai_native_compaction_v2_last_error'
]
WHERE platform = 'openai';

-- 国产账号原本借用探测状态表达固定协议，现在直接保存显式路由，避免被 OpenAI 探测下线影响。
UPDATE accounts
SET extra = (COALESCE(extra, '{}'::jsonb) || jsonb_build_object(
    'openai_text_route_mode', CASE
        WHEN credentials->>'api_protocol' = 'responses'
          OR (platform IN ('kimi', 'deepseek') AND credentials->>'api_protocol' = 'adaptive') THEN 'force_responses'
        ELSE 'force_chat_completions' END
)) - ARRAY['openai_responses_probe_status', 'openai_responses_supported']
WHERE platform IN ('kimi', 'zhipu', 'deepseek') AND type = 'apikey';
