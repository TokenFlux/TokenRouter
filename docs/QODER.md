# Qoder Native Upstream

TokenRouter supports Qoder native upstream accounts through the Qoder COSY gateway path. Public request-side aliases map to Qoder route keys, and raw route keys remain valid direct request models for compatibility and operations.

## Account Types

- `cosy` accounts can use a PAT bootstrap or device OAuth credentials.
- Manual import accepts either `pat` by itself, or an existing COSY token set.
- Existing COSY token credentials include `security_oauth_token`, `refresh_token`, `machine_id`, `machine_token`, `machine_type`, `uid` or `aid`, and optional organization metadata.
- Accounts with a `refresh_token` can be refreshed through the normal account refresh action.

## Model Aliases and Mapping

Default public aliases are:

- `claude-opus-4-6`
- `auto`
- `performance`
- `efficient`
- `lite`
- `qwen3.7-max`
- `qwen3.7-plus`
- `deepseek-v4-pro`
- `deepseek-v4-flash`
- `glm-5.2`
- `kimi-k2.7-code`
- `minimax-m3`

Qoder account `model_mapping` follows the same rewrite-rule semantics as other platforms:

- key: model name accepted at that routing layer;
- value: final Qoder route/upstream model name;
- the mapping itself does not restrict the request model space.

Use `model_whitelist` when an account must be limited to specific final route/upstream models. The gateway applies mapping first and then checks the whitelist. If no whitelist is configured, the account remains unrestricted. Channel-level mapping is also a one-step rewrite; do not configure alias chains such as `custom -> public alias -> route key`. Configure `model -> upstream route key` directly.

## Billing Scope

Qoder built-in public aliases and their route keys are manual-pricing-only. They do not fall back to LiteLLM, Claude Opus, or any model-file price when no effective channel price is configured.

- Effective channel price means at least one price pointer or valid interval is configured.
- `nil` price fields mean unconfigured.
- A pointer value of `0` means explicitly free and is treated as an effective manual price.
- Empty Qoder channel pricing rows are treated as unconfigured for billing and do not mask an alias-level manual price.
- Non-Qoder requested model names, such as a custom `gpt-5.4` entry mapped to a Qoder route key, continue to use the normal TokenRouter requested-model pricing when no effective Qoder manual price is configured, even if the channel's billing model source is `upstream`.

Pricing precedence for Qoder billing is:

1. requested public/custom alias manual channel price;
2. channel-mapped route key manual channel price;
3. upstream model manual channel price;
4. unpriced / zero-cost usage record.

Unpriced Qoder models are shown as unknown/unpriced in marketplace and admin pricing surfaces. Successful zero-cost Qoder requests still write full usage logs and continue through the normal subscription/balance billing pipeline with a zero billable amount. Qoder remains part of user × platform USD quota accounting when a positive balance-billed amount exists.

## Upstream Account Usage

Qoder has its own upstream monthly credits quota. TokenRouter treats this as account usage/capacity information only; it is separate from TokenRouter user balance, subscription, and user × platform USD quotas.

The account usage view queries Qoder's quota API and stores a last-known snapshot in `account.extra.qoder_quota_snapshot`. If the live query fails, the admin UI can show the cached snapshot together with a degraded usage error. For non-personal zero-quota accounts, `isQuotaExceeded=true` or a depleted positive `userQuota.total` applies the normal account `rate_limited_until` scheduling signal until Qoder's `expiresAt`. The observed `personal_standard` shape with `total=0`, `remaining=0`, and an extremely distant `expiresAt` is display-only until real request errors confirm it. Request-time signals such as code `115`, `agentLimitResetTime`, or HTTP 429 still use the normal account rate-limit cooldown path.

## Operations

Qoder participates in scheduler snapshots, error passthrough rules, failover, and admin user platform usage views under the `qoder` platform key. For retryable upstream failures (Qoder agent limit / 429 / 5xx), the gateway can fail over to another account before any stream chunk has been written; after streaming starts, it returns a stream-aware error instead of switching accounts.

Admin account data export/import preserves `qoder` / `cosy` accounts and their credentials for backup migration.
