-- 313_api_key_force_claude_1h_cache.sql adds the per-Key forced Claude
-- one-hour prompt-cache column to api_keys and extends cross-instance
-- authentication cache invalidation. Existing credentials receive false so
-- upgrades preserve client-requested cache lifetimes until an owner enables
-- the setting.
ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS force_claude_1h_cache BOOLEAN NOT NULL DEFAULT FALSE;

COMMENT ON COLUMN api_keys.force_claude_1h_cache IS
    'Forces ephemeral Claude prompt-cache markers on requests authenticated with this API key to use a 1h TTL.';

-- enqueue_api_key_auth_cache_invalidation schedules auth-cache eviction when
-- credentials are deleted or when auth-relevant columns, including
-- force_claude_1h_cache, change so gateway nodes drop stale snapshots.
CREATE OR REPLACE FUNCTION enqueue_api_key_auth_cache_invalidation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        PERFORM enqueue_auth_cache_invalidation(OLD.key);
        RETURN OLD;
    END IF;

    IF OLD.key IS DISTINCT FROM NEW.key
       OR OLD.status IS DISTINCT FROM NEW.status
       OR OLD.deleted_at IS DISTINCT FROM NEW.deleted_at
       OR OLD.user_id IS DISTINCT FROM NEW.user_id
       OR OLD.group_id IS DISTINCT FROM NEW.group_id
       OR OLD.ip_whitelist IS DISTINCT FROM NEW.ip_whitelist
       OR OLD.ip_blacklist IS DISTINCT FROM NEW.ip_blacklist
       OR OLD.expires_at IS DISTINCT FROM NEW.expires_at
       OR OLD.concurrency_limit IS DISTINCT FROM NEW.concurrency_limit
       OR OLD.rpm_limit IS DISTINCT FROM NEW.rpm_limit
       OR OLD.billing_mode IS DISTINCT FROM NEW.billing_mode
       OR OLD.preferred_subscription_id IS DISTINCT FROM NEW.preferred_subscription_id
       OR OLD.force_claude_1h_cache IS DISTINCT FROM NEW.force_claude_1h_cache THEN
        PERFORM enqueue_auth_cache_invalidation(OLD.key);
        IF NEW.deleted_at IS NULL AND NEW.key IS DISTINCT FROM OLD.key THEN
            PERFORM enqueue_auth_cache_invalidation(NEW.key);
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
