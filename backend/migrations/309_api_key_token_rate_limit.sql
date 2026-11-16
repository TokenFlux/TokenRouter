-- Existing credentials keep unrestricted group Token rates until users configure a ceiling.
ALTER TABLE api_keys
    ADD COLUMN IF NOT EXISTS max_token_rate_multiplier double precision NOT NULL DEFAULT 0
    CHECK (max_token_rate_multiplier >= 0 AND max_token_rate_multiplier < 1000000000000);

-- Direct configuration writes invalidate authentication snapshots before subsequent requests reuse them.
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
       OR OLD.max_token_rate_multiplier IS DISTINCT FROM NEW.max_token_rate_multiplier
       OR OLD.billing_mode IS DISTINCT FROM NEW.billing_mode
       OR OLD.preferred_subscription_id IS DISTINCT FROM NEW.preferred_subscription_id THEN
        PERFORM enqueue_auth_cache_invalidation(OLD.key);
        IF NEW.deleted_at IS NULL AND NEW.key IS DISTINCT FROM OLD.key THEN
            PERFORM enqueue_auth_cache_invalidation(NEW.key);
        END IF;
    END IF;
    RETURN NEW;
END;
$$;
