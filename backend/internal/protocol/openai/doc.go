// Package openai reads Responses and Chat payloads whose usage, terminal state,
// and output items can arrive separately in buffered JSON or stream events.
// Its codecs retain response identifiers and tool history while adapting the
// lifecycle and field variants accepted by the gateway's HTTP and WebSocket
// readers.
//
// Protocol parsing and repair stay separate from upstream exchanges. Callers
// select credentials, request limits, timing sources, and retry policy before
// passing payloads to these helpers. Some helpers validate complete JSON; others
// inspect paths or literal byte patterns, so their declarations specify the
// input and recovery rules. HTTP readers create lifecycle state for one attempt;
// callers choose the scope of usage accumulation and control update ordering
// and synchronization.
//
// Starting points:
//   - types.go: request, response, and stream event types and JSON codecs.
//   - response_forward_wire.go: response model replacement, SSE parsing, and usage extraction.
//   - tool_continuation.go: context checks for continuing tool calls.
//
// File groups:
//   - types.go, responses_stream_event_wire.go, compatibility_fields.go: protocol fields and JSON codecs.
//   - compaction_trigger.go, encrypted_replay.go, input_helpers.go, previous_response_id.go: input items, compaction, and history replay.
//   - tool_continuation.go, tool_schema.go: tool continuation and parameter schema cleanup.
//   - response_forward_wire.go, response_lifecycle.go, response_terminal.go: response reading, lifecycle events, and terminal events.
//   - sse_data.go, sse_documents.go, chat_stream_compat.go: SSE lines, concatenated documents, and Chat stream compatibility.
//   - ws_events.go, ws_payload.go, ws_warmup.go: WebSocket events, request payloads, and warmup responses.
//   - usage.go, forward_usage.go, image_output.go, visible_output.go: compatible usage normalization, sparse snapshots, image counting, and visible output detection.
//   - codex_limits.go, quota.go, quota_credits.go: quota windows and reset credits.
//   - oauth_values.go, live.go: authentication values and Live session requests.
//   - lenient_json.go, recorded_effort.go, service_tier_validation.go: JSON repair, reasoning effort, and service tier validation.
package openai
