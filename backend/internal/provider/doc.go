// Package provider manages provider configuration, credential refresh, quota
// queries, and scheduling health.
//
// Start with:
//   - record.go: provider records, identity, and availability accessors.
//   - field_policy.go: per-key redaction, configuration merge, duplication,
//     and scheduler metadata rules.
//   - admin_contracts.go: storage, group, and platform operations used by
//     administration.
//   - refresh.go: request-time credential refresh, lock coordination, and
//     conditional persistence.
//
// Files by responsibility:
//   - record*.go: provider records, endpoint/passthrough policies, model
//     mappings, pool retry, temporary suspension rules, and quota windows.
//   - platform_rules.go, kimi_rules.go, zhipu_rules.go, deepseek_rules.go, and
//     opencode.go: platform-owned endpoint, usage, health, and billing policies.
//   - admin*.go, create*.go, management*.go: provider creation, editing,
//     queries, and administration.
//   - archive*.go, codex_import*.go, crs_*.go: import/export, credential
//     matching, and CRS synchronization.
//   - shadow.go, duplicate.go: shared shadow credentials and provider
//     duplication.
//   - credential*.go, token*.go: credential identity, sanitization, cache
//     invalidation, and refresh policy.
//   - refresh*.go, background*.go, managed*.go: refresh coordination,
//     background scans, and recovery.
//   - openai*.go, claude*.go, antigravity*.go, gemini*.go: platform
//     authorization, tokens, and quotas.
//   - authorization_sessions.go: typed OAuth session storage and cleanup
//     lifecycle used by the platform authorization stores.
//   - grok*.go, qoder*.go, vertex*.go: Grok, Qoder, and Vertex credentials and
//     usage.
//   - oauth_usage*.go, usage*.go, ollama_usage*.go: usage reads, caching,
//     window statistics, and conversion.
//   - health*.go, runtime*.go, retry_cooldown.go: failure observations,
//     runtime state, and cooldowns.
//   - model*.go, protocol*.go, endpoint_rule.go: model mapping, protocol
//     selection, and endpoint eligibility.
//   - threshold_settings.go, scheduling_threshold*.go, quota*.go: quota
//     thresholds and automatic pauses.
//   - snapshot.go, clone.go, capacity_snapshot.go: record copies and
//     scheduling capacity data.
//   - testing.go, scheduled*.go, probe_runtime.go, import_probes.go:
//     connection tests, schedules, and probes.
//   - usage_monitor.go and upstream_queries.go: provider usage polling and
//     manual upstream queries.
//   - balance_alert*.go: wallet reminder configuration, polling, and delivery.
package provider
