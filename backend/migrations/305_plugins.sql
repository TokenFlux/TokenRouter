-- Plugin installation intent and business state belong to PostgreSQL. Package
-- files remain local and recover from recorded artifact identities when possible.
CREATE TABLE IF NOT EXISTS plugin_indexes (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    url TEXT NOT NULL UNIQUE,
    public_key TEXT,
    builtin BOOLEAN NOT NULL DEFAULT FALSE,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    cached_document JSONB,
    cached_at TIMESTAMPTZ,
    signature_status VARCHAR(16) NOT NULL DEFAULT 'unknown',
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS plugin_indexes_single_builtin ON plugin_indexes (builtin) WHERE builtin;

CREATE TABLE IF NOT EXISTS plugin_installations (
    plugin_id VARCHAR(80) PRIMARY KEY,
    version VARCHAR(64) NOT NULL,
    source VARCHAR(16) NOT NULL CHECK (source IN ('index', 'upload')),
    index_id BIGINT REFERENCES plugin_indexes(id) ON DELETE SET NULL,
    manifest JSONB NOT NULL,
    manifest_sha256 CHAR(64) NOT NULL,
    artifacts JSONB NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    granted_permissions JSONB NOT NULL DEFAULT '[]',
    config JSONB NOT NULL DEFAULT '{}',
    secret_config TEXT,
    limits JSONB NOT NULL DEFAULT '{}',
    -- A rollback restores configuration and grants together with executable identity.
    previous_version VARCHAR(64),
    previous_source VARCHAR(16),
    previous_index_id BIGINT REFERENCES plugin_indexes(id) ON DELETE SET NULL,
    previous_manifest JSONB,
    previous_manifest_sha256 CHAR(64),
    previous_artifacts JSONB,
    previous_granted_permissions JSONB,
    previous_config JSONB,
    previous_secret_config TEXT,
    installed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_by BIGINT
);

-- KV rows deliberately have no installation foreign key: uninstall can retain
-- business data for a later installation with the same plugin identity.
CREATE TABLE IF NOT EXISTS plugin_kv (
    plugin_id VARCHAR(80) NOT NULL,
    collection VARCHAR(64) NOT NULL,
    key VARCHAR(256) NOT NULL,
    value JSONB NOT NULL,
    version BIGINT NOT NULL DEFAULT 1,
    expires_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (plugin_id, collection, key)
);
CREATE INDEX IF NOT EXISTS plugin_kv_expires_at ON plugin_kv (expires_at) WHERE expires_at IS NOT NULL;

-- The ledger remains after uninstall. Pending email claims cannot be resent
-- automatically after uncertain transport outcomes or process interruption.
CREATE TABLE IF NOT EXISTS plugin_ledger (
    id BIGSERIAL PRIMARY KEY,
    plugin_id VARCHAR(80) NOT NULL,
    idempotency_key VARCHAR(128) NOT NULL,
    kind VARCHAR(24) NOT NULL CHECK (kind IN ('wallet_credit', 'wallet_debit', 'redeem_issue', 'subscription_grant', 'email_send')),
    user_id BIGINT,
    amount NUMERIC(20, 8) NOT NULL,
    request JSONB NOT NULL,
    result JSONB NOT NULL,
    state VARCHAR(16) NOT NULL DEFAULT 'completed' CHECK (state IN ('pending', 'completed', 'failed', 'unknown')),
    reason VARCHAR(200) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (plugin_id, idempotency_key)
);
CREATE INDEX IF NOT EXISTS plugin_ledger_daily ON plugin_ledger (plugin_id, kind, created_at);
CREATE INDEX IF NOT EXISTS plugin_ledger_user_daily ON plugin_ledger (plugin_id, user_id, kind, created_at);
