# Qoder Native Upstream

TokenRouter supports Qoder native upstream accounts through the Qoder COSY gateway path. Public request-side model aliases are mapped to internal Qoder route keys; the internal keys stay hidden unless they are present for compatibility with older saved configuration.

## Account Types

- `cosy` accounts can use a PAT bootstrap, device OAuth credentials, or local Qoder auth import.
- OAuth-style credentials include `security_oauth_token`, `refresh_token`, `machine_id`, `machine_token`, `machine_type`, `uid` or `aid`, and optional organization metadata.
- Local auth import reads Qoder auth data from the configured auth directory and builds a COSY session from it.

## Model Aliases

Default public aliases include Qoder routing tiers (`auto`, `performance`, `efficient`, `lite`) and UI-facing model names such as `qwen3.7-max`, `deepseek-v4-pro`, `glm-5`, and `kimi-k2.6`.

Qoder account `model_mapping` remains optional. When it is unconfigured, TokenRouter treats the account as allowing the current Qoder public aliases. Configure `model_mapping` only when a specific whitelist or alias override is required.

## Billing Scope

Qoder billing, pricing, marketplace pricing, and user platform USD quota policy are intentionally deferred. This integration records upstream usage fields where available, but it does not define Qoder-specific price multipliers, fake official prices, free marketplace pricing, or Qoder platform quota behavior. Because Qoder currently has no non-zero USD pricing basis, `qoder` is intentionally excluded from user × platform USD quota allowlists and admin quota matrices; otherwise precheck limits could be enabled without post-usage accrual.

## Model Sync

The admin Qoder model sync endpoint is optional. It is disabled unless `qoder.model_sync_script_path` is configured with an explicit script path. Deployments without that setting keep the built-in alias list and receive a clear disabled error from the sync endpoint.
