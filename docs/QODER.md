# Qoder Native Upstream

TokenRouter supports Qoder native upstream accounts through the Qoder COSY gateway path. Public request-side model aliases are mapped to internal Qoder route keys; the internal keys stay hidden unless they are present for compatibility with older saved configuration.

## Account Types

- `cosy` accounts can use a PAT bootstrap or device OAuth credentials.
- OAuth-style credentials include `security_oauth_token`, `refresh_token`, `machine_id`, `machine_token`, `machine_type`, `uid` or `aid`, and optional organization metadata.

## Model Aliases

Default public aliases include Qoder routing tiers (`auto`, `performance`, `efficient`, `lite`) and UI-facing model names such as `qwen3.7-max`, `deepseek-v4-pro`, `glm-5.2`, and `kimi-k2.7-code`.

Qoder account `model_mapping` remains optional and only rewrites request models to Qoder route keys or final upstream models. It does not restrict the request model space by itself. Use `model_whitelist` to restrict the final model after mapping; if no whitelist is configured, the account remains unrestricted and public aliases are resolved by the Qoder gateway.

## Billing Scope

Qoder default public aliases use Claude Opus 4.8 standard token pricing as the fallback billing basis when no channel/manual pricing entry is configured. Operators can override per-alias pricing through the existing channel pricing configuration. Qoder participates in user × platform USD quotas using the final billed balance amount, so quota precheck and post-usage accrual remain consistent for default aliases.
