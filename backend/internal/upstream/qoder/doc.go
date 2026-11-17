// Package qoder adapts Qoder authentication, encrypted session headers, stream
// output, and conversation identity for the gateway. Client protocols
// normalize into one site-specific request shape, and observed output and
// usage return to the attempt owner for completion accounting.
//
// Start with execute.go for preparation, cancellation, acceptance, and output
// ownership. Read client.go for signing, HTTP errors, and SSE normalization.
// Read payload.go for protocol input conversion and tool history
// normalization.
//
// Credential workflows use auth.go, auth_status.go, authorization.go,
// oauth.go, credential_builder.go, and refresh_exchange.go. Session headers
// and request encoding use session.go, signature.go, and encoding.go. Site and
// model controls use site.go, model_catalog.go, and thinking.go; models.go
// represents opt-in imported identities. Conversation fingerprints and rollback
// use conversation.go. Responses callers supply their full history.
// stream_tool_text.go recognizes text-encoded
// invocations, while stream_conversion.go and output_observation.go retain output
// and usage evidence. Quota metadata uses quota_usage.go; the localauth package
// isolates manual import and opt-in local tests.
package qoder
