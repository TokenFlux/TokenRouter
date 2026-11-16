// Package apikey manages gateway credentials, their admission views and cached
// authentication data. Credential creator, team payer and request actor remain
// distinct so gateway checks and billing use the appropriate user.
//
// Begin with authenticate.go to follow identity checks and diagnostic failure
// records. Read api_key.go for persisted fields, hydrated views and copy rules.
// Read service.go for Key configuration writes, funding choices and usage helpers.
//
// auth_cache*.go handles versioned snapshots, local caching, subscriptions and
// durable outbox delivery. lifecycle.go gates authentication work and drains it
// before cache closure. request_limits.go and rediscache maintain admitted
// request leases separately from cached configuration. token_rate_limit.go
// checks current payer-group Token rates before dispatch and provides form previews.
//
// composite.go, model_mapping.go and runtime_group.go resolve group prefixes,
// Key aliases and permitted fallback targets. access_context.go and
// request_metadata.go carry request decisions; reauthenticate.go reloads current
// identity for later connection turns. admin.go, credential_rotation.go and
// managed_key.go handle administrative writes, secret replacement and hidden
// execution Keys.
//
// options.go and clock.go supply startup dependencies and service time; ip.go
// and invalid_auth_abuse_limiter.go check address rules and bound invalid-client
// tracking. snapshot_copy.go provides the conversion copy helpers, while
// published_invalidation.go adapts shared invalidation for other modules.
// postgres persists records and outbox claims; httpapi exposes console DTOs
// and protocol-specific authentication responses.
package apikey
