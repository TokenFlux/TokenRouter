-- 每个阶段先取得候选数组，后续查询按实际 ID 定位，减少缺少计费索引统计时的行数误估。
CREATE OR REPLACE FUNCTION request_lookup_ids(search_id TEXT) RETURNS TABLE(id TEXT)
LANGUAGE plpgsql STABLE AS $$
DECLARE
    lookup_value TEXT := btrim(search_id);
    candidate_ids TEXT[] := ARRAY[]::TEXT[];
    matched_ids TEXT[];
    related_ids TEXT[];
    expanded_ids TEXT[];
BEGIN
    SELECT ARRAY(SELECT r.request_id FROM request_records r WHERE r.request_id=lookup_value)
    INTO matched_ids;

    -- 已保存的本地 ID 直接进入关联查询，外部别名在本地 ID 未命中时展开。
    IF cardinality(matched_ids)=0 THEN
        WITH base AS (
            SELECT lookup_value AS value
            UNION SELECT regexp_replace(lookup_value, '^(client:|local:|generated:)', '')
        ), variants AS (
            SELECT value FROM base
            UNION SELECT 'client:' || value FROM base
            UNION SELECT 'local:' || value FROM base
        ), legacy AS (
            SELECT o.request_id, o.client_request_id
            FROM variants v JOIN ops_error_logs o ON o.request_id=v.value
            UNION
            SELECT o.request_id, o.client_request_id
            FROM variants v JOIN ops_error_logs o ON o.client_request_id=v.value
            UNION
            SELECT l.request_id, l.client_request_id
            FROM variants v JOIN ops_system_logs l ON l.request_id=v.value
            UNION
            SELECT l.request_id, l.client_request_id
            FROM variants v JOIN ops_system_logs l ON l.client_request_id=v.value
        )
        SELECT ARRAY(
            SELECT v.value FROM variants v
            -- OFFSET 0 让每个候选值单独查计费索引，阻止规划器合并成大表哈希关联。
            UNION SELECT u.request_id FROM variants v
            CROSS JOIN LATERAL (
                SELECT usage.request_id FROM usage_logs usage
                WHERE COALESCE(usage.billing_key,usage.request_id)=v.value OFFSET 0
            ) u
            UNION SELECT u.request_id FROM variants v JOIN usage_logs u ON u.upstream_request_id=v.value
            UNION SELECT l.request_id FROM legacy l
            UNION SELECT l.client_request_id FROM legacy l
            UNION SELECT 'client:' || l.client_request_id FROM legacy l
        ) INTO candidate_ids;

        SELECT ARRAY(
            SELECT r.request_id FROM request_records r WHERE r.request_id=ANY(candidate_ids)
            UNION SELECT r.request_id FROM request_records r WHERE r.aliases && candidate_ids
        ) INTO matched_ids;
    END IF;

    SELECT ARRAY(
        SELECT relation->>'value' FROM request_records r
        CROSS JOIN LATERAL jsonb_array_elements(COALESCE(r.record->'aliases','[]'::jsonb)) relation
        WHERE r.request_id=ANY(matched_ids) AND relation->>'kind'='related'
        UNION
        SELECT child.request_id FROM request_records child
        WHERE child.parent_request_id <> '' AND child.parent_request_id=ANY(matched_ids)
    ) INTO related_ids;
    expanded_ids := matched_ids || related_ids;

    -- 父子请求的计费键与 API Key 一起匹配现有唯一索引，历史引用也来自这组请求。
    RETURN QUERY
    SELECT candidate.value FROM unnest(candidate_ids) candidate(value)
    WHERE candidate.value IS NOT NULL AND candidate.value <> ''
    UNION SELECT matched.value FROM unnest(matched_ids) matched(value)
    UNION
    SELECT u.request_id::TEXT FROM request_records r
    CROSS JOIN LATERAL jsonb_array_elements(COALESCE(r.record->'aliases','[]'::jsonb)) a
    JOIN usage_logs u ON COALESCE(u.billing_key,u.request_id)=a->>'value'
      AND u.api_key_id=(r.record->>'api_key_id')::bigint
    WHERE r.request_id=ANY(expanded_ids) AND a->>'kind'='billing'
    UNION
    SELECT a->>'value' FROM request_records r
    CROSS JOIN LATERAL jsonb_array_elements(COALESCE(r.record->'aliases','[]'::jsonb)) a
    WHERE r.request_id=ANY(expanded_ids) AND a->>'kind'='legacy'
    UNION SELECT related.value FROM unnest(related_ids) related(value);
END
$$;
