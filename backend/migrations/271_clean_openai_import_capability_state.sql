-- 清理导入模板中的历史能力状态；不补写缺省开关，也不改变其它模板参数。
DO $$
DECLARE
    raw_value TEXT;
    template JSONB;
    original_extra JSONB;
    cleaned_extra JSONB;
    mode_key TEXT;
BEGIN
    SELECT value INTO raw_value FROM settings
    WHERE key = 'openai_oauth_import_defaults' FOR UPDATE;
    IF raw_value IS NULL THEN
        RETURN;
    END IF;
    BEGIN
        template := raw_value::jsonb;
    EXCEPTION WHEN invalid_text_representation OR untranslatable_character OR numeric_value_out_of_range THEN
        -- 非法 JSON、不可表示的 Unicode 和超范围数值均保持原样，避免辅助模板阻断启动。
        RETURN;
    END;
    IF jsonb_typeof(template) IS DISTINCT FROM 'object'
       OR jsonb_typeof(template->'extra') IS DISTINCT FROM 'object' THEN
        RETURN;
    END IF;
    original_extra := template->'extra';
    cleaned_extra := original_extra - ARRAY[
        'openai_responses_probe_status', 'openai_responses_supported',
        'openai_compact_supported', 'openai_compact_checked_at',
        'openai_compact_last_status', 'openai_compact_last_error',
        'openai_native_compaction_v2_supported', 'openai_native_compaction_v2_checked_at',
        'openai_native_compaction_v2_last_status', 'openai_native_compaction_v2_last_error'
    ];
    FOREACH mode_key IN ARRAY ARRAY['openai_compact_mode', 'openai_native_compaction_v2_mode'] LOOP
        IF lower(btrim(cleaned_extra->>mode_key)) = 'auto' THEN
            cleaned_extra := jsonb_set(cleaned_extra, ARRAY[mode_key], '"force_on"'::jsonb);
        END IF;
    END LOOP;
    IF cleaned_extra IS DISTINCT FROM original_extra THEN
        UPDATE settings
        SET value = jsonb_set(template, '{extra}', cleaned_extra)::text, updated_at = NOW()
        WHERE key = 'openai_oauth_import_defaults';
    END IF;
END $$;
