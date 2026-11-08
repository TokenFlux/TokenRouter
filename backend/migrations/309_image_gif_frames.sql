-- Animation controls and observed frame units preserve static records with zero
-- defaults. File counts retain their existing meaning across this upgrade.
ALTER TABLE creative_runs ADD COLUMN IF NOT EXISTS frame_count integer NOT NULL DEFAULT 0;
ALTER TABLE creative_runs ADD COLUMN IF NOT EXISTS frame_duration_ms integer NOT NULL DEFAULT 0;
ALTER TABLE creative_run_outputs ADD COLUMN IF NOT EXISTS frame_count integer NOT NULL DEFAULT 0;
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS image_frame_count integer NOT NULL DEFAULT 0;
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS image_cache_read_tokens integer NOT NULL DEFAULT 0;
