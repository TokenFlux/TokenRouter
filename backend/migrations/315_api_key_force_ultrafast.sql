-- 315_api_key_force_ultrafast.sql adds the Ultrafast choice to the API Key
-- policy constraint. The migration replaces the check in one transaction so
-- existing policies stay valid and unknown values remain rejected.
ALTER TABLE api_keys
    DROP CONSTRAINT IF EXISTS api_keys_fast_mode_policy_check,
    ADD CONSTRAINT api_keys_fast_mode_policy_check
        CHECK (fast_mode_policy IN ('follow_request', 'force_on', 'force_ultrafast', 'force_off'));
