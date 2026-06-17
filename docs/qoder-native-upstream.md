# Qoder Native Upstream

## Status
TokenRouter supports Qoder native upstream accounts and routes them through the Qoder gateway/service path.
Public model names are mapped to internal route keys; the route keys themselves stay internal.

## Supported auth / account shapes
- PAT-based bootstrap: `pat` + machine identity exchange builds a Qoder session.
- Device OAuth / native token import: `security_oauth_token` + `machine_id` + `uid` or `aid`.
- Local auth import: `machine_id` + `auth_dir` can load a stored local identity.
- Preserved metadata when present: `refresh_token`, `machine_token`, `machine_type`, `organization_id`, `organization_name`, `name`, `user_type`, `extra`.

## Model alias mapping
Qoder request models are resolved through TokenRouter aliases, then mapped to internal route keys.
Examples of public aliases include Qoder routing tiers and UI-facing model names such as `qwen3.7-max`, `deepseek-v4-pro`, `glm-5`, and `kimi-k2.6`.
Compatibility aliases are kept for older configs, but the public/default surface prefers the newer readable aliases.

## Usage and billing mapping
Qoder usage details are preserved from upstream where available:
- `prompt_tokens`
- `completion_tokens`
- `total_tokens`
- `prompt_tokens_details.cached_tokens`
- `prompt_tokens_details.cacheable_tokens`
- `completion_tokens_details.reasoning_tokens`

Billing / internal usage split:
- ordinary input tokens = `prompt_tokens - cached_tokens`, clamped at `0`
- cache read input tokens = `cached_tokens`
- output tokens = `completion_tokens`
- `cacheable_tokens` is telemetry only; it is not treated as `cache_creation_input_tokens` unless upstream provides an explicit creation field

Client-visible usage:
- OpenAI-compatible responses keep upstream totals and preserve usage details when possible.
- Anthropic-compatible responses split cached input into `cache_read_input_tokens` and ordinary input into `input_tokens`.
- Do not synthesize `cache_creation_input_tokens` from `cacheable_tokens`.

## Cache control parity
TokenRouter adds Qoder-style `{"type":"ephemeral"}` cache control to the last eligible text block when building requests.
It does not overwrite an existing `cache_control`, and it does not add cache control to tool blocks or other non-text blocks.

## Stable conversation / cache-hit conditions
Qoder cache hits are observed through upstream usage, not forced by a TokenRouter flag. A hit is visible when Qoder returns cached input in `usage.prompt_tokens_details.cached_tokens` (or a compatible cached-token field), which TokenRouter exposes as Anthropic `usage.cache_read_input_tokens` and as OpenAI `usage.prompt_tokens_details.cached_tokens`.

For Claude Code with `claude-opus-4-6` / Qoder `ultimate`, the stable replay conditions are:
- use the same account and API key scope;
- keep a stable conversation key: prefer `metadata.user_id.session_id`, request `prompt_cache_key`, request/header `session_id`, or `conversation_id`;
- keep the same message prefix; append new turns instead of rewriting earlier messages;
- keep tools and non-volatile system text unchanged.

Claude Code may vary the `x-anthropic-billing-header ... cch=...` fragment between turns. TokenRouter ignores only that volatile `cch` value for Qoder conversation matching; other system prompt changes still force a full replay/new Qoder session. First turns normally create/cache prompt state and may report no cache read. A deterministic local smoke test should therefore send two turns with the same `prompt_cache_key` or Claude metadata session, then check that the second response reports non-zero cached tokens.

## OpenAI Chat tool-call compatibility
Some Qoder routes may emit Claude-style tool names such as `Bash` even when an OpenAI Chat client declared a lowercase function name such as `bash`. TokenRouter maps upstream tool-call names back to the declared OpenAI tool names for both streaming and non-streaming Chat Completions responses, so clients like opencode can match the returned `tool_calls[].function.name` against their configured tools.

## Real-client validation notes

Live validation on 2026-06-17 captured real Claude Code 2.1.179 and OpenCode 1.17.7 traffic through TokenRouter + Qoder using isolated config directories and temporary API keys. The latest sanitized evidence is in `docs/qoder-gateway-current-live-validation-20260617.md` and `tmp/qoder-live-current-20260617-130355/`; the earlier full matrix is in `docs/qoder-gateway-live-client-validation-20260617.md` and `tmp/qoder-live-verify-20260617-065425/`.

Observed transport/base URL shape:

- Claude Code used a root Anthropic base URL (`http://127.0.0.1:19180`) and the gateway received `/v1/messages?beta=true` requests.
- OpenCode Anthropic required a `/v1` base URL.
- OpenCode OpenAI Chat and Responses providers both completed tool round trips through the gateway.

Observed successful tool/result characteristics:

- Claude Code: Bash + Read tool round trip, valid `tool_use.input` objects, matching `tool_result.tool_use_id`s, no `Invalid tool parameters`.
- OpenCode: supported Chat/Responses/Anthropic transports completed `glob` + `bash` tool execution without `provider_error`.
- OpenCode `deepseek-v4-pro` and `glm-5.1` chat runs showed cached-token usage in the second turn.
- Direct Anthropic Messages stable-prefix probes showed upstream cache reads on the second turn (`cache_read_input_tokens=5630`); direct Chat probes with a stable prefix still returned zero cached tokens in one run, confirming cache usage must be read from upstream usage fields rather than inferred from key stability alone.

## Local validation
Backend:
- `go test ./internal/pkg/qoder -run 'Qoder|SSE|Usage|Cache|Encode|Decode' -count=1`
- `go test ./internal/service -run '^TestQoder' -count=1`

Frontend:
- `cd frontend && pnpm test:run src/components/keys/__tests__/UseKeyModal.spec.ts src/composables/__tests__/useQoderOAuth.spec.ts`
- `cd frontend && pnpm build`

Full gates:
- `PATH="$(go env GOPATH)/bin:$PATH" make test`
- `make build`

## Known limits
- No dedicated public staging environment was used for this cleanup.
- Qoder behavior can vary by account, model, and route.
- `cacheable_tokens` billing still needs upstream/billing confirmation before it should be treated as a chargeable creation field.
- TokenRouter can keep the Qoder conversation/session stable, but cannot force Qoder to return a cache hit on demand; use the upstream cached-token usage fields as the source of truth.
