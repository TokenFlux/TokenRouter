// Package schema defines TokenRouter's Ent entity fields, relationships, and indexes.
// Ent generation turns these declarations into typed queries and mutations used
// by handwritten repositories. Database initialization applies the numbered SQL
// migrations, so schema declarations and migration changes need to be maintained
// together as the persistence model evolves.
//
// Reading entry points:
//   - user.go: user identity, balance, access limits, and entity relationships.
//   - provider.go: upstream credentials, scheduling state, and provider group membership.
//   - group.go: model groups, request limits, and billing settings.
//
// File groups:
//   - user*.go, auth_identity*.go, pending_auth_session.go, identity_adoption_decision.go: users, login identities, authentication sessions, and profile adoption.
//   - team*.go: teams, members, invitations, and ownership transfers.
//   - api_key*.go, group.go, provider*.go, proxy.go: access keys, groups, upstream providers, and proxies.
//   - tls_fingerprint*.go, error_passthrough_rule.go: TLS handshakes and upstream error passthrough rules.
//   - payment*.go, subscription_plan.go, redeem_code*.go, promo_code*.go: payments, plans, redemption codes, and signup promotions.
//   - creative_run*.go, batch_image*.go: creative jobs, outputs, pending actions, and batch image events.
//   - usage*.go: request usage, usage cleanup tasks, and usage export tasks.
//   - announcement*.go, setting.go, security_secret.go, idempotency_record.go: announcements, settings, secrets, and idempotent request records.
package schema
