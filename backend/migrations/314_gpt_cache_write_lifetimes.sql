-- Optional lifetime prices retain the legacy aggregate price when absent.
ALTER TABLE pricing_config_model_pricing
    ADD COLUMN IF NOT EXISTS cache_write_30m_price NUMERIC(20,12),
    ADD COLUMN IF NOT EXISTS cache_write_12h_price NUMERIC(20,12);

ALTER TABLE pricing_config_pricing_intervals
    ADD COLUMN IF NOT EXISTS cache_write_30m_price NUMERIC(20,12),
    ADD COLUMN IF NOT EXISTS cache_write_12h_price NUMERIC(20,12);

ALTER TABLE pricing_config_provider_stats_model_pricing
    ADD COLUMN IF NOT EXISTS cache_write_30m_price NUMERIC(20,12),
    ADD COLUMN IF NOT EXISTS cache_write_12h_price NUMERIC(20,12);

ALTER TABLE pricing_config_provider_stats_pricing_intervals
    ADD COLUMN IF NOT EXISTS cache_write_30m_price NUMERIC(20,12),
    ADD COLUMN IF NOT EXISTS cache_write_12h_price NUMERIC(20,12);

-- NULL distinguishes historical or unclassified usage from a reported zero.
ALTER TABLE usage_logs
    ADD COLUMN IF NOT EXISTS cache_creation_30m_tokens INTEGER,
    ADD COLUMN IF NOT EXISTS cache_creation_12h_tokens INTEGER;
