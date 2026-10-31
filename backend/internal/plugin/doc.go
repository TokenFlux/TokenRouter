// Package plugin manages trusted administrator-installed plugins and their host capabilities.
//
// Start here:
//   - manager.go: reconciles installation intent with local packages and process ownership.
//   - service.go and service_versions.go: serialize administration and restore complete release snapshots.
//   - writes.go: commits capability effects and replay evidence under one transaction boundary.
//
// File groups:
//   - installation.go, packages.go, and runtime.go define storage, package, and process contracts.
//   - indexes.go defines catalog transfer contracts and project-owned verification roots.
//   - catalog.go selects source-qualified releases; catalog_service.go, catalog_refresh.go, and catalog_indexes.go own cache and registration coordination.
//   - configuration.go, limits.go, and dev_source.go normalize persistent policy and development records.
//   - grant.go, ledger.go, wallet.go, redeem.go, subscriptions.go, and capabilities.go define caller authority, financial rules, and audit collaboration.
//   - http.go defines transport-independent HTTP contribution values.
//   - tasks.go schedules declared jobs and retains in-memory execution results.
//   - kv.go and kv_maintenance.go enforce isolated JSON storage and sampled quota maintenance.
//   - email.go coordinates durable delivery claims with uncertain external outcomes.
//   - host_queries.go and query_types.go enforce read permissions and whitelist host observations.
//   - options.go and errors.go define runtime defaults and stable management or capability failures.
package plugin
