-- Separate five-minute cache-write card prices from uniform cache_write_price
-- overrides so editing 5m rates preserves catalog hourly pricing.
ALTER TABLE pricing_config_model_pricing
    ADD COLUMN IF NOT EXISTS cache_write_5m_price NUMERIC(20,12);

ALTER TABLE pricing_config_pricing_intervals
    ADD COLUMN IF NOT EXISTS cache_write_5m_price NUMERIC(20,12);

ALTER TABLE pricing_config_provider_stats_model_pricing
    ADD COLUMN IF NOT EXISTS cache_write_5m_price NUMERIC(20,12);

ALTER TABLE pricing_config_provider_stats_pricing_intervals
    ADD COLUMN IF NOT EXISTS cache_write_5m_price NUMERIC(20,12);

-- Record raw upstream cache-write counters only when they disagree with the
-- settled aggregate; normal rows remain NULL.
ALTER TABLE usage_logs
    ADD COLUMN IF NOT EXISTS cache_write_anomaly JSONB;
