-- Marketplace recent request history must fetch successful usage rows and
-- failed ops error rows by the same request-visible/effective model key before
-- applying per-model limits. These expression indexes support that per-pair
-- query shape without falling back to group-wide candidate sampling.
--
-- This migration is intentionally *_notx because CREATE INDEX CONCURRENTLY
-- cannot run inside a transaction.

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_marketplace_recent_effective_model
    ON usage_logs (
        group_id,
        (COALESCE(NULLIF(requested_model, ''), NULLIF(model, ''), NULLIF(upstream_model, ''))),
        created_at DESC
    )
    WHERE actual_cost > 0;

CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_ops_error_logs_marketplace_recent_effective_model
    ON ops_error_logs (
        group_id,
        (COALESCE(NULLIF(requested_model, ''), NULLIF(model, ''), NULLIF(upstream_model, ''))),
        created_at DESC
    )
    WHERE COALESCE(status_code, 0) >= 400
      AND is_count_tokens = FALSE;
