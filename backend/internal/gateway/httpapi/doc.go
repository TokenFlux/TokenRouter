// Package httpapi binds gateway HTTP protocols to authentication, request
// preparation, selected-provider execution, and client response output. Text
// entries resolve the effective group and model before invoking the selected
// provider's protocol path, while count and media entries retain their own
// admission and completion rules.
//
// Reading starts with route registration and the entry for the client's protocol.
// Request-building adapters supply provider identity, body transformations, and
// outbound headers; output adapters translate responses and preserve the client
// metadata needed by completion and observation. Application assembly supplies
// shared execution and resource dependencies. Per-request and per-attempt state
// remains in the entry and its bound operations rather than in route registration.
//
// Entry points:
//   - routes.go registers text, media, model-list, and realtime routes with admission middleware.
//   - openai_text.go defines text request data and execution interfaces.
//   - openai_responses_execution.go executes a Responses attempt and dispatches the upstream protocol.
//
// File groups:
//   - authorization.go, api_key*.go, and routes*.go handle authentication, key model rewriting, and protocol admission.
//   - messages*.go and gemini_native*.go prepare Antigravity Messages and Gemini entries.
//   - openai_text*.go and openai_protocol_execution*.go handle OpenAI text entries and protocol execution.
//   - openai_request*.go, codex*.go, and session_identity.go prepare outbound messages, client identity, and session IDs.
//   - openai_response*.go, output*.go, and response_tool_restoration.go read responses, restore tool names, and write output.
//   - sse_keepalive.go and stream_heartbeat_accounting.go own text heartbeat timing and retry byte accounting.
//   - compact*.go identifies compact requests and handles recovery and streaming heartbeats.
//   - grok*.go, qoder*.go, and google*.go adapt platform attempts, media, and errors.
//   - media*.go, openai_images*.go, and openai_auxiliary*.go handle images, video, audio, search, and counting.
//   - count_tokens*.go prepares Messages counts, dispatches the selected platform, and releases failed sessions.
//   - live*.go, openai_live*.go, and ws*.go manage realtime connections, frames, and closure.
//   - ops*.go, moderation.go, and cyber*.go handle observation, moderation, and policy blocking.
//   - models*.go, search*.go, and runtime_settings.go expose model catalogs, search responses, and gateway settings.
//   - wait.go, selected_provider_slot.go, and completion_submission.go manage concurrency waits, release, and completion submission.
package httpapi
