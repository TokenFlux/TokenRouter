// Package config loads, upgrades, normalizes, and validates versioned YAML
// process configuration before application assembly.
//
// Start here:
//   - config.go: resolves configuration files, runs upfront Normalize, and
//     orchestrates startup Validate checks.
//   - defaults.go: initializes version 2 startup defaults on typed Config values.
//   - upgrade.go: migrates version 1 files and environment inputs to version 2.
//
// File groups:
//   - server.go, auth.go, gateway.go, runtime_domains.go, plugins.go: domain
//     configuration structs, normalization helpers, and read-only validators.
//   - file.go, schema.go, interpolate.go, decode.go, ignored_env.go,
//     oauth_defaults.go: path selection, unknown-key checks, explicit reference
//     interpolation, YAML AST decoding, residual environment warnings, and
//     identity default projections.
//   - upgrade_*.go: version 1 environment, key-path, and YAML AST migrations.
package config
