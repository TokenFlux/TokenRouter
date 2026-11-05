// Package grok adapts xAI token grants, request bodies, streamed responses,
// and media exchanges for TokenRouter's upstream callers. Incoming tool
// histories, cache identities, and usage observations need platform-specific
// normalization before gateway and provider services apply their routing or
// persistence rules.
//
// Start with request_patch.go for Responses input policy,
// responses_execute.go for execution and completion results, and oauth.go for
// effective authorization and API URLs. OAuth and sso_device.go handle grant
// exchanges, while billing, quota, and subscription_tier.go interpret account
// observations. Provider services own authorization sessions, credential
// storage, and health updates around those platform operations.
//
// Request patching, model_input.go, and compact.go handle fields, replay, and
// compaction. Cache, chat, and composer_images.go handle identities,
// conversion, and image descriptions. Responses execution shares upstream
// heartbeat filtering with the common OpenAI response readers. Media, video_content.go, voice,
// audio_usage.go, and realtime handle generated content and audio quantities.
// Models, observed_models.go, standalone_search.go, and search_count.go
// handle names, search evidence, and search usage. overrides.go publishes explicit
// startup overrides. cli_identity.go, endpoint_policy.go, and transport_fallback.go
// handle request targets and transport identity; errors.go and failure_classifier.go interpret rejected
// exchanges for their callers.
package grok
