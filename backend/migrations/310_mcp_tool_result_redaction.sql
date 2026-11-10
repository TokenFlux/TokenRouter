-- Store response masking per credential. Existing grants and inserts from older
-- binaries use the same off default as new credentials.
ALTER TABLE mcp_access_tokens
    ADD COLUMN IF NOT EXISTS redact_tool_results BOOLEAN NOT NULL DEFAULT FALSE;

ALTER TABLE mcp_oauth_grants
    ADD COLUMN IF NOT EXISTS redact_tool_results BOOLEAN NOT NULL DEFAULT FALSE;
