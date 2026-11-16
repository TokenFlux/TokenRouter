// Package provider adapts selected providers for gateway request execution and
// completion capture. Request targets combine copied provider configuration with
// attempt routes, while platform policies supply credentials, protocol changes,
// model eligibility, and response observations for the gateway's provider loop.
//
// Start with:
//   - execution_provider.go: execution targets and provider/protocol snapshots.
//   - route_plan.go: candidate routes from group mappings and client protocols.
//   - request_credentials.go: request credentials and failed recovery.
//
// File groups:
//   - execution_*.go, credential_target.go, request_credentials.go,
//     runtime_readers.go, and selection.go: targets, credentials, and selection results.
//   - model_*.go, compact_models.go, and compatible_eligibility.go: mapping, catalog display, and request eligibility.
//   - completion_*.go and ws_result.go: completion snapshots and usage capture.
//   - grok_*.go, qoder_*.go, ollama_wire_policy.go, and anthropic_settings.go: platform settings, encoding, and error policy.
//   - openai_*.go, responses_*.go, codex_request.go, and compat_continuation.go: request normalization, continuation, and failures.
//   - health_observation.go, proxy_response_feedback.go, failure_scope.go, and
//     invalid_json.go: provider health, proxy stream feedback, and failure classification.
//   - image_intent.go, response_image_policy.go, and creative_*.go: image intent, permissions, and creative targets.
//   - fast_policy.go, claude_cache_policy.go, thinking_request.go, and input_tokens.go: service tiers, Claude cache TTL policy, thinking fields, and input-token estimation.
//   - search_runtime.go, standalone_search*.go, and stats_pricing.go: search execution and price lookup.
//   - upstream_*.go and ws_diagnostics.go: upstream context, request IDs, and WebSocket diagnostics.
package provider
