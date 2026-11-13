// Package session derives gateway affinity and stores continuation and ownership
// evidence across request turns. Parsed metadata, selected content, source
// identity, and protocol-specific digests supply different routing seeds. Callers
// choose the applicable evidence and check provider eligibility or response
// ownership before continuing a request through a remembered target.
//
// Memory stores retain digest prefixes, prompt-cache bindings, opaque state, and
// local connection preferences. Shared cache capabilities provide provider and
// HTTP-owner persistence, first-group isolation, and optional controller or
// blocking operations. Their storage lifetimes and failure behavior are distinct:
// provider-affinity misses permit routing recovery, while ownership failures remain
// part of the caller's authorization decision.
//
// Reading entry points:
//   - hash.go explains metadata, marked-content, source, and readable-text priority.
//   - digest_session_store.go matches growing transcripts and replaces old prefixes.
//   - openai_ws_state_store.go separates shared response identity from local
//     socket and ciphertext state.
//
// File groups:
//   - hash.go, qoder_hash.go, openai_content_seed.go, client_id.go select content
//     or declared identity and validate persisted client IDs.
//   - gemini_digest.go, anthropic_prompt_cache.go, digest_session_store.go build
//     protocol digests and retain continuation bindings.
//   - gemini_tool_calls.go retains original Gemini tool IDs and signatures
//     within the Key and upstream that produced them.
//   - openai_ws_state_store.go, compat_responses.go, reasoning_history.go hold
//     response continuity, rejection sets, opaque state, and recoverable
//     reasoning text.
//   - cyber_keys.go, cyber_transcript.go, cyber_blocks.go, cyber_store_adapter.go
//     derive explicit or transcript block identities and apply scoped storage
//     lookup.
//   - isolation.go, http_response_owner.go compare stored session groups or
//     response owners with the requesting identity.
//   - live.go, codex_turn_origins.go define Live records and remember the latest
//     provider for an HTTP key/session pair.
//   - stores.go defines the cache capabilities consumed by gateway session handling.
package session
