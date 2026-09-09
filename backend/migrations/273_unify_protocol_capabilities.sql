-- 一次切换迁移：停止旧实例后执行；保留其它凭据、自定义端点和任务资源绑定。
DO $$ BEGIN
 IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='groups' AND column_name='allowed_client_protocols')
 AND NOT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='groups' AND column_name='allowed_protocols') THEN
  ALTER TABLE groups RENAME COLUMN allowed_client_protocols TO allowed_protocols;
 END IF;
END $$;
ALTER TABLE groups ADD COLUMN IF NOT EXISTS protocol_fallbacks jsonb NOT NULL DEFAULT '{}';
ALTER TABLE groups ADD COLUMN IF NOT EXISTS responses_image_policy varchar NOT NULL DEFAULT 'inherit';

-- 临时函数只用于本次回填，执行结束即删除，不创建第二份运行时目录。
CREATE OR REPLACE FUNCTION pg_temp.native_protocols(p text, t text, auth text) RETURNS jsonb LANGUAGE plpgsql AS $$
BEGIN
 IF p='anthropic' AND t IN ('oauth','setup-token','apikey','bedrock','service_account') THEN RETURN '["anthropic_messages"]'; END IF;
 IF p='openai' AND t='apikey' THEN RETURN '["openai_responses","openai_chat_completions","openai_embeddings","openai_images_generations","openai_images_edits","openai_responses_websocket","openai_responses_compact","openai_alpha_search"]'; END IF;
 IF p='openai' AND t='oauth' THEN
  IF lower(auth) IN ('personalaccesstoken','personal_access_token') THEN RETURN '["openai_responses","openai_responses_websocket","openai_responses_compact"]'; END IF;
  IF lower(auth)='agentidentity' THEN RETURN '["openai_responses","openai_responses_websocket","openai_responses_compact","openai_alpha_search"]'; END IF;
  RETURN '["openai_responses","openai_responses_websocket","openai_responses_compact","openai_alpha_search","openai_live"]';
 END IF;
 IF p IN ('deepseek','kimi') AND t='apikey' THEN RETURN '["anthropic_messages","openai_responses","openai_chat_completions"]'; END IF;
 IF p='zhipu' AND t='apikey' THEN RETURN '["anthropic_messages","openai_chat_completions"]'; END IF;
 IF p='gemini' THEN
  IF t='apikey' THEN RETURN '["gemini_generate_content","gemini_batch_generate_content"]'; END IF;
  IF t='service_account' THEN RETURN '["gemini_generate_content","vertex_batch_prediction"]'; END IF;
  IF t='oauth' THEN RETURN '["gemini_generate_content"]'; END IF;
 END IF;
 IF p='antigravity' AND t='upstream' THEN RETURN '["anthropic_messages"]'; END IF;
 IF p='antigravity' AND t='oauth' THEN RETURN '["gemini_generate_content"]'; END IF;
 IF p='qoder' AND t='cosy' THEN RETURN '["qoder_chat"]'; END IF;
 IF p='grok' AND t IN ('oauth','apikey') THEN RETURN '["openai_responses","openai_chat_completions","openai_images_generations","openai_images_edits","grok_videos_generations","grok_videos_edits","grok_videos_extensions","grok_tts","grok_stt","grok_custom_voices","grok_voice_realtime"]'; END IF;
 RETURN '[]';
END $$;

-- 只回填缺少统一结构的记录，重放不能覆盖管理员的新设置。
UPDATE accounts SET credentials = jsonb_set(COALESCE(credentials,'{}'), '{upstream_protocols}',
 CASE WHEN platform IN ('kimi','zhipu','deepseek') THEN
  CASE COALESCE(credentials->>'api_protocol','chat_completions')
   WHEN 'adaptive' THEN pg_temp.native_protocols(platform,type,'')
   WHEN 'anthropic' THEN '["anthropic_messages"]'::jsonb
   WHEN 'responses' THEN CASE WHEN platform='zhipu' THEN '["openai_chat_completions"]'::jsonb ELSE '["openai_responses"]'::jsonb END
   ELSE '["openai_chat_completions"]'::jsonb END
 ELSE pg_temp.native_protocols(platform,type,COALESCE(credentials->>'auth_mode',credentials->>'openai_auth_mode','')) END)
WHERE NOT COALESCE(credentials,'{}') ? 'upstream_protocols' AND parent_account_id IS NULL;

-- 保留旧工作负载与强制协议限制；显式空工作负载仍不恢复文本能力。
UPDATE accounts SET credentials = jsonb_set(credentials,'{upstream_protocols}',
 COALESCE((SELECT jsonb_agg(p) FROM jsonb_array_elements_text(credentials->'upstream_protocols') p WHERE
  NOT (p='openai_responses' AND COALESCE(extra->>'openai_text_route_mode','')='force_chat_completions') AND
  NOT (p='openai_chat_completions' AND COALESCE(extra->>'openai_text_route_mode','')='force_responses') AND
  NOT (p='openai_embeddings' AND credentials ? 'openai_workload_capabilities' AND NOT (credentials->'openai_workload_capabilities' ? 'embeddings')) AND
  NOT (p IN ('openai_responses','openai_chat_completions','openai_responses_websocket','openai_responses_compact','openai_alpha_search') AND credentials ? 'openai_workload_capabilities' AND NOT (credentials->'openai_workload_capabilities' ? 'text_generation'))
 ),'[]'))
WHERE platform='openai' AND type='apikey' AND (credentials ? 'openai_workload_capabilities' OR extra ? 'openai_text_route_mode');

UPDATE accounts SET credentials = jsonb_set(credentials,'{api_base_urls}',
 jsonb_build_object(credentials->>'api_protocol',credentials->>'base_url') || COALESCE(credentials->'api_base_urls','{}'))
WHERE platform IN ('kimi','zhipu','deepseek') AND credentials->>'api_protocol' IN ('anthropic','responses','chat_completions') AND COALESCE(credentials->>'base_url','')<>'';
UPDATE accounts SET credentials = credentials - 'api_protocol' - 'openai_workload_capabilities' - 'openai_capabilities',
 extra = COALESCE(extra,'{}') - 'openai_text_route_mode' - 'openai_responses_mode'
WHERE credentials ?| ARRAY['api_protocol','openai_workload_capabilities','openai_capabilities'] OR extra ?| ARRAY['openai_text_route_mode','openai_responses_mode'];

-- 回填历史公开入口；借助临时标记区分首轮迁移和已完成后的重放。
DO $$ BEGIN
 IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname='groups_protocol_policy_v1' AND conrelid='groups'::regclass) THEN
  UPDATE groups SET allowed_protocols = COALESCE(allowed_protocols,'[]') ||
   CASE platform WHEN 'openai' THEN '["openai_embeddings","openai_responses_websocket","openai_responses_compact","openai_alpha_search"]'::jsonb
    WHEN 'grok' THEN '["openai_responses_websocket","openai_responses_compact","grok_videos_generations","grok_videos_edits","grok_videos_extensions","grok_tts","grok_stt","grok_custom_voices","grok_voice_realtime","grok_web_search","grok_x_search"]'::jsonb ELSE '[]'::jsonb END ||
   CASE WHEN platform IN ('openai','grok') AND allow_image_generation THEN '["openai_images_generations","openai_images_edits"]'::jsonb ELSE '[]'::jsonb END ||
   CASE WHEN platform='gemini' AND allow_batch_image_generation THEN '["image_batches"]'::jsonb ELSE '[]'::jsonb END ||
   CASE WHEN platform='openai' AND allow_live THEN '["openai_live"]'::jsonb ELSE '[]'::jsonb END;
  UPDATE groups SET protocol_fallbacks = CASE platform
   WHEN 'anthropic' THEN '{"anthropic_messages":"gemini_generate_content","openai_responses":"anthropic_messages","openai_chat_completions":"anthropic_messages"}'::jsonb
   WHEN 'gemini' THEN '{"anthropic_messages":"gemini_generate_content","openai_responses":"gemini_generate_content","openai_chat_completions":"gemini_generate_content"}'::jsonb
   WHEN 'antigravity' THEN '{"anthropic_messages":"gemini_generate_content","openai_responses":"gemini_generate_content","openai_chat_completions":"gemini_generate_content"}'::jsonb
   WHEN 'qoder' THEN '{"anthropic_messages":"qoder_chat","openai_responses":"qoder_chat","openai_chat_completions":"qoder_chat"}'::jsonb
   WHEN 'zhipu' THEN '{"anthropic_messages":"openai_chat_completions","openai_responses":"openai_chat_completions"}'::jsonb
   ELSE '{"anthropic_messages":"openai_responses","openai_chat_completions":"openai_responses","openai_responses":"openai_chat_completions"}'::jsonb END;
  UPDATE groups SET protocol_fallbacks = protocol_fallbacks || '{"openai_images_generations":"openai_responses","openai_images_edits":"openai_responses","openai_responses_websocket":"openai_responses","openai_alpha_search":"openai_responses"}'::jsonb WHERE platform='openai';
  UPDATE groups SET protocol_fallbacks = protocol_fallbacks || '{"openai_responses_websocket":"openai_responses","openai_responses_compact":"openai_responses","grok_web_search":"openai_responses","grok_x_search":"openai_responses"}'::jsonb WHERE platform='grok';
  UPDATE groups SET responses_image_policy=CASE WHEN allow_image_generation THEN 'inherit' ELSE 'block' END WHERE platform IN ('openai','grok');
  ALTER TABLE groups ADD CONSTRAINT groups_protocol_policy_v1 CHECK (jsonb_typeof(allowed_protocols)='array' AND jsonb_typeof(protocol_fallbacks)='object' AND responses_image_policy IN ('inherit','enabled','disabled','block'));
 END IF;
END $$;
