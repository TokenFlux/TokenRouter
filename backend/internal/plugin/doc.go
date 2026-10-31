// Package plugin manages trusted administrator-installed plugins and their host capabilities.
//
// Start here:
//   - manager.go: reconciles installation intent with local packages and process ownership.
//   - service.go: serializes administrator installation, configuration, and removal operations.
//   - writes.go: commits capability effects and replay evidence under one transaction boundary.
//
// File groups:
//   - installation.go, packages.go, and runtime.go define storage, package, and process contracts.
//   - configuration.go, limits.go, and dev_source.go normalize persistent policy and development records.
//   - grant.go, ledger.go, wallet.go, subscriptions.go, and capabilities.go define caller authority, financial rules, and audit collaboration.
//   - email.go coordinates durable delivery claims with uncertain external outcomes.
//   - host_queries.go and query_types.go enforce read permissions and whitelist host observations.
//   - options.go and errors.go define runtime defaults and stable management or capability failures.
package plugin
