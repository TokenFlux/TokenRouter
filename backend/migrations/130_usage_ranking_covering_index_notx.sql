CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_ranking_cover
    ON usage_logs (
        created_at,
        user_id
    )
    INCLUDE (
        input_tokens,
        output_tokens,
        cache_creation_tokens,
        cache_read_tokens,
        actual_cost
    );
