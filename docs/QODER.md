# Qoder Native Upstream

TokenRouter supports Qoder native upstream accounts through the Qoder COSY gateway path. Public request-side model aliases are mapped to internal Qoder route keys; the internal keys stay hidden unless they are present for compatibility with older saved configuration.

## Account Types

- `cosy` accounts can use a PAT bootstrap or device OAuth credentials.
- Manual import accepts either `pat` by itself, or an existing COSY token set.
- Existing COSY token credentials include `security_oauth_token`, `refresh_token`, `machine_id`, `machine_token`, `machine_type`, `uid` or `aid`, and optional organization metadata.
- Accounts with a `refresh_token` can be refreshed through the normal account refresh action.

## Model Aliases

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

Qoder account `model_mapping` remains optional and only rewrites request models to Qoder route keys or final upstream models. It does not restrict the request model space by itself. Use `model_whitelist` to restrict the final model after mapping; if no whitelist is configured, the account remains unrestricted and public aliases are resolved by the Qoder gateway.

## Billing Scope

Qoder default public aliases, and their built-in Qoder route keys, use Claude Opus 4.8 standard token pricing as the fallback billing basis when no channel/manual pricing entry is configured. The same fallback applies when a custom request alias resolves to a built-in Qoder route key and the custom alias itself has no known price. Operators can override per-alias pricing through the existing channel pricing configuration. Qoder participates in user × platform USD quotas using the final billed balance amount, so quota precheck and post-usage accrual remain consistent for default aliases.

The channel pricing "sync latest models" action returns the default public alias list above. For Qoder, the generated pricing entry is prefilled with the Claude Opus 4.8 fallback price and remains manually editable.

## Operations

Qoder participates in scheduler snapshots, error passthrough rules, and admin user platform usage views under the `qoder` platform key.

Admin account data export/import preserves `qoder` / `cosy` accounts and their credentials for backup migration.
