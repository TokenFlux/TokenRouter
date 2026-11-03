-- Persist version exceptions with each accepted package so restart and rollback
-- retain the administrator's decision. Existing installations enforce version bounds.
ALTER TABLE plugin_installations
    ADD COLUMN IF NOT EXISTS ignore_host_version BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS previous_ignore_host_version BOOLEAN NOT NULL DEFAULT FALSE;
